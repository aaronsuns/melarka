package stream

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/testutil"
)

type fakeRunner struct {
	calls   atomic.Int32
	release chan struct{}
	// started, if set, receives once per Run call right before it blocks on
	// release — lets a test know a job has actually taken the semaphore
	// slot and begun "transcoding" before it acts.
	started chan struct{}
}

func (f *fakeRunner) Run(_ context.Context, args []string) error {
	f.calls.Add(1)
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.release != nil {
		<-f.release
	}
	return os.WriteFile(args[len(args)-1], []byte("transcoded"), 0o644)
}

// Two phones ask for the same uncached tier at once.
func TestConcurrentGetsTranscodeOnce(t *testing.T) {
	r := &fakeRunner{release: make(chan struct{})}
	c := NewCache(t.TempDir(), 1<<30, 2, r)
	p := Plan{Codec: "aac", BitrateKbps: 256, Ext: ".m4a"}
	var wg sync.WaitGroup
	paths := make([]string, 2)
	for i := range paths {
		wg.Add(1)
		go func(i int) { defer wg.Done(); paths[i], _ = c.Get(context.Background(), "7-aac256-abcd", "src.wav", p) }(i)
	}
	time.Sleep(100 * time.Millisecond)
	close(r.release)
	wg.Wait()
	if r.calls.Load() != 1 {
		t.Fatalf("ffmpeg ran %d times", r.calls.Load())
	}
	if paths[0] == "" || paths[0] != paths[1] {
		t.Fatalf("paths %v", paths)
	}
	if _, err := c.Get(context.Background(), "7-aac256-abcd", "src.wav", p); err != nil || r.calls.Load() != 1 {
		t.Fatal("cache hit re-ran ffmpeg")
	}
}

// A job still queued for the semaphore
// (ffmpeg not started yet) must give up once every caller waiting on it has
// gone, instead of transcoding for nobody. Skipping through several
// lossless tracks in the car must not queue a full transcode per skip.
func TestAbandonedWaiterNeverTranscodes(t *testing.T) {
	r := &fakeRunner{started: make(chan struct{}, 2), release: make(chan struct{})}
	c := NewCache(t.TempDir(), 1<<30, 1, r) // maxConcurrent=1: only one slot to fight over
	p := Plan{Codec: "aac", BitrateKbps: 256, Ext: ".m4a"}

	// A takes the only slot and sits in "ffmpeg" (blocked on release).
	go c.Get(context.Background(), "A", "srcA.wav", p)
	<-r.started

	// B queues behind A for the same slot, under a context we cancel before
	// the slot ever frees.
	ctxB, cancelB := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := c.Get(ctxB, "B", "srcB.wav", p); err == nil {
			t.Error("expected an error for a waiter whose context was cancelled")
		}
	}()
	cancelB()
	<-done // B's Get call has returned: it was the only waiter for "B", so its
	// job (still queued for the semaphore, since A holds the only slot) has
	// been signalled to abandon.

	close(r.release) // free A's slot — a buggy implementation would let B's
	// queued job grab it now and run ffmpeg.

	// Give a (buggy) queued B job a chance to grab the freed slot and signal
	// started; a correct implementation never sends here again.
	select {
	case <-r.started:
		t.Fatal("a second ffmpeg run started: abandoned waiter was not skipped")
	case <-time.After(150 * time.Millisecond):
	}
	if r.calls.Load() != 1 {
		t.Fatalf("ffmpeg ran %d times, want 1 (A only, B must have been abandoned)", r.calls.Load())
	}
}

// A brand-new caller for a key must
// never be handed errAbandoned just because its first DoChan happened to
// join singleflight's in-flight call in the narrow window between that
// call deciding to give up (its last waiter left) and singleflight actually
// clearing the call out. Get must retry and get a real result instead of a
// spurious "transcode failed" — this exercises exactly that window using
// the onAbandon test hook to pin the old call there while B joins it.
func TestNewWaiterDuringAbandonUnwindGetsRealResult(t *testing.T) {
	r := &fakeRunner{started: make(chan struct{}, 4), release: make(chan struct{})}
	c := NewCache(t.TempDir(), 1<<30, 1, r) // maxConcurrent=1: only one slot to fight over
	p := Plan{Codec: "aac", BitrateKbps: 256, Ext: ".m4a"}

	reachedAbandon := make(chan struct{})
	resumeAbandon := make(chan struct{})
	// Set before any goroutine touches the cache, so reads of onAbandon from
	// other goroutines are happens-after this write (no data race) without
	// needing c.mu on every select in the hot path.
	c.onAbandon = func() {
		close(reachedAbandon)
		<-resumeAbandon
	}

	// "busy" occupies the only slot for the whole test (we never release it
	// until the end), so key "K"'s job can never grab the semaphore and
	// must pick the abandon branch instead.
	go c.Get(context.Background(), "busy", "srcBusy.wav", p)
	<-r.started

	// A is the sole waiter for "K"; its job queues behind "busy".
	ctxA, cancelA := context.WithCancel(context.Background())
	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		c.Get(ctxA, "K", "srcK.wav", p)
	}()
	cancelA() // A was K's only waiter: its job observes abandon and pauses in onAbandon above
	<-reachedAbandon
	<-doneA // A's own Get call already returned via its own ctx.Done()

	// B arrives now, while the abandoned call for "K" is still registered
	// in singleflight (its goroutine paused inside onAbandon) — B's first
	// DoChan is guaranteed to join that stale call, not start a fresh one.
	doneB := make(chan struct{})
	var pathB string
	var errB error
	go func() {
		defer close(doneB)
		pathB, errB = c.Get(context.Background(), "K", "srcK.wav", p)
	}()
	time.Sleep(50 * time.Millisecond) // headroom for B to reach DoChan and join the stale call
	close(resumeAbandon)              // let the stale call finish unwinding and deliver errAbandoned
	close(r.release)                  // free "busy"'s slot so B's retry can actually transcode
	<-doneB

	if errB != nil {
		t.Fatalf("B: unexpected error %v (must retry past a stale abandoned call)", errB)
	}
	if pathB == "" {
		t.Fatal("B: empty path")
	}
	if r.calls.Load() != 2 { // "busy" once, B's successful retry for "K" once
		t.Fatalf("ffmpeg ran %d times, want 2 (busy + K's retry)", r.calls.Load())
	}
}

func TestEvictOldestFirst(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir, 25, 1, &fakeRunner{})
	now := time.Now()
	for i, name := range []string{"old.m4a", "mid.m4a", "new.m4a"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, make([]byte, 10), 0o644)
		mt := now.Add(time.Duration(i-3) * time.Hour)
		os.Chtimes(p, mt, mt)
	}
	if err := c.Evict(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "old.m4a")); !os.IsNotExist(err) {
		t.Error("oldest not evicted")
	}
	for _, n := range []string{"mid.m4a", "new.m4a"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s evicted", n)
		}
	}
}

func TestRealFFmpegTranscode(t *testing.T) {
	dir := t.TempDir()
	src := sample(t, dir, "in.wav")
	c := NewCache(filepath.Join(dir, "cache"), 1<<30, 1, ExecRunner{FFmpeg: "ffmpeg"})
	out, err := c.Get(context.Background(), "1-aac256-x", src, Decide(Source{Codec: "pcm_s16le"}, High))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(out) != ".m4a" {
		t.Fatalf("out=%s", out)
	}
	if st, _ := os.Stat(out); st.Size() == 0 {
		t.Fatal("empty output")
	}
}

func sample(t *testing.T, dir, name string) string { return testutil.Sample(t, dir, name) }
