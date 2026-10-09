package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/testutil"
)

func newStore(t *testing.T) *Store {
	return &Store{DB: testutil.DB(t), Now: func() time.Time { return time.Unix(1_700_000_000, 0) }}
}

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "s3cret") || VerifyPassword(h, "wrong") {
		t.Fatal("verify")
	}
}

func TestLoginUnknownUser(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := s.CreateUser(ctx, "bob", "pw", "member"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Login(ctx, "nosuchuser", "whatever", "iPhone"); err != ErrBadCredentials {
		t.Fatalf("unknown user err=%v", err)
	}
}

func TestLoginAuthenticateLogout(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := s.CreateUser(ctx, "bob", "pw", "member"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "Bob", "pw", "member"); err != ErrExists {
		t.Fatalf("dup err=%v", err)
	}
	if _, _, err := s.Login(ctx, "bob", "nope", "iPhone"); err != ErrBadCredentials {
		t.Fatalf("err=%v", err)
	}
	tok, u, err := s.Login(ctx, "BOB", "pw", "iPhone")
	if err != nil || u.Role != "member" {
		t.Fatalf("login %v %+v", err, u)
	}
	got, dev, err := s.Authenticate(ctx, tok)
	if err != nil || got.ID != u.ID || dev == 0 {
		t.Fatalf("auth %v", err)
	}
	var stored string
	s.DB.QueryRow("SELECT token_hash FROM devices").Scan(&stored)
	if stored == tok {
		t.Fatal("raw token stored")
	}
	if err := s.Logout(ctx, dev); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Authenticate(ctx, tok); err != ErrBadCredentials {
		t.Fatalf("after logout err=%v", err)
	}
}

func TestSetPasswordRevokesDevices(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u, _ := s.CreateUser(ctx, "alice", "pw", "admin")
	tok, _, _ := s.Login(ctx, "alice", "pw", "phone")
	if err := s.SetPassword(ctx, u.ID, "new"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Authenticate(ctx, tok); err == nil {
		t.Fatal("old token still valid")
	}
	if _, _, err := s.Login(ctx, "alice", "new", "phone"); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureAdminOnlyOnce(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if err := s.EnsureAdmin(ctx, "alice", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureAdmin(ctx, "other", "pw"); err != nil {
		t.Fatal(err)
	}
	us, _ := s.Users(ctx)
	if len(us) != 1 || us[0].Username != "alice" {
		t.Fatalf("users=%+v", us)
	}
}

// Review fix Important-1: after 5 consecutive wrong-password attempts for a
// username, further attempts within 30s of the last failure must be
// refused (ErrThrottled) without running argon2 at all -- even a correct
// password must not get through the backoff window. A success resets the
// counter.
func TestLoginPerUsernameBackoff(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	s := &Store{DB: testutil.DB(t), Now: func() time.Time { return now }}
	if _, err := s.CreateUser(ctx, "bob", "pw", "member"); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		if _, _, err := s.Login(ctx, "bob", "wrong", "x"); err != ErrBadCredentials {
			t.Fatalf("attempt %d: err=%v, want ErrBadCredentials", i+1, err)
		}
	}
	// 6th rapid wrong attempt: throttled, not another argon2-costed failure.
	if _, _, err := s.Login(ctx, "bob", "wrong", "x"); err != ErrThrottled {
		t.Fatalf("6th attempt err=%v, want ErrThrottled", err)
	}
	// The correct password is refused too while the window is open.
	if _, _, err := s.Login(ctx, "BOB", "pw", "x"); err != ErrThrottled {
		t.Fatalf("correct password during window err=%v, want ErrThrottled", err)
	}

	// Advance the clock past the 30s window: the correct password now works.
	now = now.Add(31 * time.Second)
	if _, _, err := s.Login(ctx, "bob", "pw", "x"); err != nil {
		t.Fatalf("after window err=%v", err)
	}

	// Success reset the counter: 5 more wrong attempts are needed to
	// re-trigger the throttle, not just one.
	for i := 0; i < 4; i++ {
		if _, _, err := s.Login(ctx, "bob", "wrong", "x"); err != ErrBadCredentials {
			t.Fatalf("post-reset attempt %d: err=%v", i+1, err)
		}
	}
	if _, _, err := s.Login(ctx, "bob", "wrong", "x"); err != ErrBadCredentials {
		t.Fatalf("5th post-reset attempt (not yet throttled) err=%v", err)
	}
	if _, _, err := s.Login(ctx, "bob", "pw", "x"); err != ErrThrottled {
		t.Fatalf("6th post-reset attempt err=%v, want ErrThrottled", err)
	}
}

// Review fix Important-1: Login must not run more than verifySlotCount
// argon2id verifications concurrently. Deterministic via the package-level
// verify hook and a shrunk slotWait, instead of racing real argon2 timing.
func TestLoginSemaphoreBoundsConcurrency(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: testutil.DB(t), Now: func() time.Time { return time.Unix(1_700_000_000, 0) }}
	for i := 0; i < verifySlotCount+1; i++ {
		if _, err := s.CreateUser(ctx, usernameFor(i), "pw", "member"); err != nil {
			t.Fatal(err)
		}
	}

	oldVerify, oldWait := verify, slotWait
	t.Cleanup(func() { verify = oldVerify; slotWait = oldWait })
	slotWait = 100 * time.Millisecond

	release := make(chan struct{})
	var inFlight int32
	var mu sync.Mutex
	var maxInFlight int32
	verify = func(hash, pw string) bool {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()
		<-release
		mu.Lock()
		inFlight--
		mu.Unlock()
		return true
	}

	var wg sync.WaitGroup
	for i := 0; i < verifySlotCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Login(ctx, usernameFor(i), "pw", "x")
		}(i)
	}
	// Give the four goroutines time to all be blocked inside verify holding
	// their slots (they must be -- there are exactly verifySlotCount of
	// them, one each).
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := inFlight
		mu.Unlock()
		if n == int32(verifySlotCount) {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only %d of %d verifications ever became in-flight", n, verifySlotCount)
		case <-time.After(time.Millisecond):
		}
	}

	// A 5th concurrent attempt must not get a slot within slotWait.
	if _, _, err := s.Login(ctx, usernameFor(verifySlotCount), "pw", "x"); err != ErrBusy {
		t.Fatalf("5th concurrent login err=%v, want ErrBusy", err)
	}

	close(release)
	wg.Wait()
	if maxInFlight > int32(verifySlotCount) {
		t.Fatalf("max concurrent verifications=%d, want <= %d", maxInFlight, verifySlotCount)
	}
}

func usernameFor(i int) string { return "user" + string(rune('a'+i)) }

// Review fix Minor-5: changePassword must be able to check the caller's
// current password without leaving a throwaway device row behind (that used
// to happen because it reused Login, which always creates a device on
// success).
func TestCheckPasswordNoDeviceRow(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u, err := s.CreateUser(ctx, "alice", "pw", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Login(ctx, "alice", "pw", "phone"); err != nil {
		t.Fatal(err)
	}

	if ok, err := s.CheckPassword(ctx, u.ID, "wrong"); err != nil || ok {
		t.Fatalf("wrong password ok=%v err=%v", ok, err)
	}
	if ok, err := s.CheckPassword(ctx, u.ID, "pw"); err != nil || !ok {
		t.Fatalf("right password ok=%v err=%v", ok, err)
	}

	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE user_id=?`, u.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("CheckPassword must not create device rows, devices=%d", n)
	}
}

func TestMiddlewareAndRequireAdmin(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	s.CreateUser(ctx, "kid", "pw", "member")
	tok, _, _ := s.Login(ctx, "kid", "pw", "ipad")
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(UserFrom(r.Context()).Username)) })

	rec := httptest.NewRecorder()
	s.Middleware(ok).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Fatalf("no token code=%d", rec.Code)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	s.Middleware(ok).ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "kid" {
		t.Fatalf("bearer code=%d body=%q", rec.Code, rec.Body)
	}

	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
	rec = httptest.NewRecorder()
	s.Middleware(RequireAdmin(ok)).ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("member on admin route code=%d", rec.Code)
	}
}
