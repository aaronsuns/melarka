package usageping

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/testutil"
)

type fakeSite struct {
	mu   sync.Mutex
	got  []Payload
	code int
}

func (f *fakeSite) server(t *testing.T) *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p Payload
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&p) != nil {
			t.Errorf("bad ping %s", r.Method)
		}
		f.mu.Lock()
		f.got = append(f.got, p)
		code := f.code
		f.mu.Unlock()
		if code == 0 {
			code = 204
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestOnByDefaultForEveryInstall(t *testing.T) {
	s := &Service{DB: testutil.DB(t)}
	if on, err := s.Enabled(context.Background()); err != nil || !on {
		t.Fatalf("not on by default: %v %v", on, err)
	}
}

func TestEnvOffDeletesTheIDAndNeverSends(t *testing.T) {
	site := &fakeSite{}
	s := &Service{DB: testutil.DB(t), URL: site.server(t).URL, Version: "1", Off: true}
	ctx := context.Background()
	s.Run(ctx) // returns at once when Off
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM usage_ping`).Scan(&n)
	if n != 0 {
		t.Fatal("the id outlived LARK_USAGE_PING=off")
	}
	if on, _ := s.Enabled(ctx); on {
		t.Fatal("enabled while off by env")
	}
	if err := s.SetEnabled(ctx, true); !errors.Is(err, ErrOff) {
		t.Fatalf("switch overrode the env: %v", err)
	}
	if sent, _ := s.SendIfDue(ctx); sent || len(site.got) != 0 {
		t.Fatal("sent while off by env")
	}
}

func TestPayloadHasOnlyCoarseNumbers(t *testing.T) {
	site := &fakeSite{}
	s := &Service{DB: testutil.DB(t), URL: site.server(t).URL, Version: "1", InContainer: func() bool { return true }}
	ctx := context.Background()
	if sent, err := s.SendIfDue(ctx); err != nil || !sent {
		t.Fatalf("%v %v", sent, err)
	}
	p := site.got[0]
	if p.OS != runtime.GOOS || p.Arch != runtime.GOARCH || !p.Container || p.Users != "1" || p.Tracks != "0" {
		t.Fatalf("payload %+v", p)
	}
	for n, want := range map[int]string{0: "1", 1: "1", 2: "2-5", 5: "2-5", 6: "6-20", 20: "6-20", 21: "21+"} {
		if got := UsersRange(n); got != want {
			t.Errorf("UsersRange(%d) = %s", n, got)
		}
	}
	for n, want := range map[int]string{0: "0", 1: "1-99", 99: "1-99", 100: "100-999", 9999: "1000-9999", 10000: "10000+"} {
		if got := TracksRange(n); got != want {
			t.Errorf("TracksRange(%d) = %s", n, got)
		}
	}
}

func TestSendsIDAndVersionOnlyThenWaitsAWeek(t *testing.T) {
	site := &fakeSite{}
	now := time.Unix(1_800_000_000, 0)
	s := &Service{DB: testutil.DB(t), URL: site.server(t).URL, Version: "1.2.3", Now: func() time.Time { return now }}
	ctx := context.Background()
	s.SetEnabled(ctx, false) // a fresh id, not the migration's
	if err := s.SetEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if sent, err := s.SendIfDue(ctx); err != nil || !sent {
		t.Fatalf("first ping: %v %v", sent, err)
	}
	if len(site.got) != 1 || site.got[0].Version != "1.2.3" || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(site.got[0].ID) {
		t.Fatalf("payload %+v", site.got)
	}
	now = now.Add(Interval - time.Minute)
	if sent, _ := s.SendIfDue(ctx); sent {
		t.Fatal("pinged again within the week")
	}
	now = now.Add(2 * time.Minute)
	if sent, _ := s.SendIfDue(ctx); !sent || len(site.got) != 2 || site.got[1].ID != site.got[0].ID {
		t.Fatalf("weekly ping: %v %+v", sent, site.got)
	}
	// Turning it on again keeps the id.
	s.SetEnabled(ctx, true)
	var id string
	s.DB.QueryRow(`SELECT install_id FROM usage_ping`).Scan(&id)
	if id != site.got[0].ID {
		t.Fatal("enabling twice changed the id")
	}
}

func TestOffForgetsTheIDAndOnAgainIsANewInstall(t *testing.T) {
	site := &fakeSite{}
	s := &Service{DB: testutil.DB(t), URL: site.server(t).URL, Version: "1"}
	ctx := context.Background()
	s.SetEnabled(ctx, true)
	s.SendIfDue(ctx)
	if err := s.SetEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM usage_ping`).Scan(&n)
	if n != 0 {
		t.Fatal("the id outlived turning it off")
	}
	if sent, _ := s.SendIfDue(ctx); sent {
		t.Fatal("pinged after turning it off")
	}
	s.SetEnabled(ctx, true)
	s.SendIfDue(ctx)
	if len(site.got) != 2 || site.got[1].ID == site.got[0].ID {
		t.Fatalf("expected a new id: %+v", site.got)
	}
}

func TestAFailedPingIsTriedAgain(t *testing.T) {
	site := &fakeSite{code: 500}
	s := &Service{DB: testutil.DB(t), URL: site.server(t).URL, Version: "1"}
	ctx := context.Background()
	s.SetEnabled(ctx, true)
	if sent, err := s.SendIfDue(ctx); sent || err == nil {
		t.Fatalf("a 500 counted as sent: %v %v", sent, err)
	}
	site.code = 204
	if sent, err := s.SendIfDue(ctx); !sent || err != nil {
		t.Fatalf("retry: %v %v", sent, err)
	}
}

func TestRunPingsRightAfterTurningOn(t *testing.T) {
	site := &fakeSite{}
	s := &Service{DB: testutil.DB(t), URL: site.server(t).URL, Version: "1"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.SetEnabled(context.Background(), false)
	go func() { s.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	if err := s.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		site.mu.Lock()
		n := len(site.got)
		site.mu.Unlock()
		if n == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no ping after turning it on")
}
