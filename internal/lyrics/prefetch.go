package lyrics

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// prefetchIdle is how long the worker sleeps when nothing is left to look up.
const prefetchIdle = 10 * time.Minute

// RunPrefetch looks up one track per tick until ctx is done; every <= 0 returns at once.
// Order: visible, not broken, no stored lyrics, no fresh miss, not marked "no lyrics" by the
// admin, not in failure backoff,
// not instrumental; favorited by anyone first, then kept, then newest.
//
// It never spins: with nothing left it sleeps prefetchIdle, and after a
// lookup where every provider failed (an outage) it pauses for FailRetry
// rather than moving on to the next track at full rate. A track's backoff
// doubles per consecutive failure (Backoff), so one track that keeps
// failing is not re-picked after every pause and can't starve the rest.
func (s *Service) RunPrefetch(ctx context.Context, every time.Duration) {
	if every <= 0 {
		return
	}
	for {
		wait := every
		id, ok, err := s.nextPrefetch(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			s.log().Warn("lyrics prefetch: pick next track", "err", err)
			wait = prefetchIdle
		case !ok:
			wait = prefetchIdle
		default:
			if _, err := s.autoLookup(ctx, id); err != nil {
				if ctx.Err() != nil {
					return
				}
				// Don't retry the same track every tick (head-of-line).
				s.log().Warn("lyrics prefetch failed", "track", id, "err", err)
				s.backoff(id)
			}
			if s.inBackoff(id) {
				wait = s.failRetry()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (s *Service) backoff(trackID int64) {
	s.failed.Fail(trackID, s.now(), s.failRetry(), s.missRetry())
}

// inBackoff: an on-demand lookup should wait (plain FailRetry).
func (s *Service) inBackoff(trackID int64) bool {
	return s.failed.Active(trackID, s.now(), s.failRetry())
}

// backedOff lists the tracks prefetch skips (the doubled backoff).
func (s *Service) backedOff() []any { return s.failed.ActiveIDs(s.now(), s.missRetry()) }

func (s *Service) nextPrefetch(ctx context.Context) (trackID int64, ok bool, err error) {
	args := []any{s.now().Add(-s.missRetry()).Unix()}
	skip := ""
	if ids := s.backedOff(); len(ids) > 0 {
		skip = " AND t.id NOT IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
		args = append(args, ids...)
	}
	err = s.DB.QueryRowContext(ctx, `SELECT t.id FROM tracks t LEFT JOIN lyrics_lookup l ON l.track_id=t.id
		WHERE t.status!='trashed' AND t.missing_since IS NULL AND t.broken=0
		  AND NOT EXISTS (SELECT 1 FROM lyrics y WHERE y.track_id=t.id)
		  AND (l.track_id IS NULL OR (l.found=0 AND l.manual=0 AND l.attempted_at < ?))
		  AND NOT EXISTS (SELECT 1 FROM track_tags tt JOIN tags g ON g.id=tt.tag_id
		                  WHERE tt.track_id=t.id AND tt.removed=0 AND g.name='instrumental')`+skip+`
		ORDER BY t.id IN (SELECT track_id FROM favorites) DESC, (t.status='kept') DESC, t.added_at DESC, t.id DESC
		LIMIT 1`, args...).Scan(&trackID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return trackID, true, nil
}
