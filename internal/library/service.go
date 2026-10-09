package library

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type ScanStatus struct {
	LibraryID  int64      `json:"library_id"`
	Running    bool       `json:"running"`
	Last       ScanResult `json:"last"`
	LastError  string     `json:"last_error"`
	FinishedAt int64      `json:"finished_at"`
}

type Service struct {
	Store    *Store
	Scanner  *Scanner
	Log      *slog.Logger
	Debounce time.Duration // default 10s
	Interval time.Duration // default 1h

	mu      sync.Mutex
	locks   map[int64]*sync.Mutex
	status  map[int64]*ScanStatus
	pending map[int64]bool
	timers  map[int64]*time.Timer
	ctx     context.Context // base ctx for background scans; set by Run, falls back to context.Background()
	closed  bool            // true once Run's ctx has been cancelled; Trigger/debounced become no-ops
	wg      sync.WaitGroup  // tracks in-flight Trigger goroutines so Run can wait for them on shutdown
}

// bgCtx returns the ctx background (Trigger-spawned) scans should run with:
// Run's own ctx once Run has started, or context.Background() before that so
// ScanNow/Trigger still work from tests that never call Run.
func (s *Service) bgCtx() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func (s *Service) init() {
	if s.locks == nil {
		s.locks = map[int64]*sync.Mutex{}
		s.status = map[int64]*ScanStatus{}
		s.pending = map[int64]bool{}
		s.timers = map[int64]*time.Timer{}
	}
}

func (s *Service) lockFor(id int64) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	if s.locks[id] == nil {
		s.locks[id] = &sync.Mutex{}
		s.status[id] = &ScanStatus{LibraryID: id}
	}
	return s.locks[id]
}

// ScanNow runs a scan for libID, blocking until it finishes. Only one scan
// per library runs at a time; concurrent callers for the same library queue
// up on the library's lock.
func (s *Service) ScanNow(ctx context.Context, libID int64) (ScanResult, error) {
	l := s.lockFor(libID)
	l.Lock()
	defer l.Unlock()
	return s.scanLocked(ctx, libID)
}

// scanLocked performs the scan and records status. The caller must already
// hold the per-library lock returned by lockFor(libID).
func (s *Service) scanLocked(ctx context.Context, libID int64) (ScanResult, error) {
	lib, err := s.Store.Library(ctx, libID)
	if err != nil {
		return ScanResult{}, err
	}
	s.setStatus(libID, func(st *ScanStatus) { st.Running = true })
	start := time.Now()
	res, err := s.Scanner.Scan(ctx, lib)
	s.setStatus(libID, func(st *ScanStatus) {
		st.Running = false
		st.Last = res
		st.LastError = ""
		if err != nil {
			st.LastError = err.Error()
		}
		st.FinishedAt = time.Now().Unix()
	})
	s.Log.Info("scan finished", "library", lib.Name, "took", time.Since(start).Round(time.Millisecond),
		"added", res.Added, "updated", res.Updated, "moved", res.Moved, "missing", res.Missing, "broken", res.Broken, "err", err)
	return res, err
}

func (s *Service) setStatus(id int64, f func(*ScanStatus)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s.status[id])
}

// Trigger schedules a scan in the background. Calls made while a scan for the
// same library is queued collapse into one. After Run's ctx has been
// cancelled, Trigger is a no-op: no new background scan is started.
//
// The goroutine below holds the library's lock for the whole queued-scan ->
// pending-cleared -> scan sequence (instead of releasing it after clearing
// pending and letting ScanNow re-acquire it moments later). That avoids a
// window where a fresh Trigger, seeing pending already cleared but the scan
// not yet started, would queue a redundant extra scan.
//
// wg.Add happens inside the same s.mu critical section that later checks
// s.closed during shutdown (see Run), so a Trigger that raced with shutdown
// either lands entirely before the shutdown's Wait() (and is waited for) or
// entirely after closed is set (and never spawns a goroutine at all) — no
// wg.Add can race a concurrent wg.Wait.
func (s *Service) Trigger(libID int64) {
	s.lockFor(libID)
	s.mu.Lock()
	if s.closed || s.pending[libID] {
		s.mu.Unlock()
		return
	}
	s.pending[libID] = true
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		l := s.lockFor(libID)
		l.Lock() // wait for a running scan to finish first
		defer l.Unlock()
		s.mu.Lock()
		s.pending[libID] = false
		s.mu.Unlock()
		s.scanLocked(s.bgCtx(), libID)
	}()
}

func (s *Service) TriggerAll(ctx context.Context) {
	libs, err := s.Store.Libraries(ctx)
	if err != nil {
		s.Log.Error("list libraries", "err", err)
		return
	}
	for _, l := range libs {
		s.Trigger(l.ID)
	}
}

func (s *Service) Status() []ScanStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	out := []ScanStatus{}
	for _, st := range s.status {
		out = append(out, *st)
	}
	return out
}

func (s *Service) debounced(libID int64) {
	d := s.Debounce
	if d == 0 {
		d = 10 * time.Second
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	if s.closed {
		return
	}
	if t := s.timers[libID]; t != nil {
		t.Stop()
	}
	s.timers[libID] = time.AfterFunc(d, func() { s.Trigger(libID) })
}

func (s *Service) Run(ctx context.Context) error {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()

	libs, err := s.Store.Libraries(ctx)
	if err != nil {
		return err
	}
	for _, l := range libs {
		s.ScanNow(ctx, l.ID)
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	for _, l := range libs {
		addTree(w, l.Root, s.Log)
	}
	interval := s.Interval
	if interval == 0 {
		interval = time.Hour
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			// Stop pending debounce timers and let any scan already in
			// flight (running or queued via Trigger) finish before we
			// return: our caller may close the DB right after Run returns,
			// and a background scan still using it would otherwise hit
			// "sql: database is closed" (or, for a not-yet-started debounce
			// timer, fire an uncancellable walk after shutdown).
			s.mu.Lock()
			s.closed = true
			for _, t := range s.timers {
				t.Stop()
			}
			s.timers = map[int64]*time.Timer{}
			s.mu.Unlock()
			s.wg.Wait()
			return nil
		case <-tick.C:
			// Re-attach every current library's tree before rescanning: an
			// inotify/kqueue watch is lost when its directory disappears
			// (e.g. a USB disk drops and remounts) and a fresh remount fires
			// no event in an already-watched parent to tell us to re-add it.
			// A library added at runtime has the same gap: its root was
			// never in Run's startup addTree loop. Re-adding an
			// already-watched path is harmless.
			current, _ := s.Store.Libraries(ctx)
			for _, l := range current {
				addTree(w, l.Root, s.Log)
			}
			s.TriggerAll(ctx)
		case ev := <-w.Events:
			if ev.Op&fsnotify.Create != 0 {
				if st, err := os.Stat(ev.Name); err == nil && st.IsDir() && !strings.HasPrefix(filepath.Base(ev.Name), ".") {
					addTree(w, ev.Name, s.Log)
				}
			}
			// Libraries may be added at runtime, so resolve against the current list.
			current, _ := s.Store.Libraries(ctx)
			for _, l := range current {
				if ev.Name == l.Root || strings.HasPrefix(ev.Name, l.Root+string(filepath.Separator)) {
					s.debounced(l.ID)
				}
			}
		case err := <-w.Errors:
			s.Log.Warn("watcher", "err", err)
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				// The kernel dropped events; we can't know what we missed,
				// so re-attach every tree and rescan everything to recover.
				current, _ := s.Store.Libraries(ctx)
				for _, l := range current {
					addTree(w, l.Root, s.Log)
				}
				s.TriggerAll(ctx)
			}
		}
	}
}

func addTree(w *fsnotify.Watcher, root string, log *slog.Logger) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if p != root && strings.HasPrefix(d.Name(), ".") {
			return fs.SkipDir
		}
		if err := w.Add(p); err != nil {
			log.Warn("watch", "path", p, "err", err)
		}
		return nil
	})
}
