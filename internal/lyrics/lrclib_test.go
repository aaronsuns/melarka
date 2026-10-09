package lyrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

const larkUA = "Melarka/dev (+https://github.com/aaronsuns/melarka)"

type lrclibFake struct {
	mu       sync.Mutex
	reqs     []string
	badUA    []string
	get      func(w http.ResponseWriter)
	search   func(w http.ResponseWriter)
	fixtures map[string][]byte
}

func newLRCLIBFake(t *testing.T) (*lrclibFake, *LRCLIB) {
	t.Helper()
	f := &lrclibFake{fixtures: map[string][]byte{}}
	for _, n := range []string{"lrclib_get", "lrclib_404", "lrclib_search"} {
		b, err := os.ReadFile("testdata/" + n + ".json")
		if err != nil {
			t.Fatal(err)
		}
		f.fixtures[n] = b
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, r.URL.Path+"?"+r.URL.RawQuery)
		if ua := r.Header.Get("User-Agent"); ua != larkUA {
			f.badUA = append(f.badUA, ua)
		}
		f.mu.Unlock()
		switch r.URL.Path {
		case "/api/get":
			f.get(w)
		case "/api/search":
			f.search(w)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return f, &LRCLIB{HTTP: srv.Client(), BaseURL: srv.URL}
}

func (f *lrclibFake) serve(name string, code int) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write(f.fixtures[name])
	}
}

func (f *lrclibFake) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reqs...)
}

func TestLRCLIBGetHit(t *testing.T) {
	f, p := newLRCLIBFake(t)
	f.get = f.serve("lrclib_get", 200)
	f.search = func(w http.ResponseWriter) { t.Error("search called after a /api/get hit") }
	cs, err := p.Search(context.Background(), Query{Title: "Faded", Artist: "Alan Walker", Album: "Faded", DurationS: 212})
	if err != nil || len(cs) != 1 {
		t.Fatalf("%+v %v", cs, err)
	}
	c := cs[0]
	if c.Source != "lrclib" || c.ExternalID != "37495478" || !c.Synced || c.DurationS != 213 || c.Title != "Faded" || c.Artist != "Alan Walker" || !strings.HasPrefix(c.Text, "[00:08.62]") {
		t.Fatalf("%+v", c)
	}
	reqs := f.paths()
	if len(reqs) != 1 || !strings.Contains(reqs[0], "track_name=Faded") || !strings.Contains(reqs[0], "artist_name=Alan+Walker") ||
		!strings.Contains(reqs[0], "album_name=Faded") || !strings.Contains(reqs[0], "duration=212") {
		t.Fatalf("requests %v", reqs)
	}
	if len(f.badUA) != 0 {
		t.Fatalf("user agents %v", f.badUA)
	}
}

func TestLRCLIBFallsBackToSearch(t *testing.T) {
	f, p := newLRCLIBFake(t)
	f.get = f.serve("lrclib_404", 404)
	f.search = f.serve("lrclib_search", 200)
	cs, err := p.Search(context.Background(), Query{Title: "Faded", Artist: "Alan Walker", DurationS: 212})
	if err != nil || len(cs) != 1 || cs[0].Synced || cs[0].Text != "plain only" || cs[0].ExternalID != "1" {
		t.Fatalf("%+v %v", cs, err)
	}
	reqs := f.paths()
	if len(reqs) != 2 || reqs[1] != "/api/search?artist_name=Alan+Walker&track_name=Faded" {
		t.Fatalf("requests %v", reqs)
	}
	if len(f.badUA) != 0 {
		t.Fatalf("user agents %v", f.badUA)
	}
}

func TestLRCLIBErrors(t *testing.T) {
	f, p := newLRCLIBFake(t)
	f.get = func(w http.ResponseWriter) { w.WriteHeader(500) }
	if _, err := p.Search(context.Background(), Query{Title: "Faded", Artist: "Alan Walker", DurationS: 212}); err == nil {
		t.Fatal("500 must be an error")
	}
	f.get = func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body>Cloudflare</body></html>"))
	}
	if _, err := p.Search(context.Background(), Query{Title: "Faded", Artist: "Alan Walker", DurationS: 212}); err == nil {
		t.Fatal("an HTML page must be an error")
	}
}

func TestLRCLIBNoArtistSearchesByTitle(t *testing.T) {
	f, p := newLRCLIBFake(t)
	f.get = func(w http.ResponseWriter) { t.Error("/api/get without an artist") }
	f.search = f.serve("lrclib_search", 200)
	if _, err := p.Search(context.Background(), Query{Title: "Faded", DurationS: 212}); err != nil {
		t.Fatal(err)
	}
	if reqs := f.paths(); len(reqs) != 1 || reqs[0] != "/api/search?track_name=Faded" {
		t.Fatalf("requests %v", reqs)
	}
	if len(f.badUA) != 0 {
		t.Fatalf("user agents %v", f.badUA)
	}
}

func TestBuild(t *testing.T) {
	ps, err := Build([]string{"embedded", "lrclib"}, Deps{})
	if err != nil || len(ps) != 2 || ps[0].Name() != "embedded" || ps[1].Name() != "lrclib" {
		t.Fatalf("%v %v", ps, err)
	}
	if _, err := Build([]string{"lrclib", "nope"}, Deps{}); err == nil {
		t.Fatal("unknown provider accepted")
	}
	if _, err := Build([]string{"lrclib", "lrclib"}, Deps{}); err == nil {
		t.Fatal("duplicate provider accepted")
	}
}

// Any 4xx from /api/get (not only 404) falls back to search.
func TestLRCLIBGet4xxFallsBackToSearch(t *testing.T) {
	f, p := newLRCLIBFake(t)
	f.get = func(w http.ResponseWriter) { w.WriteHeader(400) }
	f.search = f.serve("lrclib_search", 200)
	cs, err := p.Search(context.Background(), Query{Title: "Faded", Artist: "Alan Walker", DurationS: 212})
	if err != nil || len(cs) != 1 || cs[0].Text != "plain only" {
		t.Fatalf("%+v %v", cs, err)
	}
	if reqs := f.paths(); len(reqs) != 2 || !strings.HasPrefix(reqs[1], "/api/search?") {
		t.Fatalf("requests %v", reqs)
	}
}
