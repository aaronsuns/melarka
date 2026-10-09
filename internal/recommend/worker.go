package recommend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	nightlyKey  = "recommendations.nightly_day"
	idleMax     = 10 * time.Minute // re-check at least this often (clock changes)
	defaultAt   = "02:30"
	defaultBack = time.Minute
	defaultGap  = 2 * time.Second
)

// ParseRefreshAt validates an "HH:MM" time of day (config recommendations.refresh_at).
func ParseRefreshAt(s string) (hm [2]int, err error) {
	var h, m int
	var rest string
	if n, _ := fmt.Sscanf(s+"|", "%d:%d%s", &h, &m, &rest); n != 3 || rest != "|" || h < 0 || h > 23 || m < 0 || m > 59 {
		return hm, fmt.Errorf("refresh_at %q: want HH:MM (00:00–23:59)", s)
	}
	return [2]int{h, m}, nil
}

// dueAt is today's (now's day, now's location) nightly refresh time.
func dueAt(now time.Time, at string) (time.Time, error) {
	hm, err := ParseRefreshAt(at)
	if err != nil {
		return time.Time{}, err
	}
	y, mo, d := now.Date()
	return time.Date(y, mo, d, hm[0], hm[1], 0, 0, now.Location()), nil
}

// Run refreshes users one at a time until ctx ends: on-demand requests
// first, then — once today's RefreshAt has passed — every user with plays
// or favorites who wasn't refreshed (or tried) since. It never spins: a
// failed refresh is recorded (not retried until asked again or the next
// night) and followed by ErrBackoff, which a new request can't cut short;
// with nothing to do it sleeps until the next due time or a request.
func (s *Service) Run(ctx context.Context) {
	s.init()
	for {
		wait, err := s.step(ctx)
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(wait)
		if err != nil {
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func orDur(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// step does at most one user's refresh and says how long to wait before
// the next; a non-nil error means back off (wait is ErrBackoff).
func (s *Service) step(ctx context.Context) (time.Duration, error) {
	backoff := orDur(s.ErrBackoff, defaultBack)
	at := s.RefreshAt
	if at == "" {
		at = defaultAt
	}
	now := s.now()
	due, err := dueAt(now, at)
	if err != nil {
		s.log().Error("recommendations: bad refresh_at", "err", err)
		return idleMax, err
	}
	uid, err := s.nextUser(ctx, now, due)
	if err != nil {
		if ctx.Err() == nil {
			s.log().Warn("recommendations: pick next user", "err", err)
		}
		return backoff, err
	}
	if uid == 0 {
		next := due
		if !now.Before(due) {
			next = due.AddDate(0, 0, 1)
		}
		return min(max(next.Sub(now), time.Second), idleMax), nil
	}
	s.mu.Lock()
	s.running = uid
	s.mu.Unlock()
	err = s.Refresh(ctx, uid)
	s.mu.Lock()
	s.running = 0
	s.mu.Unlock()
	if err != nil {
		if ctx.Err() != nil {
			return 0, err
		}
		s.log().Warn("recommendations: refresh failed", "user", uid, "err", err)
		if _, err2 := s.DB.ExecContext(ctx, `INSERT INTO recommendation_users(user_id,attempted_at) VALUES (?,?)
			ON CONFLICT(user_id) DO UPDATE SET attempted_at=excluded.attempted_at`, uid, s.now().Unix()); err2 != nil {
			s.log().Warn("recommendations: record failure", "user", uid, "err", err2)
		}
		return backoff, err
	}
	s.log().Info("recommendations refreshed", "user", uid)
	return orDur(s.Pause, defaultGap), nil
}

// nextUser: the oldest pending on-demand request; else, when the nightly
// run is due and not finished today, the next active user not refreshed or
// tried since it became due (none left: today's run is recorded as done).
// 0 means nothing to do.
func (s *Service) nextUser(ctx context.Context, now, due time.Time) (int64, error) {
	var uid int64
	err := s.DB.QueryRowContext(ctx, `SELECT user_id FROM recommendation_users WHERE `+pendingWhere+`
		ORDER BY requested_at, user_id LIMIT 1`).Scan(&uid)
	if err == nil {
		return uid, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if now.Before(due) {
		return 0, nil
	}
	today := now.Format(time.DateOnly)
	var done string
	if err := s.DB.QueryRowContext(ctx, `SELECT value FROM app_state WHERE key=?`, nightlyKey).Scan(&done); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if done == today {
		return 0, nil
	}
	err = s.DB.QueryRowContext(ctx, `SELECT u.id FROM users u LEFT JOIN recommendation_users r ON r.user_id=u.id
		WHERE MAX(COALESCE(r.refreshed_at,0), COALESCE(r.attempted_at,0)) < ?
		  AND (EXISTS (SELECT 1 FROM play_events e WHERE e.user_id=u.id AND e.started_at>=? AND e.played_seconds>=?)
		    OR EXISTS (SELECT 1 FROM favorites f WHERE f.user_id=u.id))
		ORDER BY u.id LIMIT 1`, due.Unix(), now.Add(-playWindow).Unix(), minPlayedSeconds).Scan(&uid)
	if err == nil {
		return uid, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO app_state(key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, nightlyKey, today)
	return 0, err
}
