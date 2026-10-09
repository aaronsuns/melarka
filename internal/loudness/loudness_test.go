package loudness

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/testutil"
)

// sampleSummary is the tail of what ffmpeg -af ebur128=peak=true:framelog=quiet
// writes to stderr.
const sampleSummary = `Output #0, null, to 'pipe:':
  Stream #0:0: Audio: pcm_s16le, 44100 Hz, stereo, s16, 1411 kb/s
[Parsed_ebur128_0 @ 0x600000c0c000] Summary:

  Integrated loudness:
    I:         -16.3 LUFS
    Threshold: -26.5 LUFS

  Loudness range:
    LRA:         6.1 LU
    Threshold: -36.4 LUFS
    LRA low:   -20.9 LUFS
    LRA high:  -14.8 LUFS

  True peak:
    Peak:       -1.4 dBFS
[out#0/null @ 0x600000c08000] video:0KiB audio:40370KiB subtitle:0KiB other streams:0KiB global headers:0KiB muxing overhead: unknown
size=N/A time=00:03:54.40 bitrate=N/A speed= 412x
`

func TestParseEBUR128(t *testing.T) {
	r, err := ParseEBUR128(sampleSummary)
	if err != nil || r.LUFS != -16.3 || r.TruePeakDB != -1.4 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := ParseEBUR128("no summary"); err == nil {
		t.Fatal("want error")
	}
	// Only the final Summary block counts.
	twice := strings.Replace(sampleSummary, "-16.3 LUFS", "-9.0 LUFS", 1) + sampleSummary
	if r, err := ParseEBUR128(twice); err != nil || r.LUFS != -16.3 {
		t.Fatalf("last summary: %+v %v", r, err)
	}
	// A summary without a true peak (peak=true missing) is an error, not a 0 dBTP peak.
	noPeak, _, _ := strings.Cut(sampleSummary, "  True peak:")
	if _, err := ParseEBUR128(noPeak); err == nil {
		t.Fatal("want error without a true peak")
	}
	// Digital silence: ffmpeg prints -inf.
	silent := strings.Replace(sampleSummary, "-1.4 dBFS", "-inf dBFS", 1)
	if r, err := ParseEBUR128(silent); err != nil || !math.IsInf(r.TruePeakDB, -1) {
		t.Fatalf("silence: %+v %v", r, err)
	}
}

func TestMeasureKnownTone(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	quiet := testutil.Sample(t, dir, "quiet.flac", "-af", "volume=-12dB")
	loud := testutil.Sample(t, dir, "loud.flac", "-af", "volume=-6dB")
	a, err := FFmpeg{Path: "ffmpeg"}.Measure(ctx, quiet)
	if err != nil {
		t.Fatal(err)
	}
	b, err := FFmpeg{Path: "ffmpeg", Nice: true}.Measure(ctx, loud)
	if err != nil {
		t.Fatal(err)
	}
	if d := b.LUFS - a.LUFS; math.Abs(d-6) > 0.5 {
		t.Fatalf("6 dB apart, got %.2f", d)
	}
	if a.LUFS > -25 || a.LUFS < -40 {
		t.Fatalf("quiet tone LUFS %.1f out of range", a.LUFS)
	}
	if b.TruePeakDB > -10 {
		t.Fatalf("peak %.1f", b.TruePeakDB)
	}
	if _, err := (FFmpeg{Path: "ffmpeg"}).Measure(ctx, filepath.Join(dir, "absent.flac")); err == nil {
		t.Fatal("want an error for a missing file")
	}
}

// fakeMeasurer answers per base name: fail → error, silent → -inf, else a fixed result.
type fakeMeasurer struct {
	fail, silent string
	mu           sync.Mutex
	paths        []string
	inFlight     atomic.Int32
	maxInFlight  atomic.Int32
	block        func(ctx context.Context) // optional, runs inside Measure
}

func (f *fakeMeasurer) Measure(ctx context.Context, path string) (Result, error) {
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		m := f.maxInFlight.Load()
		if n <= m || f.maxInFlight.CompareAndSwap(m, n) {
			break
		}
	}
	f.mu.Lock()
	f.paths = append(f.paths, path)
	f.mu.Unlock()
	if f.block != nil {
		f.block(ctx)
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
	}
	switch filepath.Base(path) {
	case f.fail:
		return Result{}, errors.New("decode error")
	case f.silent:
		return Result{LUFS: -70, TruePeakDB: math.Inf(-1)}, nil
	}
	return Result{LUFS: -11.5, TruePeakDB: -0.3}, nil
}

func (f *fakeMeasurer) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

type wenv struct {
	db   *sql.DB
	root string
	now  time.Time
}

func newWEnv(t *testing.T) *wenv {
	e := &wenv{db: testutil.DB(t), root: "/lib", now: time.Unix(1_800_000_000, 0)}
	if _, err := e.db.Exec(`INSERT INTO libraries(id,name,root) VALUES (1,'m',?)`, e.root); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *wenv) add(t *testing.T, rel string, addedAt int64, extra string) int64 {
	t.Helper()
	res, err := e.db.Exec(`INSERT INTO tracks(library_id,rel_path,size,mtime,fingerprint,added_at) VALUES (1,?,1,1,?,?)`, rel, rel, addedAt)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if extra != "" {
		if _, err := e.db.Exec(`UPDATE tracks SET `+extra+` WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func (e *wenv) worker(m Measurer) *Worker {
	return &Worker{DB: e.db, Measure: m, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		RetryFailedAfter: 720 * time.Hour, Now: func() time.Time { return e.now }}
}

type row struct {
	lufs, peak sql.NullFloat64
	checked    sql.NullInt64
}

func (e *wenv) row(t *testing.T, id int64) row {
	t.Helper()
	var r row
	if err := e.db.QueryRow(`SELECT loudness_lufs, true_peak_db, loudness_checked_at FROM tracks WHERE id=?`, id).Scan(&r.lufs, &r.peak, &r.checked); err != nil {
		t.Fatal(err)
	}
	return r
}

func step(t *testing.T, w *Worker) bool {
	t.Helper()
	measured, err := w.Step(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return measured
}

func TestWorkerRecordsNewestFirstAndFailures(t *testing.T) {
	e := newWEnv(t)
	old := e.add(t, "old.flac", 100, "")
	newest := e.add(t, "a/newest.flac", 300, "")
	bad := e.add(t, "bad.flac", 200, "")
	e.add(t, "trashed.flac", 400, "status='trashed'")
	e.add(t, "missing.flac", 400, "missing_since=1")
	e.add(t, "broken.flac", 400, "broken=1")
	done := e.add(t, "done.flac", 500, "loudness_lufs=-8, true_peak_db=0.1, loudness_checked_at=1")
	m := &fakeMeasurer{fail: "bad.flac"}
	w := e.worker(m)
	for i := range 3 {
		if !step(t, w) {
			t.Fatalf("step %d measured nothing", i)
		}
	}
	if step(t, w) {
		t.Fatal("nothing should be left")
	}
	want := []string{"/lib/a/newest.flac", "/lib/bad.flac", "/lib/old.flac"}
	if got := m.seen(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order %v, want %v", got, want)
	}
	for _, id := range []int64{old, newest} {
		r := e.row(t, id)
		if r.lufs.Float64 != -11.5 || r.peak.Float64 != -0.3 || r.checked.Int64 != e.now.Unix() {
			t.Fatalf("track %d: %+v", id, r)
		}
	}
	if r := e.row(t, bad); r.lufs.Valid || r.peak.Valid || r.checked.Int64 != e.now.Unix() {
		t.Fatalf("failed track: %+v", r)
	}
	if r := e.row(t, done); r.lufs.Float64 != -8 || r.checked.Int64 != 1 {
		t.Fatalf("an already measured track was touched: %+v", r)
	}
}

func TestWorkerFailureNotRetried(t *testing.T) {
	e := newWEnv(t)
	bad := e.add(t, "bad.flac", 100, "")
	m := &fakeMeasurer{fail: "bad.flac"}
	w := e.worker(m)
	if !step(t, w) {
		t.Fatal("first step measured nothing")
	}
	if r := e.row(t, bad); r.lufs.Valid || r.checked.Int64 != e.now.Unix() {
		t.Fatalf("after failure: %+v", r)
	}
	if step(t, w) {
		t.Fatal("a failure must not be retried at once")
	}
	e.now = e.now.Add(720 * time.Hour)
	if step(t, w) {
		t.Fatal("not before RetryFailedAfter has passed")
	}
	e.now = e.now.Add(time.Second)
	if !step(t, w) {
		t.Fatal("retried after RetryFailedAfter")
	}
	if n := len(m.seen()); n != 2 {
		t.Fatalf("measured %d times, want 2", n)
	}
}

// Digital silence has no loudness: NULL, so it gets no gain.
func TestWorkerSilenceStoredAsNull(t *testing.T) {
	e := newWEnv(t)
	id := e.add(t, "silence.flac", 100, "")
	w := e.worker(&fakeMeasurer{silent: "silence.flac"})
	step(t, w)
	if r := e.row(t, id); r.lufs.Valid || r.peak.Valid || !r.checked.Valid {
		t.Fatalf("%+v", r)
	}
}

// Shutting down mid-measurement must not record the track as failed.
func TestWorkerCancelledMeasureNotRecorded(t *testing.T) {
	e := newWEnv(t)
	id := e.add(t, "a.flac", 100, "")
	ctx, cancel := context.WithCancel(context.Background())
	m := &fakeMeasurer{block: func(context.Context) { cancel() }}
	w := e.worker(m)
	if _, err := w.Step(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want context.Canceled", err)
	}
	if r := e.row(t, id); r.checked.Valid {
		t.Fatalf("recorded on cancel: %+v", r)
	}
}

// A file replaced while it was being measured keeps NULL loudness, so the
// new file is measured on the next pick instead of carrying the old result.
func TestWorkerFileChangedDuringMeasure(t *testing.T) {
	e := newWEnv(t)
	id := e.add(t, "a.flac", 100, "")
	m := &fakeMeasurer{block: func(context.Context) {
		e.db.Exec(`UPDATE tracks SET size=2, mtime=2 WHERE id=?`, id)
	}}
	w := e.worker(m)
	step(t, w)
	if r := e.row(t, id); r.checked.Valid {
		t.Fatalf("stale result recorded: %+v", r)
	}
	m.block = nil
	step(t, w)
	if r := e.row(t, id); r.lufs.Float64 != -11.5 {
		t.Fatalf("not measured again: %+v", r)
	}
}

func TestWorkerBudget(t *testing.T) {
	e := newWEnv(t)
	e.add(t, "a.flac", 100, "")
	e.add(t, "b.flac", 200, "")
	m := &fakeMeasurer{}
	w := e.worker(m)
	w.Gap = 2 * time.Second // Idle left 0: defaults to 60s
	busy := 2
	w.Busy = func() bool {
		if busy > 0 {
			busy--
			return true
		}
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var slept []time.Duration
	w.Sleep = func(ctx context.Context, d time.Duration) bool {
		slept = append(slept, d)
		if d == time.Minute { // idle: nothing left, stop the run
			cancel()
			return false
		}
		return ctx.Err() == nil
	}
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
	want := []time.Duration{5 * time.Second, 5 * time.Second, 2 * time.Second, 2 * time.Second, time.Minute}
	if len(slept) != len(want) {
		t.Fatalf("slept %v, want %v", slept, want)
	}
	for i := range want {
		if slept[i] != want[i] {
			t.Fatalf("slept %v, want %v", slept, want)
		}
	}
	if n := len(m.seen()); n != 2 {
		t.Fatalf("measured %d", n)
	}
	if m.maxInFlight.Load() != 1 {
		t.Fatalf("%d measurements in flight at once", m.maxInFlight.Load())
	}
}

func TestRunReturnsOnCancel(t *testing.T) {
	e := newWEnv(t)
	w := e.worker(&fakeMeasurer{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }() // real Sleep
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run ignored a cancelled context")
	}
}
