package lastfm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aaronsuns/lark-server/internal/tags"
)

type Source interface {
	TrackTopTags(ctx context.Context, artist, track string) ([]Tag, error)
	ArtistTopTags(ctx context.Context, artist string) ([]Tag, error)
}

type Worker struct {
	DB         *sql.DB
	Source     Source
	Tags       *tags.Store
	Vocab      *tags.Vocab
	Log        *slog.Logger
	MinCount   int           // default 10
	MaxTags    int           // default 6
	Idle       time.Duration // nothing to do → sleep; default 10m
	ErrBackoff time.Duration // any error → sleep; default 1m
	ArtistTTL  time.Duration // artist cache lifetime; default 90 days
	SkipFor    time.Duration // a track Last.fm couldn't answer for is retried after this; default 24h
	Now        func() time.Time

	mu   sync.Mutex
	skip map[int64]time.Time // track id → retry after
}

func orDur(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// skipped lists the tracks still skipped after a per-track error, pruning expired ones.
func (w *Worker) skipped() []any {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	var ids []any
	for id, until := range w.skip {
		if now.Before(until) {
			ids = append(ids, id)
		} else {
			delete(w.skip, id)
		}
	}
	return ids
}

func (w *Worker) skipTrack(id int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.skip == nil {
		w.skip = map[int64]time.Time{}
	}
	w.skip[id] = w.now().Add(orDur(w.SkipFor, 24*time.Hour))
}

// Step tags one track; did is false when no track is waiting.
//
// A Source error about this track (Last.fm error 8, an HTTP 500, a garbled
// answer) puts it in an in-memory skip list for SkipFor so it can't block the
// rest of the library; an outage (ErrUnavailable) or a cancelled ctx doesn't,
// so the same track is simply retried after Run's backoff. Either way the
// error is returned and Run backs off, so at worst one track per ErrBackoff
// is skipped.
func (w *Worker) Step(ctx context.Context) (did bool, err error) {
	id, err := w.step(ctx)
	var se sourceError
	if errors.As(err, &se) && ctx.Err() == nil && !errors.Is(err, ErrUnavailable) {
		w.skipTrack(id)
	}
	return id != 0 && err == nil, err
}

// sourceError marks an error that came from the Source (not the DB).
type sourceError struct{ err error }

func (e sourceError) Error() string { return e.err.Error() }
func (e sourceError) Unwrap() error { return e.err }

func (w *Worker) step(ctx context.Context) (int64, error) {
	var id int64
	var artist, title string
	args := []any{}
	skip := ""
	if ids := w.skipped(); len(ids) > 0 {
		skip = " AND t.id NOT IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
		args = ids
	}
	err := w.DB.QueryRowContext(ctx, `SELECT t.id, COALESCE(NULLIF(o.artist,''), t.tag_artist), COALESCE(NULLIF(o.title,''), t.tag_title)
		FROM tracks t LEFT JOIN track_overrides o ON o.track_id=t.id LEFT JOIN tagging_state s ON s.track_id=t.id
		WHERE t.status!='trashed' AND t.missing_since IS NULL AND t.broken=0 AND s.lastfm_at IS NULL
		  AND COALESCE(NULLIF(o.artist,''), t.tag_artist) != ''`+skip+`
		ORDER BY (t.status='kept') DESC, t.added_at DESC, t.id DESC LIMIT 1`, args...).Scan(&id, &artist, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var mapped []tags.Tag
	if title != "" {
		ts, err := w.Source.TrackTopTags(ctx, artist, title)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return id, sourceError{err}
		}
		mapped = w.mapTags(ts)
	}
	if len(mapped) == 0 {
		ts, err := w.artistTags(ctx, artist)
		if err != nil {
			return id, err
		}
		mapped = w.mapTags(ts)
	}
	if err := w.Tags.Replace(ctx, id, "lastfm", mapped); err != nil {
		return id, err
	}
	if _, err := w.DB.ExecContext(ctx, `INSERT INTO tagging_state(track_id, lastfm_at) VALUES (?,?)
		ON CONFLICT(track_id) DO UPDATE SET lastfm_at=excluded.lastfm_at`, id, w.now().Unix()); err != nil {
		return id, err
	}
	return id, nil
}

func (w *Worker) mapTags(ts []Tag) []tags.Tag {
	minCount := w.MinCount
	if minCount <= 0 {
		minCount = 10
	}
	maxTags := w.MaxTags
	if maxTags <= 0 {
		maxTags = 6
	}
	out := []tags.Tag{}
	seen := map[string]bool{}
	for _, t := range ts {
		if t.Count < minCount {
			continue
		}
		e, ok := w.Vocab.Lookup(t.Name)
		if !ok || seen[e.Slug] {
			continue
		}
		seen[e.Slug] = true
		out = append(out, tags.Tag{Name: e.Slug, Kind: e.Kind})
		if len(out) == maxTags {
			break
		}
	}
	return out
}

type cachedTag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// artistTags returns the artist's Last.fm tags from the cache when fresh,
// otherwise fetches and stores them (an empty list is stored too).
func (w *Worker) artistTags(ctx context.Context, artist string) ([]Tag, error) {
	var raw string
	var fetched int64
	err := w.DB.QueryRowContext(ctx, `SELECT tags, fetched_at FROM lastfm_artist_tags WHERE artist=?`, artist).Scan(&raw, &fetched)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil && w.now().Sub(time.Unix(fetched, 0)) < orDur(w.ArtistTTL, 90*24*time.Hour) {
		var cs []cachedTag
		if json.Unmarshal([]byte(raw), &cs) == nil {
			out := make([]Tag, len(cs))
			for i, c := range cs {
				out[i] = Tag{c.Name, c.Count}
			}
			return out, nil
		}
	}
	ts, err := w.Source.ArtistTopTags(ctx, artist)
	if errors.Is(err, ErrNotFound) {
		ts, err = nil, nil
	}
	if err != nil {
		return nil, sourceError{err}
	}
	cs := make([]cachedTag, len(ts))
	for i, t := range ts {
		cs[i] = cachedTag{t.Name, t.Count}
	}
	b, _ := json.Marshal(cs)
	if _, err := w.DB.ExecContext(ctx, `INSERT INTO lastfm_artist_tags(artist,tags,fetched_at) VALUES (?,?,?)
		ON CONFLICT(artist) DO UPDATE SET tags=excluded.tags, fetched_at=excluded.fetched_at`, artist, string(b), w.now().Unix()); err != nil {
		return nil, err
	}
	return ts, nil
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// Run steps until ctx is done.
func (w *Worker) Run(ctx context.Context) {
	log := w.Log
	if log == nil {
		log = slog.Default()
	}
	for ctx.Err() == nil {
		did, err := w.Step(ctx)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			log.Warn("last.fm tagging", "err", err)
			sleep(ctx, orDur(w.ErrBackoff, time.Minute))
		case !did:
			sleep(ctx, orDur(w.Idle, 10*time.Minute))
		}
	}
}
