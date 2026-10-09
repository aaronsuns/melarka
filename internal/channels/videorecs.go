package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// 视频 为你推荐: the videos most often found in the YouTube Mixes
// of what a user recently watched and searched for, weighted by how often.

const (
	videoSeeds     = 10
	videoMix       = 25
	videoKeep      = 30
	videoBudget    = 40 // on-visit yt-dlp Mix calls per user per day
	staleAfter     = 30 * time.Minute
	minVideoS      = 60
	maxVideoS      = 4 * 3600
	videoRecsDay   = "video.recs_day"
	videoSeedDays  = 30
	videoSeedSpanS = videoSeedDays * 24 * 3600
)

type VideoRec struct {
	VideoID    string  `json:"video_id"`
	Title      string  `json:"title"`
	Channel    string  `json:"channel"`
	ChannelID  string  `json:"channel_id"`
	DurationS  int     `json:"duration_s"`
	Thumbnail  string  `json:"thumbnail"` // the proxy
	Score      float64 `json:"score"`
	ReasonKind string  `json:"reason_kind"` // watch | search
	Reason     string  `json:"reason"`      // the strongest seed's title or query (untrusted text)
}

type VideoRecs struct {
	Items       []VideoRec `json:"items"`
	RefreshedAt *int64     `json:"refreshed_at"`
	Refreshing  bool       `json:"refreshing"`
}

func (s *Service) videoRefresher() *refresher {
	// ?1 is the nightly run's due time (refresher.go): "recent" follows the
	// service clock, not SQLite's.
	return &refresher{name: "videorecs", table: "video_rec_users", dayKey: videoRecsDay, budget: videoBudget,
		eligible: fmt.Sprintf(`EXISTS (SELECT 1 FROM video_history h WHERE h.user_id=u.id AND h.last_at>=?1-%d)`, videoSeedSpanS),
		run:      s.refreshVideoRecs}
}

type videoSeed struct {
	video, label, kind string
	times              int
}

// videoSeeds: userID's last 30 days of watches and searches with a seed
// video, most frequent first, then most recent.
func (s *Service) videoSeeds(ctx context.Context, userID int64) ([]videoSeed, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT seed_video_id, label, kind, times FROM video_history
		WHERE user_id=? AND seed_video_id!='' AND last_at>=?
		ORDER BY times DESC, last_at DESC, rowid DESC LIMIT ?`, userID, s.now().Unix()-videoSeedSpanS, videoSeeds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []videoSeed
	for rows.Next() {
		var sd videoSeed
		if err := rows.Scan(&sd.video, &sd.label, &sd.kind, &sd.times); err != nil {
			return nil, err
		}
		out = append(out, sd)
	}
	return out, rows.Err()
}

// refreshVideoRecs rebuilds userID's 为你推荐. budgeted: its fetched (not
// cached) Mixes count against the daily on-visit budget. When every Mix
// failed the previous list stays.
func (s *Service) refreshVideoRecs(ctx context.Context, userID int64, budgeted bool) error {
	// Taken before the seeds are read: history recorded while this runs is
	// newer than refreshed_at (so it is refreshed again), and a clear while
	// this runs makes gen old (so nothing built from the cleared history is stored).
	start, gen := s.now(), s.historyGen(userID)
	seeds, err := s.videoSeeds(ctx, userID)
	if err != nil {
		return err
	}
	watched, err := s.idSet(ctx, `SELECT item FROM video_history WHERE user_id=? AND kind='watch'`, userID)
	if err != nil {
		return err
	}
	var mixes []seedMix
	var used []videoSeed // parallel to mixes
	fetched, failed := 0, 0
	var lastErr error
	for _, sd := range seeds {
		vids, err := s.CachedMix(ctx, sd.video, func(ctx context.Context) ([]ytdlp.Video, error) {
			fetched++
			var out []ytdlp.Video
			err := s.low(ctx, func(ctx context.Context) error {
				var err error
				out, err = s.YT.Mix(ctx, sd.video, videoMix)
				return err
			})
			return out, err
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failed, lastErr = failed+1, err
			s.log().Warn("channels: 视频 mix", "video", sd.video, "err", err)
			continue
		}
		mixes = append(mixes, seedMix{seedVideo: sd.video, weight: float64(sd.times), videos: vids})
		used = append(used, sd)
	}
	if budgeted && fetched > 0 {
		if err := s.addMixes(ctx, s.videoRefresher(), userID, fetched); err != nil {
			s.log().Warn("channels: 视频 budget", "err", err)
		}
	}
	if len(seeds) > 0 && failed == len(seeds) {
		return fmt.Errorf("channels: every 视频 Mix failed: %w", lastErr)
	}
	ranked := rankMixVideos(mixes, func(v ytdlp.Video) bool {
		// An unknown duration (0: a channel upload a thin Mix got from its feed) is allowed.
		return !watched[v.ID] && !v.Live && (v.DurationS == 0 || v.DurationS >= minVideoS) && v.DurationS <= maxVideoS
	})
	return s.storeVideoRecs(ctx, userID, start, gen, used, ranked[:min(len(ranked), videoKeep)])
}

// storeVideoRecs replaces userID's list and stamps refreshed_at with start
// (when the seeds were read). A clear since gen discards the result.
func (s *Service) storeVideoRecs(ctx context.Context, userID int64, start time.Time, gen uint64, seeds []videoSeed, ranked []mixScore) error {
	now := s.now().Unix()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM video_recommendations WHERE user_id=?`, userID); err != nil {
		return err
	}
	for _, r := range ranked {
		sd := seeds[r.seed]
		if _, err := tx.ExecContext(ctx, `INSERT INTO video_recommendations(user_id,video_id,title,channel,channel_id,duration_s,score,reason_kind,reason,created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, userID, r.v.ID, r.v.Title, r.v.Channel, r.v.ChannelID, r.v.DurationS, r.score, sd.kind, sd.label, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO video_rec_users(user_id,refreshed_at) VALUES (?,?)
		ON CONFLICT(user_id) DO UPDATE SET refreshed_at=excluded.refreshed_at`, userID, start.Unix()); err != nil {
		return err
	}
	// Checked after this transaction's first write, so it holds the write
	// lock: a clear that bumped gen earlier is seen here; one that bumps it
	// later runs its DELETE after this commit.
	if s.historyGen(userID) != gen {
		s.log().Info("channels: 视频 history cleared during a refresh; result discarded", "user", userID)
		return nil
	}
	return tx.Commit()
}

// VideoRecs returns the list and, when it is stale (refreshed ≥ 30 min ago and
// history changed since, within today's budget), requests a refresh ("on visit").
func (s *Service) VideoRecs(ctx context.Context, userID int64) (VideoRecs, error) {
	out := VideoRecs{Items: []VideoRec{}}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.video_id, r.title, r.channel, r.channel_id, r.duration_s, r.score, r.reason_kind, r.reason
		FROM video_recommendations r WHERE r.user_id=?
		  AND NOT EXISTS (SELECT 1 FROM video_history h WHERE h.user_id=r.user_id AND h.kind='watch' AND h.item=r.video_id)
		  AND EXISTS (SELECT 1 FROM video_history h WHERE h.user_id=r.user_id AND h.kind=r.reason_kind AND h.label=r.reason)
		ORDER BY r.score DESC, r.rowid`, userID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v VideoRec
		if err := rows.Scan(&v.VideoID, &v.Title, &v.Channel, &v.ChannelID, &v.DurationS, &v.Score, &v.ReasonKind, &v.Reason); err != nil {
			rows.Close()
			return out, err
		}
		v.Thumbnail = videoThumb(v.VideoID)
		out.Items = append(out.Items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	var refreshed, attempted, lastAt sql.NullInt64
	var pending bool
	err = s.DB.QueryRowContext(ctx, `SELECT refreshed_at, attempted_at, COALESCE(`+pendingSQL+`,0) FROM video_rec_users WHERE user_id=?`, userID).
		Scan(&refreshed, &attempted, &pending)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if refreshed.Valid {
		out.RefreshedAt = &refreshed.Int64
	}
	r := s.videoRefresher()
	if pending || s.isRunning(r, userID) {
		out.Refreshing = true
		return out, nil
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT MAX(last_at) FROM video_history WHERE user_id=?`, userID).Scan(&lastAt); err != nil {
		return out, err
	}
	last := max(refreshed.Int64, attempted.Int64) // 0 when never
	if !lastAt.Valid || lastAt.Int64 <= last || s.now().Sub(time.Unix(last, 0)) < staleAfter {
		return out, nil
	}
	switch err := s.request(ctx, r, userID); {
	case err == nil:
		out.Refreshing = true
	case errors.Is(err, ErrTooSoon): // a visit never fails over the budget
	default:
		return out, err
	}
	return out, nil
}
