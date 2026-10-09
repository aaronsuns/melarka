package stream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

func FFmpegArgs(src, dst string, p Plan) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", src,
		"-map", "0:a:0", "-vn", "-map_metadata", "-1"}
	switch p.Codec {
	case "aac":
		args = append(args, "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", p.BitrateKbps), "-movflags", "+faststart", "-f", "ipod")
	case "alac":
		args = append(args, "-c:a", "alac", "-movflags", "+faststart", "-f", "ipod")
	case "flac":
		args = append(args, "-c:a", "flac", "-f", "flac")
	}
	return append(args, dst)
}

type Runner interface {
	Run(ctx context.Context, args []string) error
}

type ExecRunner struct {
	FFmpeg string
	Nice   bool
}

func (r ExecRunner) Run(ctx context.Context, args []string) error {
	name := r.FFmpeg
	if r.Nice {
		args = append([]string{"-n", "10", r.FFmpeg}, args...)
		name = "nice"
	}
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

type Cache struct {
	Dir      string
	MaxBytes int64
	Runner   Runner
	sem      chan struct{}
	group    singleflight.Group

	mu      sync.Mutex
	waiting map[string]*waitState

	// onAbandon, if set, is invoked synchronously by a job's goroutine right
	// after it observes every waiter for its key has left, but before it
	// actually returns errAbandoned. It exists only so tests can pin down
	// the narrow window between that decision and singleflight clearing its
	// in-flight call for the key — during which a brand-new caller could
	// otherwise join (and be wrongly handed the result of) the stale call.
	// Zero-cost (nil, never called) outside tests.
	onAbandon func()
}

func NewCache(dir string, maxBytes int64, maxConcurrent int, r Runner) *Cache {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Cache{Dir: dir, MaxBytes: maxBytes, Runner: r, sem: make(chan struct{}, maxConcurrent)}
}

// waitState tracks how many callers are currently waiting on a given cache
// key so a job still queued for a semaphore slot (ffmpeg not yet started)
// can give up once nobody is left to receive it.
type waitState struct {
	n       int
	abandon chan struct{}
}

// errAbandoned is returned internally when every waiter for a key left
// before its job reached the front of the semaphore queue.
var errAbandoned = errors.New("stream: transcode abandoned, no waiters left")

// ErrBusy is TryGetWith's answer when starting now would not leave a
// semaphore slot free for playback.
var ErrBusy = errors.New("stream: no transcode slot to spare")

func (c *Cache) addWaiter(key string) *waitState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.waiting == nil {
		c.waiting = map[string]*waitState{}
	}
	ws, ok := c.waiting[key]
	if !ok {
		ws = &waitState{abandon: make(chan struct{})}
		c.waiting[key] = ws
	}
	ws.n++
	return ws
}

// removeWaiter deregisters a caller. Once the last waiter for key leaves,
// abandon is closed so a job still stuck waiting for the semaphore (ffmpeg
// has not started) gives up rather than transcoding for nobody.
func (c *Cache) removeWaiter(key string, ws *waitState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ws.n--
	if ws.n <= 0 {
		close(ws.abandon)
		if c.waiting[key] == ws {
			delete(c.waiting, key)
		}
	}
}

// maxAbandonRetries bounds how many times Get retries after joining a
// singleflight call that turns out to have already been abandoned (see
// errAbandoned) — a narrow race window between the call's last waiter
// leaving and singleflight clearing its in-flight entry, in which a
// brand-new caller can join the stale call instead of starting its own.
const maxAbandonRetries = 3

// Get returns the cached transcode for key, producing it once even when many
// requests ask at the same time. Once ffmpeg has actually started, the
// transcode is no longer tied to any caller's context: a phone that
// disconnects mid-transcode still leaves a usable file for its retry. But a
// job still queued behind a full semaphore is tied to demand — if every
// caller waiting on it disconnects before it gets a slot, it gives up
// instead of burning a slot (and CPU) transcoding for nobody, which matters
// when skipping through several tracks queues one job per skip.
func (c *Cache) Get(ctx context.Context, key, src string, p Plan) (string, error) {
	return c.GetWith(ctx, key, src, p, c.Runner)
}

// GetWith is Get, transcoding with r instead of the cache's own runner when
// this caller ends up starting the job (a background pre-transcode runs
// niced). Through the same semaphore and singleflight as playback.
func (c *Cache) GetWith(ctx context.Context, key, src string, p Plan, r Runner) (string, error) {
	return c.get(ctx, key, src, p, r, false)
}

// TryGetWith is GetWith for background work: it never waits in the
// semaphore queue (where it would sit ahead of playback requests) and only
// starts when, after it took a slot, one would still be free — with a
// single slot, only when that slot is idle. Otherwise ErrBusy.
func (c *Cache) TryGetWith(ctx context.Context, key, src string, p Plan, r Runner) (string, error) {
	return c.get(ctx, key, src, p, r, true)
}

func (c *Cache) get(ctx context.Context, key, src string, p Plan, r Runner, try bool) (string, error) {
	final := filepath.Join(c.Dir, key+p.Ext)
	if _, err := os.Stat(final); err == nil {
		now := time.Now()
		os.Chtimes(final, now, now) // LRU by mtime
		return final, nil
	}
	var path string
	var err error
	for attempt := 0; attempt <= maxAbandonRetries; attempt++ {
		path, err = c.getOnce(ctx, key, src, p, final, r, try)
		// errBusy for a caller that isn't trying: it joined a background
		// try that found no slot — go again, as a waiting caller.
		if !errors.Is(err, errAbandoned) && (try || !errors.Is(err, ErrBusy)) {
			return path, err
		}
		// We joined a call that had already decided to give up. That's not
		// our own abandonment — our ctx is still live — so retry: a fresh
		// addWaiter/DoChan either joins a new in-flight call another
		// retrying caller already started, or makes us the new leader.
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	return "", err
}

// getOnce makes a single attempt at producing or joining key's transcode.
// It can return errAbandoned if the singleflight call it joined was given
// up on by every one of its waiters (see removeWaiter) right as this caller
// registered.
func (c *Cache) getOnce(ctx context.Context, key, src string, p Plan, final string, r Runner, try bool) (string, error) {
	ws := c.addWaiter(key)
	defer c.removeWaiter(key, ws)
	ch := c.group.DoChan(key, func() (any, error) {
		if _, err := os.Stat(final); err == nil {
			return final, nil
		}
		if try {
			reserve := 1
			if cap(c.sem) < 2 {
				reserve = 0
			}
			if cap(c.sem)-len(c.sem) <= reserve {
				return nil, ErrBusy
			}
			select {
			case c.sem <- struct{}{}:
			default:
				return nil, ErrBusy
			}
		} else {
			select {
			case c.sem <- struct{}{}:
			case <-ws.abandon:
				if c.onAbandon != nil {
					c.onAbandon()
				}
				return nil, errAbandoned
			}
		}
		defer func() { <-c.sem }()
		if err := os.MkdirAll(c.Dir, 0o755); err != nil {
			return nil, err
		}
		suffix := make([]byte, 4)
		rand.Read(suffix)
		tmp := final + ".tmp-" + hex.EncodeToString(suffix)
		tctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := r.Run(tctx, FFmpegArgs("file:"+src, tmp, p)); err != nil {
			os.Remove(tmp)
			return nil, err
		}
		if err := os.Rename(tmp, final); err != nil {
			os.Remove(tmp)
			return nil, err
		}
		go c.Evict()
		return final, nil
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		if r.Err != nil {
			return "", r.Err
		}
		return r.Val.(string), nil
	}
}

// Evict deletes least-recently-used files until the cache fits MaxBytes.
// Temp files younger than an hour are in-flight transcodes and are left alone.
func (c *Cache) Evict() error {
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	type f struct {
		path  string
		size  int64
		mtime time.Time
	}
	var files []f
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		p := filepath.Join(c.Dir, e.Name())
		if strings.Contains(e.Name(), ".tmp-") {
			if time.Since(info.ModTime()) > time.Hour {
				os.Remove(p)
			}
			continue
		}
		files = append(files, f{p, info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.Before(files[j].mtime) })
	for _, x := range files {
		if total <= c.MaxBytes {
			break
		}
		if err := os.Remove(x.path); err == nil {
			total -= x.size
		}
	}
	return nil
}
