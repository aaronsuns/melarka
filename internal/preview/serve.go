package preview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aaronsuns/lark-server/internal/fileutil"
)

const pollGrowth = 100 * time.Millisecond

func contentType(media string) string {
	if media == "video" || media == MediaHD {
		return "video/mp4"
	}
	return "audio/mp4"
}

// Serve answers a stream request. A finished preview is served like any
// file (Range included). While it downloads with an announced size, a
// Range request gets 206 and the exact Content-Range as soon as its first
// byte exists, and the body follows the growing file; a request without
// Range gets 200 with the full length. Without an announced size the
// request waits for the end (no honest Content-Range exists before). It
// returns an error — having written nothing — when the preview is unknown
// (ErrNotFound), failed (ErrFailed, ErrVideoUnavailable), was pushed back
// by YouTube (ErrRetry), or no byte arrived within WaitCap (ErrNotReady).
// A download that fails mid-response ends the response. The size is
// announced seconds after the download started, often after the player's
// first request: that request waits for it (up to WaitCap). A preview kept
// within the last hour is served from the copy kept for its players.
func (s *Service) Serve(w http.ResponseWriter, r *http.Request, id int64) error {
	s.init()
	p, err := s.Get(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		s.pbMu.Lock()
		k, ok := s.kept[id]
		s.pbMu.Unlock()
		if ok {
			return serveFile(w, r, k.path, k.media)
		}
	}
	if err != nil {
		return err
	}
	s.touch(r.Context(), id)
	defer s.beginServe(id)() // watched: never released while this response is in flight
	if p.Status == "failed" {
		return p.failure()
	}
	s.mu.Lock()
	lv := s.live[id]
	s.mu.Unlock()
	if p.Status == "done" || lv == nil {
		return s.serveDone(w, r, id)
	}
	if p.Media == MediaHD {
		return ErrNotReady // merged at the end: nothing servable before it is complete
	}
	path, total := lv.snapshot()
	deadline := time.Now().Add(s.waitCap())
	for total <= 0 {
		select {
		case <-lv.done:
			if err := lv.result(); err != nil {
				return err
			}
			return s.serveDone(w, r, id)
		case <-r.Context().Done():
			return r.Context().Err()
		case <-time.After(pollGrowth):
			if path, total = lv.snapshot(); total <= 0 && time.Now().After(deadline) {
				return ErrNotReady
			}
		}
	}
	start, end, partial, ok := parseRange(r.Header.Get("Range"), total)
	if !ok {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", total))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return nil
	}
	switch waitFor(r.Context(), lv, path, start+1, s.waitCap()) {
	case grown:
	case ended:
		if err := lv.result(); err != nil {
			return err
		}
		return s.serveDone(w, r, id)
	case gone:
		return r.Context().Err()
	case timedOut:
		return ErrNotReady
	}
	f, err := os.Open(path)
	if err != nil {
		return ErrNotReady
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", contentType(p.Media))
	h.Set("Accept-Ranges", "bytes")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	if partial {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	copyGrowing(r.Context(), w, f, lv, start, end, s.waitCap())
	return nil
}

func (s *Service) serveDone(w http.ResponseWriter, r *http.Request, id int64) error {
	p, err := s.Get(r.Context(), id)
	if err != nil {
		return err
	}
	switch p.Status {
	case "failed":
		return p.failure()
	case "downloading":
		return ErrNotReady // a row a previous process left; the next start removes it
	}
	full := filepath.Join(s.Root, filepath.FromSlash(p.path))
	if _, ok := fileutil.RelInside(s.Root, full); !ok {
		return ErrNotFound
	}
	return serveFile(w, r, full, p.Media)
}

// serveFile serves a finished file (Range included).
func serveFile(w http.ResponseWriter, r *http.Request, full, media string) error {
	f, err := os.Open(full)
	if err != nil {
		return ErrNotFound
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", contentType(media))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, filepath.Base(full), st.ModTime(), f)
	return nil
}

// parseRange reads a single "bytes=a-b" / "bytes=a-" / "bytes=-n" range
// (the first one, if several) against total. No header: the whole file, not partial.
func parseRange(h string, total int64) (start, end int64, partial, ok bool) {
	if h == "" {
		return 0, total - 1, false, true
	}
	spec, found := strings.CutPrefix(h, "bytes=")
	if !found {
		return 0, 0, false, false
	}
	spec, _, _ = strings.Cut(spec, ",")
	a, b, found := strings.Cut(strings.TrimSpace(spec), "-")
	if !found {
		return 0, 0, false, false
	}
	switch {
	case a == "":
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false, false
		}
		start, end = max(total-n, 0), total-1
	default:
		var err error
		if start, err = strconv.ParseInt(a, 10, 64); err != nil || start < 0 {
			return 0, 0, false, false
		}
		end = total - 1
		if b != "" {
			if end, err = strconv.ParseInt(b, 10, 64); err != nil || end < start {
				return 0, 0, false, false
			}
			end = min(end, total-1)
		}
	}
	if start >= total {
		return 0, 0, false, false
	}
	return start, end, true, true
}

type waitResult int

const (
	grown waitResult = iota
	ended
	gone
	timedOut
)

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

// waitFor waits until the growing file holds at least n bytes.
func waitFor(ctx context.Context, lv *live, path string, n int64, cap time.Duration) waitResult {
	deadline := time.Now().Add(cap)
	for {
		if fileSize(path) >= n {
			return grown
		}
		select {
		case <-lv.done:
			if lv.result() == nil && fileSize(path) >= n {
				return grown
			}
			return ended
		case <-ctx.Done():
			return gone
		case <-time.After(pollGrowth):
			if time.Now().After(deadline) {
				return timedOut
			}
		}
	}
}

// copyGrowing writes bytes [start, end] of a file that is still being
// written, waiting for growth; it stops when the client leaves, the
// download fails, or nothing new arrived for idleCap.
func copyGrowing(ctx context.Context, w http.ResponseWriter, f *os.File, lv *live, start, end int64, idleCap time.Duration) {
	buf := make([]byte, 64<<10)
	fl, _ := w.(http.Flusher)
	pos, idle := start, time.Now()
	for pos <= end {
		n, err := f.ReadAt(buf[:min(int64(len(buf)), end-pos+1)], pos)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
			pos += int64(n)
			idle = time.Now()
			continue
		}
		if err != nil && err != io.EOF {
			return
		}
		select {
		case <-lv.done:
			if lv.result() != nil {
				return
			}
			if st, err := f.Stat(); err != nil || st.Size() <= pos {
				return // finished shorter than announced: end here
			}
		case <-ctx.Done():
			return
		case <-time.After(pollGrowth):
			if time.Since(idle) > idleCap {
				return
			}
		}
	}
}
