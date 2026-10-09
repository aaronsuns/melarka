package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/aaronsuns/lark-server/internal/recommend"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

const (
	discoverSeeds   = 10 // Mixes per refresh
	discoverMix     = 25
	keepChannels    = 15
	keepVideos      = 30
	mixesPerDay     = 50 // on-demand Mix calls per user per day (failed ones count; the nightly run is exempt)
	minSuggestedS   = 60
	maxSuggestedS   = 4 * 3600
	discoveryDayKey = "channels.discovery_day"
)

// ErrTooSoon: an on-demand refresh past the user's daily Mix-call budget.
var ErrTooSoon = errors.New("channels: daily suggestion refresh limit reached")

type SuggestedChannel struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Avatar        string  `json:"avatar"`
	Score         float64 `json:"score"`
	SampleVideoID string  `json:"sample_video_id"`
}

type SuggestedVideo struct {
	VideoID   string  `json:"video_id"`
	Title     string  `json:"title"`
	Channel   string  `json:"channel"`
	ChannelID string  `json:"channel_id"`
	DurationS int     `json:"duration_s"`
	Thumbnail string  `json:"thumbnail"`
	URL       string  `json:"url"`
	Score     float64 `json:"score"`
}

type Suggestions struct {
	Channels    []SuggestedChannel `json:"channels"`
	Videos      []SuggestedVideo   `json:"videos"`
	RefreshedAt *int64             `json:"refreshed_at"`
	Refreshing  bool               `json:"refreshing"`
}

type discoverySeed struct{ video, channel string }

// discoverySeeds: the newest episode of each followed channel (most recent
// channels first), then the second-newest, up to discoverSeeds.
func (s *Service) discoverySeeds(ctx context.Context, userID int64) ([]discoverySeed, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT video_id, channel_id FROM (
		SELECT e.video_id, e.channel_id, e.published_at,
		       ROW_NUMBER() OVER (PARTITION BY e.channel_id ORDER BY e.published_at DESC) AS rn
		FROM episodes e JOIN channel_follows f ON f.channel_id=e.channel_id AND f.user_id=?
		WHERE e.kind IN ('video','replay','unknown'))
		WHERE rn<=2 ORDER BY rn, published_at DESC LIMIT ?`, userID, discoverSeeds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []discoverySeed
	for rows.Next() {
		var sd discoverySeed
		if err := rows.Scan(&sd.video, &sd.channel); err != nil {
			return nil, err
		}
		out = append(out, sd)
	}
	return out, rows.Err()
}

func (s *Service) idSet(ctx context.Context, q string, args ...any) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		set[id] = true
	}
	return set, rows.Err()
}

// suggestable: a single, finished, normal-length video (not a stream or a compilation).
func suggestable(v ytdlp.Video) bool {
	return ytdlp.IsVideoID(v.ID) && !v.Live && v.DurationS >= minSuggestedS && v.DurationS <= maxSuggestedS && !recommend.BlockedTitle(v.Title)
}

// Discover rebuilds userID's suggestions from the YouTube Mixes of their
// followed channels' newest episodes: a channel scores one point per
// followed channel whose episode's Mix contains it (followed, dismissed and
// the seed's own channel excluded); a video one point per Mix it appears in
// (not of a followed or dismissed channel, not dismissed, not already in
// Channels or the music downloads, not a stream, short clip or compilation).
// When every Mix failed it returns an error and the previous list stays.
func (s *Service) Discover(ctx context.Context, userID int64) error {
	return s.discover(ctx, userID, true)
}

// discover is Discover; budgeted says whether its Mix calls count against
// the user's daily on-demand budget (the nightly run's do not).
func (s *Service) discover(ctx context.Context, userID int64, budgeted bool) error {
	seeds, err := s.discoverySeeds(ctx, userID)
	if err != nil {
		return err
	}
	followed, err := s.idSet(ctx, `SELECT channel_id FROM channel_follows WHERE user_id=?`, userID)
	if err != nil {
		return err
	}
	dismissedCh, err := s.idSet(ctx, `SELECT item_id FROM channel_dismissals WHERE user_id=? AND kind='channel'`, userID)
	if err != nil {
		return err
	}
	excluded, err := s.idSet(ctx, `SELECT item_id FROM channel_dismissals WHERE user_id=? AND kind='video'
		UNION SELECT video_id FROM downloads WHERE status IN ('queued','downloading','done')
		  AND (user_id=? OR id IN (SELECT download_id FROM download_requests WHERE user_id=?))
		UNION SELECT e.video_id FROM episodes e JOIN channel_follows f ON f.channel_id=e.channel_id AND f.user_id=?`,
		userID, userID, userID, userID)
	if err != nil {
		return err
	}
	type chanAcc struct {
		s     SuggestedChannel
		from  map[string]bool
		count int
		pos   int
	}
	chans := map[string]*chanAcc{}
	var mixes []seedMix
	tried, failed := 0, 0
	var lastErr error
	for _, sd := range seeds {
		var mix []ytdlp.Video
		err := s.low(ctx, func(ctx context.Context) error {
			var err error
			mix, err = s.YT.Mix(ctx, sd.video, discoverMix)
			return err
		})
		tried++
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failed, lastErr = failed+1, err
			s.log().Warn("channels: discovery mix", "video", sd.video, "err", err)
			continue
		}
		mixes = append(mixes, seedMix{seedVideo: sd.video, weight: 1, videos: mix})
		for pos, v := range mix {
			if v.ID == sd.video || followed[v.ChannelID] || dismissedCh[v.ChannelID] {
				continue
			}
			if v.ChannelID != "" && v.ChannelID != sd.channel {
				a := chans[v.ChannelID]
				if a == nil {
					a = &chanAcc{s: SuggestedChannel{ID: v.ChannelID, Title: v.Channel, SampleVideoID: v.ID}, from: map[string]bool{}, pos: pos}
					chans[v.ChannelID] = a
				}
				a.from[sd.channel] = true
				a.count++
				a.pos = min(a.pos, pos)
			}
		}
	}
	if budgeted && tried > 0 {
		if err := s.addMixes(ctx, s.discoveryRefresher(), userID, tried); err != nil {
			s.log().Warn("channels: discovery budget", "err", err)
		}
	}
	if tried > 0 && failed == tried {
		return fmt.Errorf("channels: every discovery Mix failed: %w", lastErr)
	}
	chList := make([]*chanAcc, 0, len(chans))
	for _, a := range chans {
		a.s.Score = float64(len(a.from))
		chList = append(chList, a)
	}
	sort.Slice(chList, func(i, j int) bool {
		a, b := chList[i], chList[j]
		if a.s.Score != b.s.Score {
			return a.s.Score > b.s.Score
		}
		if a.count != b.count {
			return a.count > b.count
		}
		if a.pos != b.pos {
			return a.pos < b.pos
		}
		return a.s.ID < b.s.ID
	})
	vList := rankMixVideos(mixes, func(v ytdlp.Video) bool {
		return !excluded[v.ID] && suggestable(v) && !followed[v.ChannelID] && !dismissedCh[v.ChannelID]
	})
	now := s.now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM channel_suggestions WHERE user_id=?`, `DELETE FROM video_suggestions WHERE user_id=?`} {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return err
		}
	}
	for _, a := range chList[:min(len(chList), keepChannels)] {
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_suggestions(user_id,channel_id,title,score,sample_video_id,created_at) VALUES (?,?,?,?,?,?)`,
			userID, a.s.ID, a.s.Title, a.s.Score, a.s.SampleVideoID, now.Unix()); err != nil {
			return err
		}
	}
	for _, a := range vList[:min(len(vList), keepVideos)] {
		if _, err := tx.ExecContext(ctx, `INSERT INTO video_suggestions(user_id,video_id,title,channel,channel_id,duration_s,score,created_at) VALUES (?,?,?,?,?,?,?,?)`,
			userID, a.v.ID, a.v.Title, a.v.Channel, a.v.ChannelID, a.v.DurationS, a.score, now.Unix()); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO discovery_users(user_id,refreshed_at) VALUES (?,?)
		ON CONFLICT(user_id) DO UPDATE SET refreshed_at=excluded.refreshed_at`, userID, now.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// Suggestions is userID's current list, minus channels followed and videos
// downloaded since it was built.
func (s *Service) Suggestions(ctx context.Context, userID int64) (Suggestions, error) {
	out := Suggestions{Channels: []SuggestedChannel{}, Videos: []SuggestedVideo{}}
	rows, err := s.DB.QueryContext(ctx, `SELECT cs.channel_id, COALESCE(NULLIF(c.title,''), cs.title), COALESCE(c.avatar,''), cs.score, cs.sample_video_id
		FROM channel_suggestions cs LEFT JOIN channels c ON c.id=cs.channel_id
		WHERE cs.user_id=? AND NOT EXISTS (SELECT 1 FROM channel_follows f WHERE f.user_id=cs.user_id AND f.channel_id=cs.channel_id)
		ORDER BY cs.score DESC, cs.rowid`, userID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var c SuggestedChannel
		if err := rows.Scan(&c.ID, &c.Title, &c.Avatar, &c.Score, &c.SampleVideoID); err != nil {
			rows.Close()
			return out, err
		}
		out.Channels = append(out.Channels, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.DB.QueryContext(ctx, `SELECT vs.video_id, vs.title, vs.channel, vs.channel_id, vs.duration_s, vs.score
		FROM video_suggestions vs WHERE vs.user_id=?
		  AND NOT EXISTS (SELECT 1 FROM downloads d WHERE d.video_id=vs.video_id AND d.status='done'
		      AND (d.user_id=vs.user_id OR d.id IN (SELECT download_id FROM download_requests r WHERE r.user_id=vs.user_id)))
		  AND NOT EXISTS (SELECT 1 FROM channel_dismissals x WHERE x.user_id=vs.user_id AND x.kind='channel' AND x.item_id=vs.channel_id)
		  AND NOT EXISTS (SELECT 1 FROM channel_follows f WHERE f.user_id=vs.user_id AND f.channel_id=vs.channel_id)
		ORDER BY vs.score DESC, vs.rowid`, userID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v SuggestedVideo
		if err := rows.Scan(&v.VideoID, &v.Title, &v.Channel, &v.ChannelID, &v.DurationS, &v.Score); err != nil {
			rows.Close()
			return out, err
		}
		v.Thumbnail, v.URL = ytdlp.ThumbnailURL(v.VideoID), ytdlp.WatchURL(v.VideoID)
		out.Videos = append(out.Videos, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	var refreshed sql.NullInt64
	var pending bool
	err = s.DB.QueryRowContext(ctx, `SELECT refreshed_at, COALESCE(`+pendingSQL+`,0) FROM discovery_users WHERE user_id=?`, userID).Scan(&refreshed, &pending)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if refreshed.Valid {
		out.RefreshedAt = &refreshed.Int64
	}
	out.Refreshing = pending || s.isRunning(s.discoveryRefresher(), userID)
	return out, nil
}

// DismissSuggestion hides a suggested channel or video from userID for good.
func (s *Service) DismissSuggestion(ctx context.Context, userID int64, kind, id string) error {
	switch {
	case kind == "channel" && ytdlp.IsChannelID(id), kind == "video" && ytdlp.IsVideoID(id):
	default:
		return ErrBadID
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO channel_dismissals(user_id,kind,item_id,created_at) VALUES (?,?,?,?)`,
		userID, kind, id, s.now().Unix()); err != nil {
		return err
	}
	q := []string{`DELETE FROM channel_suggestions WHERE user_id=? AND channel_id=?`, `DELETE FROM video_suggestions WHERE user_id=? AND channel_id=?`}
	if kind == "video" {
		q = []string{`DELETE FROM video_suggestions WHERE user_id=? AND video_id=?`}
	}
	for _, stmt := range q {
		if _, err := tx.ExecContext(ctx, stmt, userID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
