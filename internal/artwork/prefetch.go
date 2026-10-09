package artwork

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// prefetchIdle is how long the worker sleeps when nothing is left to look up.
const prefetchIdle = 10 * time.Minute

// RunPrefetch looks one track's cover up per tick until ctx is done; every <= 0 returns at once.
// Order: visible, not broken, never looked up or a miss older than MissRetry, not in backoff;
// favorited by anyone first, then kept, then newest. Nothing left → sleeps 10 minutes.
//
// Lookups go through ensure, so they share the Parallel slots with on-demand
// requests and the worker counts as a waiter (its lookup is never dropped as
// abandoned). It never spins: after a lookup that only backed off (an
// outage, a failed conversion) it pauses FailRetry before the next track.
// A track's backoff doubles per consecutive failure, so one that keeps
// failing is not re-picked after every pause and can't starve the rest.
func (s *Service) RunPrefetch(ctx context.Context, every time.Duration) {
	if every <= 0 {
		return
	}
	s.init()
	for {
		wait := every
		id, ok, err := s.nextPrefetch(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			s.log().Warn("artwork prefetch: pick next track", "err", err)
			wait = prefetchIdle
		case !ok:
			wait = prefetchIdle
		default:
			if err := s.ensure(ctx, id); err != nil && ctx.Err() == nil {
				// Don't retry the same track every tick (head-of-line).
				s.log().Warn("artwork prefetch failed", "track", id, "err", err)
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

// backedOff lists the tracks still in failure backoff.
func (s *Service) backedOff() []any { return s.failed.ActiveIDs(s.now(), s.missRetry()) }

func (s *Service) nextPrefetch(ctx context.Context) (trackID int64, ok bool, err error) {
	args := []any{s.now().Add(-s.missRetry()).Unix()}
	skip := ""
	if ids := s.backedOff(); len(ids) > 0 {
		skip = " AND t.id NOT IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
		args = append(args, ids...)
	}
	err = s.DB.QueryRowContext(ctx, `SELECT t.id FROM tracks t LEFT JOIN artwork_lookup a ON a.track_id=t.id
		WHERE t.status!='trashed' AND t.missing_since IS NULL AND t.broken=0
		  AND (a.track_id IS NULL OR (a.found=0 AND a.attempted_at < ?))`+skip+`
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
