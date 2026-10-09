package artwork

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/lyrics"
)

// Service finds covers across providers, caches them as JPEGs in Dir and
// remembers the answers in artwork_lookup.
type Service struct {
	DB        *sql.DB
	Library   *library.Store
	Providers []Provider
	Imager    Imager
	HTTP      lyrics.HTTPDoer // image downloads; nil = 15 s client
	Dir       string          // <data>/cache/artwork
	Log       *slog.Logger
	Budget    time.Duration // online search; default 6s
	MissRetry time.Duration // default 30 * 24h
	FailRetry time.Duration // default 10m
	Parallel  int           // lookups at once; default 2
	// LookupTimeout bounds one whole lookup (default 60s) and LocalTimeout
	// its local phase — embedded/folder search and conversion (default
	// 15s) — so a stalled disk or a hung ffprobe/ffmpeg always frees its slot.
	LookupTimeout time.Duration
	LocalTimeout  time.Duration
	Now           func() time.Time

	once   sync.Once
	sem    chan struct{} // bounds lookups (and so ffmpeg runs) across tracks
	mu     sync.Mutex
	calls  map[int64]*call // one lookup per track at a time
	failed lyrics.Backoff  // tracks whose lookups failed lately
}

type call struct {
	done    chan struct{}
	err     error
	waiters int // callers still waiting (guarded by Service.mu)
}

func (s *Service) init() {
	s.once.Do(func() {
		n := s.Parallel
		if n <= 0 {
			n = 2
		}
		s.sem = make(chan struct{}, n)
		s.mu.Lock()
		s.calls = map[int64]*call{}
		s.mu.Unlock()
	})
}

func (s *Service) budget() time.Duration        { return orDefault(s.Budget, 6*time.Second) }
func (s *Service) missRetry() time.Duration     { return orDefault(s.MissRetry, 30*24*time.Hour) }
func (s *Service) failRetry() time.Duration     { return orDefault(s.FailRetry, 10*time.Minute) }
func (s *Service) lookupTimeout() time.Duration { return orDefault(s.LookupTimeout, 60*time.Second) }
func (s *Service) localTimeout() time.Duration  { return orDefault(s.LocalTimeout, 15*time.Second) }

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// backoff: on-demand requests wait FailRetry since the last failure; the
// prefetch worker waits FailRetry doubled per consecutive failure (up to
// MissRetry), so one track that keeps failing can't monopolise it.
func (s *Service) backoff(trackID int64) {
	s.failed.Fail(trackID, s.now(), s.failRetry(), s.missRetry())
}

// inBackoff: an on-demand lookup should wait (plain FailRetry).
func (s *Service) inBackoff(trackID int64) bool {
	return s.failed.Active(trackID, s.now(), s.failRetry())
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func validSize(n int) bool { return n == 300 || n == 1000 }

func (s *Service) File(trackID int64, size int) string {
	return filepath.Join(s.Dir, fmt.Sprintf("%d-%d.jpg", trackID, size))
}

func (s *Service) cached(trackID int64) bool {
	for _, n := range Sizes {
		if _, err := os.Stat(s.File(trackID, n)); err != nil {
			return false
		}
	}
	return true
}

func (s *Service) Get(ctx context.Context, trackID int64, size int) (string, error) {
	if !validSize(size) {
		return "", ErrBadSize
	}
	if _, err := s.Library.Track(ctx, 0, trackID); errors.Is(err, library.ErrNotFound) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	p := s.File(trackID, size)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	if err := s.ensure(ctx, trackID); err != nil {
		return "", err
	}
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	return "", ErrNoCover
}

// ensure runs at most one lookup per track at a time and at most Parallel
// overall. The lookup is detached from the caller's ctx: a caller that gives
// up (a cover scrolled away, the 10 s handler cap) doesn't waste the work —
// the answer is cached for the next request.
func (s *Service) ensure(ctx context.Context, trackID int64) error {
	s.init()
	s.mu.Lock()
	c, ok := s.calls[trackID]
	if !ok {
		c = &call{done: make(chan struct{})}
		s.calls[trackID] = c
		go s.run(context.WithoutCancel(ctx), trackID, c)
	}
	c.waiters++
	s.mu.Unlock()
	select {
	case <-c.done:
		return c.err
	case <-ctx.Done():
		s.mu.Lock()
		c.waiters--
		s.mu.Unlock()
		return ctx.Err()
	}
}

// run waits for a slot, then resolves the track under LookupTimeout. A
// lookup whose callers all gave up while it queued (a list scrolled past)
// is dropped without running; once running it finishes and caches its
// answer. A lookup that times out only backs off: the slot is freed even
// if something below it ignores its ctx.
func (s *Service) run(base context.Context, trackID int64, c *call) {
	defer close(c.done)
	s.sem <- struct{}{}
	defer func() { <-s.sem }()
	s.mu.Lock()
	if c.waiters == 0 {
		delete(s.calls, trackID)
		s.mu.Unlock()
		c.err = context.Canceled
		return
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(base, s.lookupTimeout())
	defer cancel()
	_, err := bounded(ctx, func() (struct{}, error) { return struct{}{}, s.resolve(ctx, trackID) })
	if ctx.Err() != nil {
		s.log().Warn("artwork lookup timed out", "track", trackID, "timeout", s.lookupTimeout())
		s.backoff(trackID)
		err = nil // no answer: the caller sees "no cover" for now
	}
	c.err = err
	s.mu.Lock()
	delete(s.calls, trackID)
	s.mu.Unlock()
}

// bounded runs fn but returns as soon as ctx is done, even if fn ignores it.
func bounded[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	type res struct {
		v   T
		err error
	}
	ch := make(chan res, 1) // buffered: a late fn never blocks
	go func() {
		v, err := fn()
		ch <- res{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

func (s *Service) resolve(ctx context.Context, trackID int64) error {
	if s.cached(trackID) {
		return nil
	}
	var at int64
	var found bool
	err := s.DB.QueryRowContext(ctx, `SELECT attempted_at, found FROM artwork_lookup WHERE track_id=?`, trackID).Scan(&at, &found)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	case !found && s.now().Before(time.Unix(at, 0).Add(s.missRetry())):
		return nil // fresh miss; found=1 with files gone falls through and rebuilds
	}
	if s.inBackoff(trackID) {
		return nil
	}
	q, err := lyrics.BuildQuery(ctx, s.DB, s.Library, trackID)
	if errors.Is(err, lyrics.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return s.lookup(ctx, q)
}

// lookup asks the local sources one at a time (all within LocalTimeout),
// then every online source at once. A clean "nobody has it" is remembered
// for MissRetry; when a local source errored, every online source errored, or a found image couldn't
// be downloaded or converted (a bad ffmpeg_path, a full cache disk, a
// timeout), the track only backs off in memory for FailRetry — so a found=1
// row whose cache was wiped is never downgraded by a failed rebuild.
func (s *Service) lookup(ctx context.Context, q lyrics.Query) error {
	localAnswered, remote := 0, 0
	localFailed := false
	lctx, lcancel := context.WithTimeout(ctx, s.localTimeout())
	defer lcancel()
	for _, p := range s.Providers {
		if !isLocal(p) {
			remote++
			continue
		}
		fs, err := bounded(lctx, func() ([]Found, error) { return p.Find(lctx, q) })
		if err != nil {
			// It may still hold the cover (an embedded picture behind a
			// timed-out ffprobe): never let this become a remembered miss.
			localFailed = true
			s.log().Warn("artwork provider failed", "provider", p.Name(), "track", q.TrackID, "err", err)
			continue
		}
		localAnswered++
		for _, f := range fs {
			if err := s.write(lctx, q.TrackID, f); err != nil {
				localFailed = true
				s.log().Warn("artwork: unusable image", "provider", p.Name(), "track", q.TrackID, "err", err)
				continue
			}
			return s.record(ctx, q.TrackID, true, f.Source)
		}
	}
	lcancel()
	if remote == 0 {
		if localAnswered > 0 && !localFailed {
			return s.record(ctx, q.TrackID, false, "")
		}
		s.backoff(q.TrackID)
		return nil
	}
	answers, answered := s.searchOnline(ctx, q)
	fetchFailed := false
	for _, fs := range answers { // provider order
		for _, f := range fs {
			if err := s.write(ctx, q.TrackID, f); err != nil {
				fetchFailed = true
				s.log().Warn("artwork: unusable image", "source", f.Source, "track", q.TrackID, "err", err)
				continue
			}
			return s.record(ctx, q.TrackID, true, f.Source)
		}
	}
	if answered > 0 && !fetchFailed && !localFailed {
		return s.record(ctx, q.TrackID, false, "")
	}
	s.backoff(q.TrackID)
	return nil
}

// searchOnline asks every online provider at once within the budget; each
// tries the lyrics query variants in turn and keeps the first variant's
// matching images. answers follows provider order.
func (s *Service) searchOnline(ctx context.Context, q lyrics.Query) (answers [][]Found, answered int) {
	var online []Provider
	for _, p := range s.Providers {
		if !isLocal(p) {
			online = append(online, p)
		}
	}
	qs := lyrics.SearchQueries(q)
	bctx, cancel := context.WithTimeout(ctx, s.budget())
	defer cancel()
	type ans struct {
		i   int
		fs  []Found
		err error
	}
	ch := make(chan ans, len(online)) // buffered: late answers never block
	for i, p := range online {
		go func() {
			defer func() {
				if v := recover(); v != nil {
					ch <- ans{i: i, err: fmt.Errorf("panic: %v", v)}
				}
			}()
			fs, err := findMatching(bctx, p, qs)
			ch <- ans{i, fs, err}
		}()
	}
	answers = make([][]Found, len(online))
collect:
	for range online {
		select {
		case a := <-ch:
			if a.err != nil {
				s.log().Warn("artwork provider failed", "provider", online[a.i].Name(), "track", q.TrackID, "err", a.err)
				continue
			}
			answered++
			answers[a.i] = a.fs
		case <-bctx.Done():
			s.log().Warn("artwork lookup budget exhausted", "track", q.TrackID, "budget", s.budget())
			break collect
		}
	}
	return answers, answered
}

func findMatching(ctx context.Context, p Provider, qs []lyrics.Query) ([]Found, error) {
	var firstErr error
	ok := false
	for _, v := range qs {
		fs, err := p.Find(ctx, v)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ok = true
		var m []Found
		for _, f := range fs {
			if f.URL != "" && f.Title != "" && lyrics.Match(v, lyrics.Candidate{Title: f.Title, Artist: f.Artist, DurationS: f.DurationS}) {
				m = append(m, f)
			}
		}
		if len(m) > 0 {
			return m, nil
		}
	}
	if !ok {
		return nil, firstErr
	}
	return nil, nil
}

// write turns f into <track>-300.jpg and <track>-1000.jpg, via temp files
// renamed into place, so a reader never sees half an image.
func (s *Service) write(ctx context.Context, trackID int64, f Found) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	src, stream := f.Path, f.Stream
	if f.URL != "" {
		tmp, err := s.fetch(ctx, f.URL)
		if err != nil {
			return err
		}
		defer os.Remove(tmp)
		src, stream = tmp, -1
	}
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tmps := map[int]string{}
	defer func() {
		for _, t := range tmps {
			os.Remove(t)
		}
	}()
	for _, size := range Sizes {
		t := filepath.Join(s.Dir, fmt.Sprintf(".%d-%d-%d.tmp", trackID, size, time.Now().UnixNano()))
		tmps[size] = t
		if _, err := bounded(wctx, func() (struct{}, error) { return struct{}{}, s.Imager.Square(wctx, src, stream, t, size) }); err != nil {
			return err
		}
	}
	for _, size := range Sizes {
		if err := os.Rename(tmps[size], s.File(trackID, size)); err != nil {
			return err
		}
		delete(tmps, size)
	}
	return nil
}

func (s *Service) record(ctx context.Context, trackID int64, found bool, source string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO artwork_lookup(track_id,attempted_at,found,source) VALUES (?,?,?,?)
		ON CONFLICT(track_id) DO UPDATE SET attempted_at=excluded.attempted_at, found=excluded.found, source=excluded.source`,
		trackID, s.now().Unix(), found, source)
	if err == nil {
		s.failed.Clear(trackID)
	}
	return err
}

func (s *Service) AlbumCover(ctx context.Context, albumID int64, size int) (string, error) {
	if !validSize(size) {
		return "", ErrBadSize
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT t.id, COALESCE(a.found, -1) FROM tracks t
		LEFT JOIN artwork_lookup a ON a.track_id=t.id
		WHERE t.album_id=? AND t.status!='trashed' AND t.missing_since IS NULL AND t.broken=0
		ORDER BY COALESCE(t.disc_no,0), COALESCE(t.track_no,0), t.rel_path LIMIT 50`, albumID)
	if err != nil {
		return "", err
	}
	var ids []int64
	var found []int
	for rows.Next() {
		var id int64
		var f int
		if err := rows.Scan(&id, &f); err != nil {
			rows.Close()
			return "", err
		}
		ids, found = append(ids, id), append(found, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", ErrNotFound
	}
	for _, id := range ids {
		if p := s.File(id, size); fileExists(p) {
			return p, nil
		}
	}
	for _, want := range []int{1, -1} { // a known cover (cache wiped), else the first never-tried track
		for i, id := range ids {
			if found[i] == want {
				return s.Get(ctx, id, size)
			}
		}
	}
	return "", ErrNoCover
}
