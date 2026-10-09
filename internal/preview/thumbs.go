package preview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aaronsuns/lark-server/internal/channels"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// DefaultThumbURL is where YouTube's thumbnails live (<base>/<id>/mqdefault.jpg).
const DefaultThumbURL = "https://i.ytimg.com/vi"

const (
	thumbMaxBytes  = 1 << 20
	thumbTimeout   = 5 * time.Second  // one upstream fetch at most
	thumbSlotWait  = 5 * time.Second  // waiting for a fetch slot at most
	thumbSlots     = 4                // upstream fetches at once
	thumbMissTTL   = 10 * time.Minute // a failed id is not asked again for this long
	thumbMaxMisses = 5000
	thumbMaxFiles  = 20000 // ~20 KB each: a few hundred MB at most
	thumbTmp       = ".tmp-"
)

// ErrBusy (always with ErrNotFound): no fetch slot came free in time. Not a
// miss: the id is asked for again on the next view, so it must not be cached.
var ErrBusy = errors.New("preview: thumbnail fetch slots busy")

// Thumbs proxies YouTube video thumbnails for clients that cannot reach
// i.ytimg.com (spec §18.2): fetched once per video id, kept on disk for
// MaxAge. Only <BaseURL>/<valid video id>/mqdefault.jpg is ever fetched —
// the host never comes from a request — redirects are not followed, and
// the body must be a JPEG of at most 1 MiB. At most 4 fetches run at once,
// and an id that failed is not asked again for 10 minutes.
type Thumbs struct {
	Dir     string            // <preview root>/thumbs
	BaseURL string            // "": DefaultThumbURL
	HTTP    channels.HTTPDoer // nil: a client with a 5 s timeout that follows no redirect
	MaxAge  time.Duration     // 0: 7 days
	Now     func() time.Time
	// SlotWait is how long a request waits for a fetch slot; 0: 5 s.
	SlotWait time.Duration

	// Test knobs (0: the defaults above).
	maxMisses int
	maxFiles  int

	once   sync.Once
	client channels.HTTPDoer
	slots  chan struct{}  // upstream fetches in progress
	bg     sync.WaitGroup // background refreshes of stale copies

	mu       sync.Mutex
	inflight map[string]chan struct{} // single flight per video id
	misses   map[string]time.Time     // id → when it may be asked again

	// files counts the thumbnails in Dir (-1: count on next use); a write at
	// the cap trims first (trimMu: one trim at a time).
	filesMu sync.Mutex
	files   int
	trimMu  sync.Mutex
}

func (t *Thumbs) init() {
	t.once.Do(func() {
		t.client = t.HTTP
		if t.client == nil {
			t.client = &http.Client{Timeout: thumbTimeout,
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		}
		t.slots = make(chan struct{}, thumbSlots)
		t.inflight = map[string]chan struct{}{}
		t.misses = map[string]time.Time{}
		t.files = -1
	})
}

func (t *Thumbs) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Thumbs) maxAge() time.Duration {
	if t.MaxAge > 0 {
		return t.MaxAge
	}
	return 7 * 24 * time.Hour
}

func orDefault[T int | time.Duration](v, def T) T {
	if v > 0 {
		return v
	}
	return def
}

// Path returns the local file holding videoID's thumbnail. ErrBadVideo for
// an invalid id; ErrNotFound when there is none: YouTube has none, it failed
// within the last 10 minutes, or no fetch slot came free within 5 s (the
// client falls back to its own tile). A copy older than MaxAge is returned
// at once and refreshed in the background.
func (t *Thumbs) Path(ctx context.Context, videoID string) (string, error) {
	if !ytdlp.IsVideoID(videoID) {
		return "", ErrBadVideo
	}
	t.init()
	path := filepath.Join(t.Dir, videoID+".jpg")
	fi, err := os.Stat(path)
	have := err == nil && fi.Mode().IsRegular()
	if have && t.now().Sub(fi.ModTime()) < t.maxAge() {
		return path, nil
	}
	t.mu.Lock()
	if until, ok := t.misses[videoID]; ok && t.now().Before(until) {
		t.mu.Unlock()
		if have {
			return path, nil
		}
		return "", ErrNotFound
	}
	wait, flying := t.inflight[videoID]
	if have { // stale: serve it now, refresh behind (once)
		if !flying {
			done := make(chan struct{})
			t.inflight[videoID] = done
			t.bg.Add(1)
			go func() {
				defer t.bg.Done()
				// A panic here would take the whole server down: the stale
				// copy keeps being served, and the id counts as a miss.
				defer func() {
					if r := recover(); r != nil {
						t.miss(videoID)
					}
				}()
				t.flight(context.Background(), videoID, path, done)
			}()
		}
		t.mu.Unlock()
		return path, nil
	}
	if flying {
		t.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		t.mu.Lock()
		_, missed := t.misses[videoID]
		t.mu.Unlock()
		if missed {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("%w: %w", ErrNotFound, ErrBusy) // the flight never got a slot
	}
	done := make(chan struct{})
	t.inflight[videoID] = done
	t.mu.Unlock()
	if err := t.flight(ctx, videoID, path, done); err != nil {
		return "", fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	return path, nil
}

// flight fetches videoID into path holding a slot, remembers a miss when
// YouTube had nothing usable, and ends the flight (done).
func (t *Thumbs) flight(ctx context.Context, videoID, path string, done chan struct{}) error {
	defer func() {
		t.mu.Lock()
		delete(t.inflight, videoID)
		t.mu.Unlock()
		close(done)
	}()
	timer := time.NewTimer(orDefault(t.SlotWait, thumbSlotWait))
	defer timer.Stop()
	select {
	case t.slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ErrBusy
	}
	defer func() { <-t.slots }() // also on a panic: a leaked slot is gone until restart
	// Once started, the fetch outlives a caller that gives up: others may be
	// waiting, and its result is kept. thumbTimeout and the slots bound it.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), thumbTimeout)
	err := t.fetch(fctx, videoID, path)
	cancel()
	if err != nil {
		t.miss(videoID)
	}
	return err
}

// miss remembers that videoID failed, keeping at most maxMisses ids.
func (t *Thumbs) miss(videoID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if limit := orDefault(t.maxMisses, thumbMaxMisses); len(t.misses) >= limit {
		t.pruneMissesLocked(now)
		for id := range t.misses { // still full: drop any
			if len(t.misses) < limit {
				break
			}
			delete(t.misses, id)
		}
	}
	t.misses[videoID] = now.Add(thumbMissTTL)
}

func (t *Thumbs) pruneMissesLocked(now time.Time) {
	for id, until := range t.misses {
		if !now.Before(until) {
			delete(t.misses, id)
		}
	}
}

func (t *Thumbs) fetch(ctx context.Context, videoID, path string) error {
	base := t.BaseURL
	if base == "" {
		base = DefaultThumbURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/"+videoID+"/mqdefault.jpg", nil)
	if err != nil {
		return err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, thumbMaxBytes+1))
	if err != nil {
		return err
	}
	if len(b) > thumbMaxBytes {
		return fmt.Errorf("over %d bytes", thumbMaxBytes)
	}
	if !bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}) {
		return fmt.Errorf("not a JPEG")
	}
	return t.store(path, b)
}

// store writes b to path atomically, making room first when Dir is at its
// file cap.
func (t *Thumbs) store(path string, b []byte) error {
	if err := os.MkdirAll(t.Dir, 0o755); err != nil {
		return err
	}
	_, statErr := os.Stat(path)
	isNew := statErr != nil
	if isNew && t.countFiles() >= orDefault(t.maxFiles, thumbMaxFiles) {
		t.trim()
	}
	f, err := os.CreateTemp(t.Dir, thumbTmp+"*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	now := t.now()
	if err == nil {
		err = os.Chtimes(tmp, now, now) // freshness follows t.Now
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if isNew {
		t.filesMu.Lock()
		if t.files >= 0 {
			t.files++
		}
		t.filesMu.Unlock()
	}
	return nil
}

// countFiles is the number of thumbnails in Dir, counted once then kept up
// to date by store (Sweep resets it).
func (t *Thumbs) countFiles() int {
	t.filesMu.Lock()
	defer t.filesMu.Unlock()
	if t.files < 0 {
		ents, _ := os.ReadDir(t.Dir)
		t.files = 0
		for _, e := range ents {
			if e.Type().IsRegular() && !strings.HasPrefix(e.Name(), thumbTmp) {
				t.files++
			}
		}
	}
	return t.files
}

// trim sweeps (down to 90% of the cap) when Dir is still at the cap; a
// second writer at the cap finds the room already made.
func (t *Thumbs) trim() {
	t.trimMu.Lock()
	defer t.trimMu.Unlock()
	if t.countFiles() >= orDefault(t.maxFiles, thumbMaxFiles) {
		t.Sweep()
	}
}

// Sweep deletes thumbnails older than MaxAge, temp files a crash left, and
// the oldest while there are more than 90% of the file cap; it forgets
// expired misses. It returns how many files it removed.
func (t *Thumbs) Sweep() int {
	t.init()
	t.mu.Lock()
	t.pruneMissesLocked(t.now())
	t.mu.Unlock()
	defer func() {
		t.filesMu.Lock()
		t.files = -1 // recounted on next use
		t.filesMu.Unlock()
	}()
	ents, err := os.ReadDir(t.Dir)
	if err != nil {
		return 0
	}
	now := t.now()
	type file struct {
		path string
		mod  time.Time
	}
	var keep []file
	n := 0
	for _, e := range ents {
		if !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(t.Dir, e.Name())
		age := now.Sub(fi.ModTime())
		switch {
		case strings.HasPrefix(e.Name(), thumbTmp):
			if age > time.Minute && os.Remove(p) == nil { // a fetch takes ≤ 5 s
				n++
			}
		case age >= t.maxAge():
			if os.Remove(p) == nil {
				n++
			}
		default:
			keep = append(keep, file{p, fi.ModTime()})
		}
	}
	if low := orDefault(t.maxFiles, thumbMaxFiles) * 9 / 10; len(keep) > low {
		sort.Slice(keep, func(i, j int) bool { return keep[i].mod.Before(keep[j].mod) })
		for _, f := range keep[:len(keep)-low] {
			if os.Remove(f.path) == nil {
				n++
			}
		}
	}
	return n
}

// wait blocks until the background refreshes are done (tests).
func (t *Thumbs) wait() { t.bg.Wait() }

func (t *Thumbs) missCount() int {
	t.init()
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.misses)
}

func (t *Thumbs) busySlots() int {
	t.init()
	return len(t.slots)
}
