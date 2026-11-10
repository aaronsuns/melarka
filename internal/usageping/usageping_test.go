package usageping

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
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

func TestOffByDefaultAndNothingSent(t *testing.T) {
	site := &fakeSite{}
	s := &Service{DB: testutil.DB(t), URL: site.server(t).URL, Version: "1.2.3"}
	ctx := context.Background()
	if on, err := s.Enabled(ctx); err != nil || on {
		t.Fatalf("enabled by default: %v %v", on, err)
	}
	if sent, err := s.SendIfDue(ctx); err != nil || sent || len(site.got) != 0 {
		t.Fatalf("sent while off: %v %v %v", sent, err, site.got)
	}
}

func TestSendsIDAndVersionOnlyThenWaitsAWeek(t *testing.T) {
	site := &fakeSite{}
	now := time.Unix(1_800_000_000, 0)
	s := &Service{DB: testutil.DB(t), URL: site.server(t).URL, Version: "1.2.3", Now: func() time.Time { return now }}
	ctx := context.Background()
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
