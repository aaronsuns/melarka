package recommend

import (
	"context"
	"database/sql"
	"errors"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// Reason is the seed a recommendation came from.
type Reason struct {
	TrackID int64  `json:"track_id"`
	Title   string `json:"title"`
	Artist  string `json:"artist"`
	Kind    string `json:"kind"` // played | favorite
}

type Item struct {
	VideoID   string  `json:"video_id"`
	Title     string  `json:"title"`
	Channel   string  `json:"channel"`
	DurationS int     `json:"duration_s"`
	Thumbnail string  `json:"thumbnail"`
	URL       string  `json:"url"`
	Reason    *Reason `json:"reason"` // nil when the seed track is gone
	Score     float64 `json:"score"`
}

type Result struct {
	Items       []Item `json:"items"`
	RefreshedAt *int64 `json:"refreshed_at"`
	Refreshing  bool   `json:"refreshing"`
}

// pendingWhere: a refresh was asked for after the last one finished or failed.
const pendingWhere = `requested_at > MAX(COALESCE(refreshed_at,0), COALESCE(attempted_at,0))`

// List is userID's current recommendations, best first, minus anything
// dismissed or finished downloading since the refresh.
func (s *Service) List(ctx context.Context, userID int64) (Result, error) {
	res := Result{Items: []Item{}}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.video_id, r.title, r.channel, r.duration_s, r.thumbnail, r.score,
		r.reason_track_id, r.reason_kind FROM recommendations r
		WHERE r.user_id=?
		  AND NOT EXISTS (SELECT 1 FROM downloads d WHERE d.video_id=r.video_id AND d.status='done')
		  AND NOT EXISTS (SELECT 1 FROM recommendation_dismissals x WHERE x.user_id=r.user_id AND x.video_id=r.video_id)
		ORDER BY r.score DESC, r.rowid`, userID)
	if err != nil {
		return res, err
	}
	var reasonIDs []int64
	kinds := []string{}
	var reasons []sql.NullInt64
	for rows.Next() {
		var it Item
		var reason sql.NullInt64
		var kind string
		if err := rows.Scan(&it.VideoID, &it.Title, &it.Channel, &it.DurationS, &it.Thumbnail, &it.Score, &reason, &kind); err != nil {
			rows.Close()
			return res, err
		}
		it.URL = ytdlp.WatchURL(it.VideoID)
		res.Items = append(res.Items, it)
		reasons = append(reasons, reason)
		kinds = append(kinds, kind)
		if reason.Valid {
			reasonIDs = append(reasonIDs, reason.Int64)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	tracks, err := s.Library.TracksByIDs(ctx, userID, reasonIDs)
	if err != nil {
		return res, err
	}
	byID := map[int64]Reason{}
	for _, t := range tracks {
		byID[t.ID] = Reason{TrackID: t.ID, Title: t.Title, Artist: t.Artist}
	}
	for i, r := range reasons {
		if rs, ok := byID[r.Int64]; r.Valid && ok {
			rs.Kind = kinds[i]
			res.Items[i].Reason = &rs
		}
	}
	var refreshed sql.NullInt64
	var pending bool
	err = s.DB.QueryRowContext(ctx, `SELECT refreshed_at, COALESCE(`+pendingWhere+`,0) FROM recommendation_users WHERE user_id=?`, userID).
		Scan(&refreshed, &pending)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return res, err
	}
	if refreshed.Valid {
		res.RefreshedAt = &refreshed.Int64
	}
	s.mu.Lock()
	res.Refreshing = pending || s.running == userID
	s.mu.Unlock()
	return res, nil
}

// Request queues an on-demand refresh for userID, with no time limit. A
// request while one is already pending or running for that user does
// nothing (deduped); otherwise it is queued at once. requested_at is set past
// the last finish/failure so a request in that same second still counts.
func (s *Service) Request(ctx context.Context, userID int64) error {
	s.mu.Lock()
	running := s.running == userID
	s.mu.Unlock()
	if running {
		return nil
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO recommendation_users(user_id,requested_at) VALUES (?1,?2)
		ON CONFLICT(user_id) DO UPDATE SET requested_at=MAX(?2, COALESCE(refreshed_at,0)+1, COALESCE(attempted_at,0)+1)
		WHERE NOT COALESCE(`+pendingWhere+`, 0)`, userID, s.now().Unix()); err != nil {
		return err
	}
	s.init()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

// Dismiss hides videoID from userID's recommendations for good ("不感兴趣").
func (s *Service) Dismiss(ctx context.Context, userID int64, videoID string) error {
	if !ytdlp.IsVideoID(videoID) {
		return ErrBadVideo
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO recommendation_dismissals(user_id,video_id,created_at) VALUES (?,?,?)`,
		userID, videoID, s.now().Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM recommendations WHERE user_id=? AND video_id=?`, userID, videoID); err != nil {
		return err
	}
	return tx.Commit()
}
