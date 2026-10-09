// Package loudness measures each track's EBU R128 integrated loudness and
// true peak in the background, one niced ffmpeg at a time, so players can
// even out the volume between tracks.
package loudness

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Result is one measurement: integrated loudness in LUFS and true peak in
// dBTP. Digital silence measures -inf.
type Result struct{ LUFS, TruePeakDB float64 }

type Measurer interface {
	Measure(ctx context.Context, path string) (Result, error)
}

// FFmpeg measures with ffmpeg's ebur128 filter, decoding the whole file.
type FFmpeg struct {
	Path string // the ffmpeg binary
	Nice bool   // run under nice -n 10
}

func (f FFmpeg) Measure(ctx context.Context, path string) (Result, error) {
	name := f.Path
	args := []string{"-nostdin", "-hide_banner", "-nostats", "-i", path,
		"-af", "ebur128=peak=true:framelog=quiet", "-f", "null", "-"}
	if f.Nice {
		args = append([]string{"-n", "10", f.Path}, args...)
		name = "nice"
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = &stderr // ebur128 logs its summary on stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, fmt.Errorf("ffmpeg: %w: %s", err, tail(stderr.String(), 300))
	}
	return ParseEBUR128(stderr.String())
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
)

// Worker measures one track at a time: never-measured tracks first, newest
// first, so a fresh scan or download is measured within minutes; a failed
// measurement is retried only after RetryFailedAfter. Every track that was
// measured, or failed, gets loudness_checked_at.
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
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) bool // false when ctx ended
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

// Run measures tracks until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		for w.Busy != nil && w.Busy() {
			if !w.sleep(ctx, busyWait) {
				return
			}
		}
		measured, err := w.Step(ctx)
		if ctx.Err() != nil {
			return
		}
		wait := w.Gap
		if err != nil {
			w.log().Warn("loudness: pick or record a track", "err", err)
			wait = w.idle()
		} else if !measured {
			wait = w.idle()
		}
		if !w.sleep(ctx, wait) {
			return
		}
	}
}

// Step picks the next track, measures it and records the result. measured
// is false when nothing is pending. A failed measurement is recorded (NULL
// loudness) and is not an error; err is a database error or ctx ending.
func (w *Worker) Step(ctx context.Context) (measured bool, err error) {
	var (
		id          int64
		rel, root   string
		size, mtime int64
	)
	err = w.DB.QueryRowContext(ctx, `SELECT t.id, t.rel_path, l.root, t.size, t.mtime FROM tracks t JOIN libraries l ON l.id=t.library_id
		WHERE t.status!='trashed' AND t.missing_since IS NULL AND t.broken=0
		  AND (t.loudness_checked_at IS NULL OR (t.loudness_lufs IS NULL AND t.loudness_checked_at < ?))
		ORDER BY t.loudness_checked_at IS NOT NULL, t.added_at DESC LIMIT 1`,
		w.now().Add(-w.retryAfter()).Unix()).Scan(&id, &rel, &root, &size, &mtime)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	r, merr := w.Measure.Measure(ctx, path)
	if err := ctx.Err(); err != nil {
		return false, err // shutting down: not a failure of this track
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
