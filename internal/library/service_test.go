package library

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/media"
)

func newService(t *testing.T) (*Service, *env) {
	e := newEnv(t, fakeProber{})
	return &Service{Store: e.st, Scanner: e.sc, Log: e.sc.Log, Debounce: 100 * time.Millisecond, Interval: time.Hour}, e
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition not met in 5s")
}

func trackCount(e *env) int {
	var n int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM tracks`).Scan(&n)
	return n
}

func TestScanNowRecordsStatus(t *testing.T) {
	s, e := newService(t)
	writeRandom(t, filepath.Join(e.root, "a.mp3"))
	r, err := s.ScanNow(context.Background(), e.lib.ID)
	if err != nil || r.Added != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	st := s.Status()
	if len(st) != 1 || st[0].Running || st[0].Last.Added != 1 || st[0].FinishedAt == 0 {
		t.Fatalf("status %+v", st)
	}
}

func TestWatcherPicksUpNewFiles(t *testing.T) {
	s, e := newService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	waitFor(t, func() bool { st := s.Status(); return len(st) == 1 && st[0].FinishedAt > 0 })
	writeRandom(t, filepath.Join(e.root, "new-folder", "song.mp3")) // new dir must get watched too
	waitFor(t, func() bool { return trackCount(e) == 1 })
}

// blockingProber blocks in Probe until release is closed, and closes started
// the moment Probe is actually entered. That gives the test a deterministic
// handshake for "the scan is genuinely in flight, blocked on I/O" instead of
// a fixed sleep: a fixed sleep races Scan's own ctx.Err() checks (e.g. in its
// WalkDir callback), which can abort the walk before Probe is ever reached,
// so cancelling right after "Running" flips true does not reliably prove the
// scan was still doing real work when Run's shutdown fired.
type blockingProber struct {
	started chan struct{}
	release chan struct{}
}

func (p blockingProber) Probe(_ context.Context, _ string) (media.Info, error) {
	close(p.started)
	<-p.release
	return media.Info{DurationMS: 200_000, Codec: "mp3", BitrateKbps: 192}, nil
}

// Run must not return while a Trigger-spawned scan
// is still running (the caller may close the DB right after Run returns),
// and once Run has shut down, further Trigger calls must not start new
// scans (a debounce timer firing post-shutdown must not kick off an
// uncancellable walk).
func TestRunWaitsForInFlightTriggeredScanOnShutdown(t *testing.T) {
	s, e := newService(t)
	s.Debounce = time.Hour // isolate: only the explicit Trigger below should scan
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	waitFor(t, func() bool { st := s.Status(); return len(st) == 1 && st[0].FinishedAt > 0 })

	started := make(chan struct{})
	release := make(chan struct{})
	e.sc.Prober = blockingProber{started: started, release: release}
	writeRandom(t, filepath.Join(e.root, "a.mp3"))
	s.Trigger(e.lib.ID)

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("triggered scan never reached Probe")
	}

	cancel()
	select {
	case <-done:
		t.Fatal("Run returned while a background scan was still blocked in Probe")
	case <-time.After(150 * time.Millisecond):
	}

	close(release) // let the blocked scan finish
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the in-flight scan finished")
	}

	// Deterministic: once Run has returned, s.closed is guaranteed set, so a
	// post-shutdown Trigger returns synchronously without spawning anything.
	s.Trigger(e.lib.ID)
	if st := s.Status(); st[0].Running {
		t.Fatalf("Trigger after shutdown must not start a scan: %+v", st)
	}
}

// A dropped-and-remounted library root (simulating
// a USB disk pull) loses its fsnotify watches; nothing guarantees a fresh
// event under the remounted root to re-add them. The periodic tick must
// re-attach every current library's tree (in addition to rescanning) so a
// dropped root's replacement content is still found.
func TestPeriodicRescanReattachesDroppedRoot(t *testing.T) {
	s, e := newService(t)
	s.Interval = 200 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	waitFor(t, func() bool { st := s.Status(); return len(st) == 1 && st[0].FinishedAt > 0 })

	if err := os.RemoveAll(e.root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(e.root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRandom(t, filepath.Join(e.root, "reattached.mp3"))

	waitFor(t, func() bool { return trackCount(e) == 1 })
}

// The admin console reads scan results as snake_case keys, like the rest of
// the API.
func TestScanResultJSONKeys(t *testing.T) {
	b, err := json.Marshal(ScanResult{Added: 1, Updated: 2, Moved: 3, Missing: 4, Broken: 5, Unchanged: 6})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"added":1,"updated":2,"moved":3,"missing":4,"broken":5,"unchanged":6}`
	if string(b) != want {
		t.Fatalf("json = %s, want %s", b, want)
	}
}
