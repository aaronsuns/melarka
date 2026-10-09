package channels

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/aaronsuns/lark-server/internal/recommend"
)

// refresher is one per-user background refresh fed by YouTube Mixes:
// channel discovery (§17.4) and 视频 为你推荐 (§18.2). Both share one loop
// (RunDiscovery), which runs at most one user at a time.
type refresher struct {
	name     string // "discovery" | "videorecs" — log and running-map key
	table    string // discovery_users | video_rec_users (a constant, never input)
	dayKey   string // app_state key: the nightly run's last finished day
	budget   int    // on-demand Mix calls per user per day
	eligible string // SQL boolean on u.id: who the nightly run refreshes (?1 is the run's due time)
	run      func(ctx context.Context, userID int64, budgeted bool) error
}

// pendingSQL: requested_at > MAX(refreshed_at, attempted_at) means an on-demand request waits.
const pendingSQL = `requested_at > MAX(COALESCE(refreshed_at,0), COALESCE(attempted_at,0))`

func (s *Service) discoveryRefresher() *refresher {
	return &refresher{name: "discovery", table: "discovery_users", dayKey: discoveryDayKey, budget: mixesPerDay,
		eligible: `EXISTS (SELECT 1 FROM channel_follows f WHERE f.user_id=u.id)`, run: s.discover}
}

// refreshers is every refresher the loop serves, in priority order.
func (s *Service) refreshers() []*refresher {
	return []*refresher{s.discoveryRefresher(), s.videoRefresher()}
}

// isRunning: r is refreshing userID right now.
func (s *Service) isRunning(r *refresher, userID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return userID != 0 && s.running[r.name] == userID
}

func (s *Service) setRunning(r *refresher, userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running == nil {
		s.running = map[string]int64{}
	}
	s.running[r.name] = userID
}

// RequestDiscovery queues an on-demand refresh. One already pending or
// running is joined (nil); past mixesPerDay Mix calls today it is refused (ErrTooSoon).
func (s *Service) RequestDiscovery(ctx context.Context, userID int64) error {
	return s.request(ctx, s.discoveryRefresher(), userID)
}

// request queues an on-demand refresh of r for userID: one already pending
// or running is joined (nil); past r.budget Mix calls today, ErrTooSoon.
func (s *Service) request(ctx context.Context, r *refresher, userID int64) error {
	now := s.now()
	var day string
	var mixes int
	var pending bool
	err := s.DB.QueryRowContext(ctx, `SELECT day, mixes, COALESCE(`+pendingSQL+`,0) FROM `+r.table+` WHERE user_id=?`, userID).Scan(&day, &mixes, &pending)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if pending || s.isRunning(r, userID) {
		return nil
	}
	if day == now.Format(time.DateOnly) && mixes >= r.budget {
		return ErrTooSoon
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO `+r.table+`(user_id,requested_at) VALUES (?,?)
		ON CONFLICT(user_id) DO UPDATE SET requested_at=excluded.requested_at`, userID, now.Unix()); err != nil {
		return err
	}
	s.init()
	kick(s.discKick)
	return nil
}

// addMixes counts n of today's on-demand Mix calls of r for userID.
func (s *Service) addMixes(ctx context.Context, r *refresher, userID int64, n int) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO `+r.table+`(user_id,day,mixes) VALUES (?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET mixes=CASE WHEN day=excluded.day THEN mixes+excluded.mixes ELSE excluded.mixes END, day=excluded.day`,
		userID, s.now().Format(time.DateOnly), n)
	return err
}

// RunDiscovery serves every refresher (channel discovery, 视频 为你推荐),
// one user at a time, until ctx ends: on-demand requests first, then — once
// today's RefreshAt has passed — each refresher's nightly run. Never spins.
func (s *Service) RunDiscovery(ctx context.Context) {
	s.init()
	for {
		wait := s.refreshStep(ctx)
		if ctx.Err() != nil {
			return
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-s.discKick:
			t.Stop()
		case <-t.C:
		}
	}
}

// refreshStep runs at most one user's refresh and says how long to wait.
func (s *Service) refreshStep(ctx context.Context) time.Duration {
	now := s.now()
	at := s.RefreshAt
	if at == "" {
		at = "02:30"
	}
	hm, err := recommend.ParseRefreshAt(at)
	if err != nil {
		s.log().Error("channels: bad refresh time", "err", err)
		return pollIdleMax
	}
	due := time.Date(now.Year(), now.Month(), now.Day(), hm[0], hm[1], 0, 0, now.Location())
	s.dailyPrune(ctx, now)
	rs := s.refreshers()
	var r *refresher
	var uid int64
	var requested bool
	for _, c := range rs { // every pending on-demand request first
		id, err := s.pendingUser(ctx, c)
		if err != nil {
			if ctx.Err() == nil {
				s.log().Warn("channels: pick next "+c.name, "err", err)
			}
			return time.Minute
		}
		if id != 0 {
			r, uid, requested = c, id, true
			break
		}
	}
	if r == nil {
		for _, c := range rs {
			id, err := s.nightlyUser(ctx, c, now, due)
			if err != nil {
				if ctx.Err() == nil {
					s.log().Warn("channels: pick next "+c.name, "err", err)
				}
				return time.Minute
			}
			if id != 0 {
				r, uid = c, id
				break
			}
		}
	}
	if r == nil {
		next := due
		if !now.Before(due) {
			next = due.AddDate(0, 0, 1)
		}
		return min(max(next.Sub(now), time.Second), pollIdleMax*6)
	}
	s.setRunning(r, uid)
	err = r.run(ctx, uid, requested)
	s.setRunning(r, 0)
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		s.log().Warn("channels: "+r.name+" failed", "user", uid, "err", err)
		s.DB.ExecContext(ctx, `INSERT INTO `+r.table+`(user_id,attempted_at) VALUES (?,?)
			ON CONFLICT(user_id) DO UPDATE SET attempted_at=excluded.attempted_at`, uid, s.now().Unix())
		return time.Minute
	}
	return orDur(s.Pause, defaultPause)
}

// pendingUser is the oldest pending on-demand request of r (0: none).
func (s *Service) pendingUser(ctx context.Context, r *refresher) (int64, error) {
	var uid int64
	err := s.DB.QueryRowContext(ctx, `SELECT user_id FROM `+r.table+` WHERE `+pendingSQL+` ORDER BY requested_at, user_id LIMIT 1`).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return uid, err
}

// nightlyUser is the next eligible user r's nightly run hasn't refreshed
// (or tried) since due; with none left, today is marked done (0).
func (s *Service) nightlyUser(ctx context.Context, r *refresher, now, due time.Time) (int64, error) {
	if now.Before(due) {
		return 0, nil
	}
	today := now.Format(time.DateOnly)
	var done string
	if err := s.DB.QueryRowContext(ctx, `SELECT value FROM app_state WHERE key=?`, r.dayKey).Scan(&done); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if done == today {
		return 0, nil
	}
	var uid int64
	err := s.DB.QueryRowContext(ctx, `SELECT u.id FROM users u LEFT JOIN `+r.table+` d ON d.user_id=u.id
		WHERE MAX(COALESCE(d.refreshed_at,0), COALESCE(d.attempted_at,0)) < ?1
		  AND `+r.eligible+`
		ORDER BY u.id LIMIT 1`, due.Unix()).Scan(&uid)
	if err == nil {
		return uid, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO app_state(key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, r.dayKey, today)
	return 0, err
}
