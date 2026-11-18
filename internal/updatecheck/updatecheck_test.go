package updatecheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func fakeGitHub(t *testing.T, code int, body string, hits *atomic.Int32, gotReq *atomic.Value) *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if gotReq != nil {
			gotReq.Store(r.Header.Get("User-Agent") + "|" + r.URL.String() + "|" + r.Header.Get("Authorization"))
		}
		w.WriteHeader(code)
		w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

const rel = `{"tag_name":"v0.3.0","html_url":"https://github.com/aaronsuns/melarka/releases/tag/v0.3.0","draft":false,"prerelease":false}`

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"0.2.23", "0.2.23", 0}, {"0.2.24", "0.2.23", 1}, {"0.2.23", "0.2.24", -1},
		{"0.10.0", "0.9.9", 1}, {"1.0.0", "0.99.99", 1}, {"v1.2.3", "1.2.3", 0},
		{"1.0.0-rc1", "1.0.0", -1}, {"1.0.0", "1.0.0-rc1", 1}, {"1.0.0-rc1", "1.0.0-rc2", -1},
		{"1.0.0+build5", "1.0.0", 0},
	} {
		got, err := Compare(c.a, c.b)
		if err != nil || got != c.want {
			t.Errorf("Compare(%q,%q) = %d, %v; want %d", c.a, c.b, got, err, c.want)
		}
	}
	for _, bad := range []string{"dev", "", "1.2", "1.2.3.4", "a.b.c", "01.2.3", "1.2.3-"} {
		if _, err := Compare(bad, "1.0.0"); err == nil {
			t.Errorf("Compare(%q) should fail", bad)
		}
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		latest, cur string
		want        bool
	}{
		{"0.3.0", "0.2.23", true}, {"0.2.23", "0.2.23", false}, {"0.2.22", "0.2.23", false},
		{"0.3.0", "dev", false}, {"0.3.0", "", false}, {"0.3.0", "unknown", false},
		{"0.3.0-rc1", "0.2.23", false}, {"0.3.0", "0.3.0-rc1", true},
	} {
		if got := Newer(c.latest, c.cur); got != c.want {
			t.Errorf("Newer(%q,%q) = %v", c.latest, c.cur, got)
		}
	}
}

func TestNewerRelease(t *testing.T) {
	var hits atomic.Int32
	var req atomic.Value
	ts := fakeGitHub(t, 200, rel, &hits, &req)
	s := &Service{URL: ts.URL + "/repos/x/releases/latest", Version: "0.2.23"}
	if err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := s.Status()
	if !r.Enabled || !r.Newer || r.Latest != "0.3.0" || r.Current != "0.2.23" || r.CheckedAt == 0 ||
		r.URL != "https://github.com/aaronsuns/melarka/releases/tag/v0.3.0" {
		t.Fatalf("%+v", r)
	}
	// Privacy: generic User-Agent, no version, no credentials.
	if got := req.Load().(string); got != "Melarka (+https://github.com/aaronsuns/melarka)|/repos/x/releases/latest|" {
		t.Fatalf("request leaks or changed: %q", got)
	}
}

func TestSameAndOlder(t *testing.T) {
	var hits atomic.Int32
	ts := fakeGitHub(t, 200, rel, &hits, nil)
	for _, cur := range []string{"0.3.0", "0.4.0"} {
		s := &Service{URL: ts.URL, Version: cur}
		if err := s.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
		if r := s.Status(); r.Newer || r.Latest != "0.3.0" {
			t.Errorf("%s: %+v", cur, r)
		}
	}
}

func TestDevBuildNeverNewer(t *testing.T) {
	var hits atomic.Int32
	ts := fakeGitHub(t, 200, rel, &hits, nil)
	s := &Service{URL: ts.URL, Version: "dev"}
	s.Check(context.Background())
	if r := s.Status(); r.Newer || r.Latest != "0.3.0" {
		t.Fatalf("%+v", r)
	}
}

func TestPrereleaseDraftAndJunkIgnored(t *testing.T) {
	for _, body := range []string{
		`{"tag_name":"v0.9.0","html_url":"https://github.com/aaronsuns/melarka/releases/tag/v0.9.0","prerelease":true}`,
		`{"tag_name":"v0.9.0","html_url":"https://github.com/aaronsuns/melarka/releases/tag/v0.9.0","draft":true}`,
		`{"tag_name":"nightly","html_url":"https://github.com/aaronsuns/melarka/releases/tag/nightly"}`,
		`{"tag_name":"v0.9.0-rc1"}`,
	} {
		var hits atomic.Int32
		ts := fakeGitHub(t, 200, body, &hits, nil)
		s := &Service{URL: ts.URL, Version: "0.2.23"}
		if err := s.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
		if r := s.Status(); r.Newer || r.Latest != "" || r.CheckedAt != 0 {
			t.Errorf("%s -> %+v", body, r)
		}
	}
}

func TestForeignLinkReplaced(t *testing.T) {
	var hits atomic.Int32
	ts := fakeGitHub(t, 200, `{"tag_name":"v0.3.0","html_url":"https://evil.example/x"}`, &hits, nil)
	s := &Service{URL: ts.URL, Version: "0.2.23"}
	s.Check(context.Background())
	if r := s.Status(); r.URL != ReleasesURL {
		t.Fatalf("%+v", r)
	}
}

func TestErrorsKeepOldAnswer(t *testing.T) {
	var hits atomic.Int32
	ok := fakeGitHub(t, 200, rel, &hits, nil)
	s := &Service{URL: ok.URL, Version: "0.2.23"}
	s.Check(context.Background())
	for _, c := range []struct {
		code int
		body string
	}{{500, "boom"}, {403, `{"message":"rate limit"}`}, {200, "not json"}} {
		bad := fakeGitHub(t, c.code, c.body, &hits, nil)
		s.URL = bad.URL
		if err := s.Check(context.Background()); err == nil {
			t.Errorf("%d: want error", c.code)
		}
		if r := s.Status(); !r.Newer || r.Latest != "0.3.0" {
			t.Errorf("%d lost the old answer: %+v", c.code, r)
		}
	}
	s.URL = "http://127.0.0.1:0/never"
	if err := s.Check(context.Background()); err == nil {
		t.Error("unreachable server: want error")
	}
}

func TestOffSendsNothing(t *testing.T) {
	var hits atomic.Int32
	ts := fakeGitHub(t, 200, rel, &hits, nil)
	s := &Service{URL: ts.URL, Version: "0.2.23", Off: true}
	s.Run(context.Background()) // returns at once when Off
	if s.Check(context.Background()) != nil || s.CheckIfDue(context.Background()) != nil {
		t.Fatal("off must not error")
	}
	if hits.Load() != 0 {
		t.Fatalf("%d requests while off", hits.Load())
	}
	if r := s.Status(); r.Enabled || r.Newer {
		t.Fatalf("%+v", r)
	}
}

func TestCheckIfDueOncePerDayWithJitter(t *testing.T) {
	var hits atomic.Int32
	ts := fakeGitHub(t, 200, rel, &hits, nil)
	now := time.Unix(1_700_000_000, 0)
	s := &Service{URL: ts.URL, Version: "0.2.23", Now: func() time.Time { return now }, Jitter: func() time.Duration { return time.Hour }}
	ctx := context.Background()
	s.CheckIfDue(ctx)
	s.CheckIfDue(ctx)
	if hits.Load() != 1 {
		t.Fatalf("first start: %d requests", hits.Load())
	}
	now = now.Add(24*time.Hour + 30*time.Minute) // inside the jitter
	s.CheckIfDue(ctx)
	if hits.Load() != 1 {
		t.Fatalf("before day+jitter: %d requests", hits.Load())
	}
	now = now.Add(31 * time.Minute)
	s.CheckIfDue(ctx)
	if hits.Load() != 2 {
		t.Fatalf("after day+jitter: %d requests", hits.Load())
	}
}

func TestFailureRetriedNextTick(t *testing.T) {
	var hits atomic.Int32
	bad := fakeGitHub(t, 500, "", &hits, nil)
	s := &Service{URL: bad.URL, Version: "0.2.23"}
	s.CheckIfDue(context.Background())
	s.CheckIfDue(context.Background())
	if hits.Load() != 2 {
		t.Fatalf("a failed check must stay due: %d", hits.Load())
	}
}
