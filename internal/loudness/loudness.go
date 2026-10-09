// Package loudness measures each track's EBU R128 integrated loudness and
// true peak in the background, one niced ffmpeg at a time, so players can
// even out the volume between tracks.
package loudness

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Result is one measurement: integrated loudness in LUFS and true peak in
// dBTP. Digital silence measures -inf.
type Result struct{ LUFS, TruePeakDB float64 }

type Measurer interface {
	Measure(ctx context.Context, path string) (Result, error)
}

// ErrTransient marks a failure that says nothing about the track itself: the
// file could not be read (an unmounted or failing disk), ffmpeg could not be
// started, or the measurement timed out. Such failures are not recorded.
var ErrTransient = errors.New("loudness: transient failure")

// FFmpeg measures with ffmpeg's ebur128 filter, decoding the whole file.
type FFmpeg struct {
	Path string // the ffmpeg binary
	Nice bool   // run under nice -n 10
}

const (
	// stderrTail is how much of ffmpeg's stderr is kept: the Summary block
	// is always at the end, and a corrupt file can log an error per frame.
	stderrTail = 64 << 10
	// killWaitDelay bounds how long Wait waits for stderr to close after the
	// process group is killed.
	killWaitDelay = 5 * time.Second
)

func (f FFmpeg) Measure(ctx context.Context, path string) (Result, error) {
	if _, err := exec.LookPath(f.Path); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrTransient, err)
	}
	name := f.Path
	args := []string{"-nostdin", "-hide_banner", "-nostats", "-i", path,
		"-af", "ebur128=peak=true:framelog=quiet", "-f", "null", "-"}
	if f.Nice {
		if _, err := exec.LookPath("nice"); err != nil {
			return Result{}, fmt.Errorf("%w: %v", ErrTransient, err)
		}
		args = append([]string{"-n", "10", f.Path}, args...)
		name = "nice"
	}
	stderr := &tailBuffer{max: stderrTail}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = stderr // ebur128 logs its summary on stderr
	// Own process group, so a timeout kills everything it started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = killWaitDelay
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		out := stderr.String()
		var ee *exec.ExitError
		// nice exits 126/127 when it cannot run ffmpeg; a read error or a file
		// that vanished since the pick is the disk, not the track.
		if errors.As(err, &ee) && (ee.ExitCode() == 126 || ee.ExitCode() == 127) ||
			strings.Contains(out, "Input/output error") || strings.Contains(out, "No such file or directory") {
			return Result{}, fmt.Errorf("%w: ffmpeg: %v: %s", ErrTransient, err, tail(out, 300))
		}
		return Result{}, fmt.Errorf("ffmpeg: %w: %s", err, tail(out, 300))
	}
	return ParseEBUR128(stderr.String())
}

// tailBuffer is an io.Writer that keeps only the last max bytes written.
type tailBuffer struct {
	max int
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if len(b.buf) > 2*b.max {
		b.buf = append(b.buf[:0], b.buf[len(b.buf)-b.max:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	if len(b.buf) > b.max {
		return string(b.buf[len(b.buf)-b.max:])
	}
	return string(b.buf)
}

// tail keeps the last n bytes of s, where ffmpeg puts the reason it failed.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}

var (
	integratedRe = regexp.MustCompile(`(?m)^\s*I:\s*(\S+)\s+LUFS`)
	truePeakRe   = regexp.MustCompile(`(?m)^\s*Peak:\s*(\S+)\s+dBFS`)
)

// ParseEBUR128 reads the final Summary block of ebur128's log: "I: -14.2 LUFS"
// under "Integrated loudness:" and "Peak: -0.8 dBFS" under "True peak:".
func ParseEBUR128(stderr string) (Result, error) {
	i := strings.LastIndex(stderr, "Summary:")
	if i < 0 {
		return Result{}, errors.New("ebur128: no summary in ffmpeg output")
	}
	sum := stderr[i:]
	m := integratedRe.FindStringSubmatch(sum)
	if m == nil {
		return Result{}, errors.New("ebur128: no integrated loudness in summary")
	}
	lufs, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return Result{}, fmt.Errorf("ebur128: integrated loudness %q: %w", m[1], err)
	}
	_, peakPart, ok := strings.Cut(sum, "True peak:")
	if !ok {
		return Result{}, errors.New("ebur128: no true peak in summary")
	}
	m = truePeakRe.FindStringSubmatch(peakPart)
	if m == nil {
		return Result{}, errors.New("ebur128: no true peak in summary")
	}
	peak, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return Result{}, fmt.Errorf("ebur128: true peak %q: %w", m[1], err)
	}
	return Result{LUFS: lufs, TruePeakDB: peak}, nil
}

const (
	defaultIdle  = time.Minute
	defaultRetry = 720 * time.Hour
	// busyWait is how long the worker waits while the stream preparer works.
	busyWait = 5 * time.Second
	// transientRun transient failures in a row pause the worker for
	// transientPause: the disk or ffmpeg is gone, not one file.
	transientRun   = 3
	transientPause = 10 * time.Minute
	// transientSkip is how long a track that failed transiently is passed
	// over, so a stuck file does not block the ones behind it.
	transientSkip = time.Hour
	// timeoutsBeforeFailure timeouts of the same track (counted since the
	// server started) record it as failed, so a file that never finishes is
	// retried only after RetryFailedAfter instead of every transientSkip.
	timeoutsBeforeFailure = 3
	// Per-track timeout: twice the duration, at least a minute, at most 30.
	minTimeout = time.Minute
	maxTimeout = 30 * time.Minute
)

// timeoutFor bounds one measurement of a track of the given duration.
func timeoutFor(d time.Duration) time.Duration {
	return min(max(minTimeout, 2*d), maxTimeout)
}

// Worker measures one track at a time: never-measured tracks first, newest
// first, so a fresh scan or download is measured within minutes; a failed
// measurement is retried only after RetryFailedAfter. Every track that was
// measured, or failed to decode, gets loudness_checked_at. Transient
// failures (see ErrTransient) record nothing.
type Worker struct {
	DB      *sql.DB
	Measure Measurer
	Log     *slog.Logger

	Gap              time.Duration // pause after each track (0 = none)
	Idle             time.Duration // poll interval when nothing is pending (default 1m)
	RetryFailedAfter time.Duration // a failure is measured again after this (default 720h)

	// Busy reports that the stream preparer is working; the worker waits
	// until it is not (nil = never busy).
	Busy func() bool

	// Injectable for tests.
	Now     func() time.Time
	Sleep   func(ctx context.Context, d time.Duration) bool // false when ctx ended
	Timeout func(duration time.Duration) time.Duration      // per-track limit (default timeoutFor)

	mu       sync.Mutex
	skip     map[int64]time.Time // transiently failed track → when it may be picked again
	timeouts map[int64]int       // timeouts per track
}

// timedOut counts a timeout of track id and reports whether it has now
// timed out often enough to be recorded as failed.
func (w *Worker) timedOut(id int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timeouts == nil {
		w.timeouts = map[int64]int{}
	}
	w.timeouts[id]++
	if w.timeouts[id] < timeoutsBeforeFailure {
		return false
	}
	delete(w.timeouts, id)
	return true
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Worker) sleep(ctx context.Context, d time.Duration) bool {
	if w.Sleep != nil {
		return w.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (w *Worker) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

func (w *Worker) idle() time.Duration {
	if w.Idle > 0 {
		return w.Idle
	}
	return defaultIdle
}

func (w *Worker) retryAfter() time.Duration {
	if w.RetryFailedAfter > 0 {
		return w.RetryFailedAfter
	}
	return defaultRetry
}

func (w *Worker) timeout(d time.Duration) time.Duration {
	if w.Timeout != nil {
		return w.Timeout(d)
	}
	return timeoutFor(d)
}

// waitIdle waits while the stream preparer works; false when ctx ended.
func (w *Worker) waitIdle(ctx context.Context) bool {
	for w.Busy != nil && w.Busy() {
		if !w.sleep(ctx, busyWait) {
			return false
		}
	}
	return ctx.Err() == nil
}

// Run measures tracks until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	transient := 0
	for ctx.Err() == nil {
		if !w.waitIdle(ctx) {
			return
		}
		measured, err := w.Step(ctx)
		if ctx.Err() != nil {
			return
		}
		wait := w.Gap
		switch {
		case errors.Is(err, ErrTransient):
			w.log().Warn("loudness: not measured, will retry", "err", err)
			if transient++; transient >= transientRun {
				w.log().Warn("loudness: repeated transient failures, pausing", "for", transientPause)
				wait, transient = transientPause, 0
			}
		case err != nil:
			transient = 0
			w.log().Warn("loudness: pick or record a track", "err", err)
			wait = w.idle()
		case !measured:
			transient = 0
			wait = w.idle()
		default:
			transient = 0
		}
		if !w.sleep(ctx, wait) {
			return
		}
	}
}

// skipped lists the tracks passed over after a transient failure, dropping
// the ones whose time is up.
func (w *Worker) skipped() []any {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	var ids []any
	for id, until := range w.skip {
		if now.After(until) {
			delete(w.skip, id)
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func (w *Worker) skipFor(id int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.skip == nil {
		w.skip = map[int64]time.Time{}
	}
	w.skip[id] = w.now().Add(transientSkip)
}

// readable reports whether path can be opened and read; a missing file or
// a failing disk is not the track's fault.
func readable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Read(make([]byte, 4096)); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// Step picks the next track, measures it and records the result. measured
// is false when nothing is pending. A track that fails to decode is recorded
// (NULL loudness) and is not an error. A transient failure records nothing
// and returns an error wrapping ErrTransient; other errors are a database
// error or ctx ending.
func (w *Worker) Step(ctx context.Context) (measured bool, err error) {
	var (
		id          int64
		rel, root   string
		size, mtime int64
		durationMS  int64
	)
	args := []any{w.now().Add(-w.retryAfter()).Unix()}
	skip := ""
	if ids := w.skipped(); len(ids) > 0 {
		skip = " AND t.id NOT IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
		args = append(args, ids...)
	}
	// mtime=0: the scanner is still probing or indexing the row.
	err = w.DB.QueryRowContext(ctx, `SELECT t.id, t.rel_path, l.root, t.size, t.mtime, t.duration_ms
		FROM tracks t JOIN libraries l ON l.id=t.library_id
		WHERE t.status!='trashed' AND t.missing_since IS NULL AND t.broken=0 AND t.mtime!=0
		  AND (t.loudness_checked_at IS NULL OR (t.loudness_lufs IS NULL AND t.loudness_checked_at < ?))`+skip+`
		ORDER BY t.loudness_checked_at IS NOT NULL, t.added_at DESC LIMIT 1`,
		args...).Scan(&id, &rel, &root, &size, &mtime, &durationMS)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := readable(path); err != nil {
		w.skipFor(id)
		return false, fmt.Errorf("%w: track %d: %v", ErrTransient, id, err)
	}
	// Playback first: the preparer may have started since the pick.
	if !w.waitIdle(ctx) {
		return false, ctx.Err()
	}
	mctx, cancel := context.WithTimeout(ctx, w.timeout(time.Duration(durationMS)*time.Millisecond))
	r, merr := w.Measure.Measure(mctx, path)
	timedOut := mctx.Err() != nil
	cancel()
	if err := ctx.Err(); err != nil {
		return false, err // shutting down: not a failure of this track
	}
	switch {
	case timedOut && w.timedOut(id):
		merr = fmt.Errorf("timed out %d times: %w", timeoutsBeforeFailure, context.DeadlineExceeded)
	case timedOut:
		w.skipFor(id)
		return false, fmt.Errorf("%w: track %d: timed out: %v", ErrTransient, id, context.DeadlineExceeded)
	case errors.Is(merr, ErrTransient):
		w.skipFor(id)
		return false, fmt.Errorf("%w: track %d: %v", ErrTransient, id, merr)
	}
	var lufs, peak any // NULL unless measured
	switch {
	case merr != nil:
		w.log().Warn("loudness: measure failed", "track", id, "path", path, "err", merr)
	case isFinite(r.LUFS) && isFinite(r.TruePeakDB):
		lufs, peak = r.LUFS, r.TruePeakDB
	default:
		// Digital silence (-inf): no loudness, so no gain.
	}
	// size and mtime guard against a file replaced while it was measured: the
	// scan's update has then changed them, and the row stays unmeasured.
	_, err = w.DB.ExecContext(ctx, `UPDATE tracks SET loudness_lufs=?, true_peak_db=?, loudness_checked_at=?
		WHERE id=? AND size=? AND mtime=?`, lufs, peak, w.now().Unix(), id, size, mtime)
	if err != nil {
		return false, err
	}
	return true, nil
}

func isFinite(f float64) bool { return !math.IsInf(f, 0) && !math.IsNaN(f) }
