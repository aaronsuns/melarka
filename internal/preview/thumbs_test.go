package preview

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/config"
)

var thumbJPEG = append([]byte{0xFF, 0xD8, 0xFF}, bytes.Repeat([]byte{1}, 100)...)

func TestThumbnailProxyFetchesOnceAndCaches(t *testing.T) {
	ctx := context.Background()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/rvOZh8idOrU/mqdefault.jpg" {
			http.NotFound(w, r)
			return
		}
		time.Sleep(20 * time.Millisecond) // let the other callers join the flight
		w.Write(thumbJPEG)
	}))
	defer srv.Close()
	th := &Thumbs{Dir: t.TempDir(), BaseURL: srv.URL}
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			p, err := th.Path(ctx, "rvOZh8idOrU")
			if err != nil || !strings.HasPrefix(p, th.Dir) {
				t.Errorf("%q %v", p, err)
			}
		})
	}
	wg.Wait()
	if hits.Load() != 1 {
		t.Fatalf("fetched %d times, want 1 (single flight + cache)", hits.Load())
	}
	p, err := th.Path(ctx, "rvOZh8idOrU")
	if b, _ := os.ReadFile(p); err != nil || !bytes.Equal(b, thumbJPEG) || hits.Load() != 1 {
		t.Fatalf("cached read: %v %d hits", err, hits.Load())
	}
}

func TestThumbnailProxyRefusesBadIDsAndNonJPEG(t *testing.T) {
	ctx := context.Background()
	var body atomic.Value
	body.Store([]byte("<html>not an image</html>"))
	var paths []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.String())
		mu.Unlock()
		w.Write(body.Load().([]byte))
	}))
	defer srv.Close()
	th := &Thumbs{Dir: t.TempDir(), BaseURL: srv.URL}
	for _, id := range []string{"../../etc/pa", "a/b", "", "rvOZh8idOrU?x=1", "http://evil", "rvOZh8idOr"} {
		if _, err := th.Path(ctx, id); !errors.Is(err, ErrBadVideo) {
			t.Errorf("%q: %v", id, err)
		}
	}
	if len(paths) != 0 {
		t.Fatalf("a bad id was fetched: %v", paths)
	}
	if _, err := th.Path(ctx, "rvOZh8idOrU"); !errors.Is(err, ErrNotFound) {
		t.Errorf("non-JPEG body accepted: %v", err)
	}
	// A body over 1 MiB is refused too, and nothing is left in Dir.
	// (Another id: the first one is now a remembered miss.)
	body.Store(append([]byte{0xFF, 0xD8, 0xFF}, bytes.Repeat([]byte{1}, 1<<20)...))
	if _, err := th.Path(ctx, "rvOZh8idOrV"); !errors.Is(err, ErrNotFound) {
		t.Errorf("oversized body accepted: %v", err)
	}
	if ents, _ := os.ReadDir(th.Dir); len(ents) != 0 {
		t.Errorf("left behind: %v", ents)
	}
	if len(paths) != 2 || paths[0] != "/rvOZh8idOrU/mqdefault.jpg" || paths[1] != "/rvOZh8idOrV/mqdefault.jpg" {
		t.Errorf("fetched %v", paths)
	}
}

func TestThumbnailProxyMissingAndRedirect(t *testing.T) {
	ctx := context.Background()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a redirect was followed to %s", r.URL)
		w.Write(thumbJPEG)
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/redirect_1/") {
			http.Redirect(w, r, target.URL+"/x.jpg", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	th := &Thumbs{Dir: t.TempDir(), BaseURL: srv.URL}
	for _, id := range []string{"missing_000", "redirect_1x"} {
		if _, err := th.Path(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", id, err)
		}
	}
}

func TestThumbnailProxyRefetchesWhenOldAndSweeps(t *testing.T) {
	ctx := context.Background()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write(thumbJPEG)
	}))
	defer srv.Close()
	now := time.Now()
	th := &Thumbs{Dir: t.TempDir(), BaseURL: srv.URL, MaxAge: time.Hour, Now: func() time.Time { return now }}
	if _, err := th.Path(ctx, "rvOZh8idOrU"); err != nil {
		t.Fatal(err)
	}
	if _, err := th.Path(ctx, "aaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	// A stray temp file from a crash.
	os.WriteFile(filepath.Join(th.Dir, ".tmp-123"), []byte("x"), 0o644)
	now = now.Add(30 * time.Minute)
	if _, err := th.Path(ctx, "aaaaaaaaaaa"); err != nil || hits.Load() != 2 {
		t.Fatalf("fresh one refetched: %v %d", err, hits.Load())
	}
	if n := th.Sweep(); n != 1 { // only the temp file
		t.Fatalf("swept %d, want 1", n)
	}
	now = now.Add(time.Hour)
	_, err := th.Path(ctx, "aaaaaaaaaaa")
	th.wait() // the old copy is served at once, refreshed behind
	if err != nil || hits.Load() != 3 {
		t.Fatalf("old one not refetched: %v %d", err, hits.Load())
	}
	if n := th.Sweep(); n != 1 { // rvOZh8idOrU is old; aaaaaaaaaaa was just refetched
		t.Fatalf("swept %d, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(th.Dir, "rvOZh8idOrU.jpg")); !os.IsNotExist(err) {
		t.Fatalf("old thumbnail kept: %v", err)
	}
}

func TestThumbnailProxyServesStaleWhenRefetchFails(t *testing.T) {
	ctx := context.Background()
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "nope", 500)
			return
		}
		w.Write(thumbJPEG)
	}))
	defer srv.Close()
	now := time.Now()
	th := &Thumbs{Dir: t.TempDir(), BaseURL: srv.URL, MaxAge: time.Hour, Now: func() time.Time { return now }}
	if _, err := th.Path(ctx, "rvOZh8idOrU"); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	now = now.Add(2 * time.Hour)
	if p, err := th.Path(ctx, "rvOZh8idOrU"); err != nil || p == "" {
		t.Fatalf("stale copy not served: %q %v", p, err)
	}
	th.wait()
}

func TestDefaultThumbURLMatchesConfig(t *testing.T) {
	t.Setenv("LARK_DATA_DIR", t.TempDir())
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.YouTubeThumbURL != DefaultThumbURL {
		t.Fatalf("config default %q, preview default %q", c.YouTubeThumbURL, DefaultThumbURL)
	}
}

func TestRunSweepsThumbnails(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "rvOZh8idOrU.jpg")
	os.WriteFile(old, thumbJPEG, 0o644)
	past := time.Now().Add(-30 * 24 * time.Hour)
	os.Chtimes(old, past, past)
	s := newEnv(t).svc
	s.Thumbs = &Thumbs{Dir: dir}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(old); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Run never swept the thumbnails")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

// Fix round 1: misses are remembered, fetches are bounded, a stale copy is
// served at once and refreshed behind, and the file cap holds on write.

func TestThumbnailMissIsRememberedForTenMinutes(t *testing.T) {
	ctx := context.Background()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	now := time.Now()
	th := &Thumbs{Dir: t.TempDir(), BaseURL: srv.URL, Now: func() time.Time { return now }}
	for range 3 {
		if _, err := th.Path(ctx, "missing_000"); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("%d upstream hits, want 1", hits.Load())
	}
	now = now.Add(11 * time.Minute)
	th.Path(ctx, "missing_000")
	if hits.Load() != 2 {
		t.Fatalf("%d upstream hits after the TTL, want 2", hits.Load())
	}
}

func TestThumbnailMissesAreCappedAndSwept(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer srv.Close()
	now := time.Now()
	th := &Thumbs{Dir: t.TempDir(), BaseURL: srv.URL, Now: func() time.Time { return now }, maxMisses: 3}
	for _, id := range []string{"missing_000", "missing_001", "missing_002", "missing_003", "missing_004"} {
		th.Path(ctx, id)
	}
	if n := th.missCount(); n > 3 {
		t.Fatalf("%d misses kept, cap 3", n)
	}
	now = now.Add(11 * time.Minute)
	th.Sweep()
	if n := th.missCount(); n != 0 {
		t.Fatalf("%d expired misses left after Sweep", n)
	}
}

func TestThumbnailFetchesAreBounded(t *testing.T) {
	ctx := context.Background()
	var inflight, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		inflight.Add(-1)
		w.Write(thumbJPEG)
	}))
	defer srv.Close()
	th := &Thumbs{Dir: t.TempDir(), BaseURL: srv.URL}
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			id := "bounded_" + string(rune('a'+i)) + "xx"
			if _, err := th.Path(ctx, id); err != nil {
				t.Errorf("%s: %v", id, err)
			}
		})
	}
	wg.Wait()
	if p := peak.Load(); p > 4 || p < 2 {
		t.Fatalf("peak %d concurrent fetches, want 2..4", p)
	}
}

func TestThumbnailSlotWaitIsBounded(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Write(thumbJPEG)
	}))
	defer srv.Close()
	th := &Thumbs{Dir: t.TempDir(), BaseURL: srv.URL, SlotWait: 50 * time.Millisecond}
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() { th.Path(context.Background(), "blocking_0"+string(rune('0'+i))) })
	}
	for th.busySlots() < 4 {
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	if _, err := th.Path(context.Background(), "waiting_000"); !errors.Is(err, ErrNotFound) || !errors.Is(err, ErrBusy) || time.Since(start) > 2*time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
	// A caller whose request ends stops waiting at once.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := th.Path(cctx, "waiting_001"); err == nil {
		t.Fatal("a cancelled caller got a thumbnail")
	}
	close(release)
	wg.Wait()
	// Busy is not a miss: the id is fetched once a slot is free.
	if _, err := th.Path(context.Background(), "waiting_000"); err != nil {
		t.Fatalf("not fetched after the slots freed: %v", err)
	}
}

func TestThumbnailStaleIsServedAtOnceAndRefreshedBehind(t *testing.T) {
	ctx := context.Background()
	fresh := append([]byte{0xFF, 0xD8, 0xFF}, bytes.Repeat([]byte{9}, 10)...)
	release := make(chan struct{})
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-release
		w.Write(fresh)
	}))
	defer srv.Close()
	now := time.Now()
	th := &Thumbs{Dir: t.TempDir(), BaseURL: srv.URL, MaxAge: time.Hour, Now: func() time.Time { return now }}
	p := filepath.Join(th.Dir, "rvOZh8idOrU.jpg")
	os.WriteFile(p, thumbJPEG, 0o644)
	old := now.Add(-2 * time.Hour)
	os.Chtimes(p, old, old)
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			got, err := th.Path(ctx, "rvOZh8idOrU")
			if err != nil || got != p {
				t.Errorf("%q %v", got, err)
			}
		})
	}
	wg.Wait() // every caller answered while the upstream is still blocked
	close(release)
	th.wait()
	if b, _ := os.ReadFile(p); !bytes.Equal(b, fresh) || hits.Load() != 1 {
		t.Fatalf("refresh: %d hits, file %v", hits.Load(), b)
	}
}

func TestThumbnailStaleRefreshFailureIsAMiss(t *testing.T) {
	ctx := context.Background()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "down", 500)
	}))
	defer srv.Close()
	now := time.Now()
	th := &Thumbs{Dir: t.TempDir(), BaseURL: srv.URL, MaxAge: time.Hour, Now: func() time.Time { return now }}
	p := filepath.Join(th.Dir, "rvOZh8idOrU.jpg")
	os.WriteFile(p, thumbJPEG, 0o644)
	old := now.Add(-2 * time.Hour)
	os.Chtimes(p, old, old)
	for range 3 {
		if got, err := th.Path(ctx, "rvOZh8idOrU"); err != nil || got != p {
			t.Fatalf("%q %v", got, err)
		}
		th.wait()
	}
	if hits.Load() != 1 {
		t.Fatalf("%d refresh attempts within the miss TTL, want 1", hits.Load())
	}
}

func TestThumbnailFileCapHoldsOnWrite(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(thumbJPEG) }))
	defer srv.Close()
	th := &Thumbs{Dir: t.TempDir(), BaseURL: srv.URL, maxFiles: 5}
	for i := range 12 {
		th.Path(ctx, "capped_0"+string(rune('a'+i))+"xx")
		th.wait()
		if n := countJPEG(t, th.Dir); n > 5 {
			t.Fatalf("%d files after %d writes, cap 5", n, i+1)
		}
	}
	// Room is made, not refused for good: the newest one is there.
	if _, err := os.Stat(filepath.Join(th.Dir, "capped_0lxx.jpg")); err != nil {
		t.Fatalf("newest not stored: %v", err)
	}
}

func countJPEG(t *testing.T, dir string) int {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	n := 0
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".jpg") {
			n++
		}
	}
	return n
}

type panicDoer struct{}

func (panicDoer) Do(*http.Request) (*http.Response, error) { panic("boom") }

// A panicking fetch on the request path gives its slot back (the panic goes
// on to the HTTP server's recoverer); four of them must not wedge the proxy.
func TestThumbnailPanicReleasesTheSlot(t *testing.T) {
	th := &Thumbs{Dir: t.TempDir(), HTTP: panicDoer{}}
	for i := range thumbSlots + 1 {
		func() {
			defer func() { _ = recover() }()
			th.Path(context.Background(), "panic0000"+string(rune('a'+i))+"x")
		}()
	}
	if n := len(th.slots); n != 0 {
		t.Fatalf("%d slots still held after panics", n)
	}
}

// The stale refresh runs in its own goroutine: a panic there is recovered
// (not a crashed server), gives its slot back and counts as a miss.
func TestThumbnailStaleRefreshPanicIsRecovered(t *testing.T) {
	now := time.Now()
	th := &Thumbs{Dir: t.TempDir(), HTTP: panicDoer{}, MaxAge: time.Hour, Now: func() time.Time { return now }}
	p := filepath.Join(th.Dir, "rvOZh8idOrU.jpg")
	os.WriteFile(p, thumbJPEG, 0o644)
	old := now.Add(-2 * time.Hour)
	os.Chtimes(p, old, old)
	if got, err := th.Path(context.Background(), "rvOZh8idOrU"); err != nil || got != p {
		t.Fatalf("%q %v", got, err)
	}
	th.wait()
	if n := len(th.slots); n != 0 || th.missCount() != 1 {
		t.Fatalf("slots %d, misses %d", n, th.missCount())
	}
}
