package stream

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/testutil"
)

// countingRunner records how many runs overlap and blocks each until released.
type countingRunner struct {
	calls    atomic.Int32
	inflight atomic.Int32
	max      atomic.Int32
	started  chan string
	release  chan struct{}
}

func (r *countingRunner) Run(_ context.Context, args []string) error {
	r.calls.Add(1)
	n := r.inflight.Add(1)
	for {
		m := r.max.Load()
		if n <= m || r.max.CompareAndSwap(m, n) {
			break
		}
	}
	defer r.inflight.Add(-1)
	if r.started != nil {
		r.started <- args[len(args)-1]
	}
	if r.release != nil {
		<-r.release
	}
	return writeFile(args[len(args)-1])
}

func writeFile(p string) error {
	return (&fakeRunner{}).Run(context.Background(), []string{p})
}

func prepSetup(t *testing.T) (*Service, *library.Store, int64, *fakeRunner) {
	t.Helper()
	d := testutil.DB(t)
	lib := &library.Store{DB: d}
	l, err := lib.EnsureLibrary(context.Background(), "main", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	cacheRunner := &fakeRunner{}
	svc := &Service{
		Library: lib,
		Cache:   NewCache(filepath.Join(t.TempDir(), "cache"), 1<<30, 2, cacheRunner),
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return svc, lib, l.ID, cacheRunner
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never became true")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A passthrough track (mp3 192k at "high") is skipped; a FLAC is transcoded
// with the preparer's own (niced) runner, into the shared cache, so the
// stream request that follows is a cache hit.
func TestPreparerTranscodesOnlyWhatNeedsIt(t *testing.T) {
	svc, lib, libID, cacheRunner := prepSetup(t)
	mp3 := insertTrack(t, lib, libID, "a.mp3", "mp3", 192)
	flac := insertTrack(t, lib, libID, "b.flac", "flac", 900)
	r := &countingRunner{}
	p := &Preparer{Service: svc, Runner: r, Max: 8, Log: svc.Log}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	if n := p.Enqueue([]int64{mp3, flac}, High); n != 2 {
		t.Fatalf("queued %d, want 2", n)
	}
	waitFor(t, func() bool { return r.calls.Load() == 1 && p.Idle() })
	time.Sleep(50 * time.Millisecond)
	if r.calls.Load() != 1 {
		t.Fatalf("preparer ran ffmpeg %d times, want 1 (the passthrough mp3 must be skipped)", r.calls.Load())
	}
	if cacheRunner.calls.Load() != 0 {
		t.Fatal("the preparer must use its own niced runner, not the playback one")
	}
	if _, _, err := svc.Resolve(context.Background(), flac, High); err != nil {
		t.Fatal(err)
	}
	if cacheRunner.calls.Load() != 0 || r.calls.Load() != 1 {
		t.Fatal("the stream request after a prepare must be a cache hit")
	}
}

// Jobs are deduplicated (within a request, against the pending list and the
// one running), the newest request goes first, and the list is bounded:
// what falls off the end is the stale part of an older request.
func TestPreparerDedupesAndDropsStale(t *testing.T) {
	svc, lib, libID, _ := prepSetup(t)
	ids := make([]int64, 6)
	for i := range ids {
		ids[i] = insertTrack(t, lib, libID, string(rune('a'+i))+".flac", "flac", 900)
	}
	r := &countingRunner{started: make(chan string, 10), release: make(chan struct{})}
	p := &Preparer{Service: svc, Runner: r, Max: 3, Log: svc.Log}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	p.Enqueue([]int64{ids[0]}, High)
	<-r.started // ids[0] is running
	p.Enqueue([]int64{ids[0], ids[1], ids[1], ids[2]}, High)
	if got := p.pendingIDs(); !equal(got, []int64{ids[1], ids[2]}) {
		t.Fatalf("pending %v, want %v (running and repeated ids dropped)", got, ids[1:3])
	}
	p.Enqueue([]int64{ids[3], ids[2], ids[4]}, High)
	if got := p.pendingIDs(); !equal(got, []int64{ids[3], ids[2], ids[4]}) {
		t.Fatalf("pending %v: newest request first, bounded to 3", got)
	}
	// A different quality is a different job.
	p.Enqueue([]int64{ids[3]}, Saver)
	if got := p.pending[0]; got.ID != ids[3] || got.Tier != Saver {
		t.Fatalf("front = %+v", got)
	}
	close(r.release)
	waitFor(t, p.Idle)
	if r.max.Load() != 1 {
		t.Fatalf("%d transcodes overlapped, want strictly one at a time", r.max.Load())
	}
}

func TestNewPreparerIsNiced(t *testing.T) {
	p := NewPreparer(&Service{}, "ffmpeg")
	er, ok := p.Runner.(ExecRunner)
	if !ok || !er.Nice || er.FFmpeg != "ffmpeg" {
		t.Fatalf("runner = %#v, want a niced ExecRunner", p.Runner)
	}
	if p.Max < 3 || p.Max > 16 {
		t.Fatalf("Max = %d", p.Max)
	}
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A pre-transcode never waits in the semaphore queue ahead of playback,
// and only starts when a slot stays free for playback. A job that couldn't
// start stays pending and is retried on the next Enqueue.
func TestPreparerLeavesASlotForPlayback(t *testing.T) {
	svc, lib, libID, _ := prepSetup(t) // cache: 2 slots
	play := &countingRunner{started: make(chan string, 1), release: make(chan struct{})}
	svc.Cache.Runner = play
	a := insertTrack(t, lib, libID, "a.flac", "flac", 900)
	b := insertTrack(t, lib, libID, "b.flac", "flac", 900)
	go svc.Resolve(context.Background(), a, High) // playback holds one of the 2 slots
	<-play.started

	r := &countingRunner{}
	p := &Preparer{Service: svc, Runner: r, Max: 8, Log: svc.Log}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	p.Enqueue([]int64{b}, High)
	time.Sleep(100 * time.Millisecond)
	if r.calls.Load() != 0 {
		t.Fatal("a prepare took the last free slot")
	}
	if got := p.pendingIDs(); !equal(got, []int64{b}) {
		t.Fatalf("pending %v, want the job kept for a retry", got)
	}
	close(play.release)
	// The next request retries it. Playback gives its slot back a moment
	// after its release, so keep asking until the retry finds the slot free.
	waitFor(t, func() bool {
		if r.calls.Load() == 0 {
			p.Enqueue(nil, High)
		}
		return r.calls.Load() == 1 && p.Idle()
	})
}

// A playback request that joins a prepare which gave up for lack of a slot
// still gets its transcode.
func TestPlaybackJoiningABusyPrepareStillTranscodes(t *testing.T) {
	c := NewCache(t.TempDir(), 1<<30, 1, &fakeRunner{})
	hold := &fakeRunner{started: make(chan struct{}, 1), release: make(chan struct{})}
	p := Plan{Codec: "aac", BitrateKbps: 256, Ext: ".m4a"}
	go c.GetWith(context.Background(), "X", "x.wav", p, hold)
	<-hold.started
	if _, err := c.TryGetWith(context.Background(), "Y", "y.wav", p, &fakeRunner{}); err != ErrBusy {
		t.Fatalf("try with no free slot: %v, want ErrBusy", err)
	}
	close(hold.release)
	if _, err := c.Get(context.Background(), "Y", "y.wav", p); err != nil {
		t.Fatal(err)
	}
}
