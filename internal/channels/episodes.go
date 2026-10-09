package channels

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/aaronsuns/lark-server/internal/fileutil"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

type File struct {
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	Bytes    int64   `json:"bytes"`
	Error    string  `json:"error"`
}

type Episode struct {
	VideoID      string  `json:"video_id"`
	ChannelID    string  `json:"channel_id"`
	ChannelTitle string  `json:"channel_title"`
	Title        string  `json:"title"`
	Description  string  `json:"description,omitempty"`
	PublishedAt  int64   `json:"published_at"`
	DurationS    int     `json:"duration_s"`
	Kind         string  `json:"kind"`
	Thumbnail    string  `json:"thumbnail"`
	Audio        *File   `json:"audio"`
	Video        *File   `json:"video"`
	PositionS    float64 `json:"position_s"`
	Played       bool    `json:"played"`
	Kept         bool    `json:"kept"`
}

type ChannelPage struct {
	Channel   ChannelInfo `json:"channel"`
	Following *Settings   `json:"following"`
	Followers int         `json:"followers"`
	Episodes  []Episode   `json:"episodes"`
}

// ChannelHit is a channel search/resolve result with the caller's follow state.
type ChannelHit struct {
	ytdlp.Channel
	Following bool `json:"following"`
}

const (
	pageEpisodes = 50
	feedStale    = 30 * time.Minute // an unfollowed channel's page reads its feed again after this
)

// episodeSelect: one row per episode for the user bound to me.uid (the first argument).
const episodeSelect = `WITH me(uid) AS (SELECT ?)
	SELECT e.video_id, e.channel_id, c.title, e.title, e.description, e.published_at, e.duration_s, e.kind,
	af.status, COALESCE(af.progress,0), COALESCE(af.bytes,0), COALESCE(af.error,''),
	vf.status, COALESCE(vf.progress,0), COALESCE(vf.bytes,0), COALESCE(vf.error,''),
	COALESCE(p.position_s,0), COALESCE(p.played,0),
	EXISTS (SELECT 1 FROM episode_keeps k WHERE k.user_id=me.uid AND k.video_id=e.video_id)
	FROM me, episodes e JOIN channels c ON c.id=e.channel_id
	LEFT JOIN episode_files af ON af.video_id=e.video_id AND af.kind='audio'
	LEFT JOIN episode_files vf ON vf.video_id=e.video_id AND vf.kind='video'
	LEFT JOIN episode_progress p ON p.video_id=e.video_id AND p.user_id=me.uid`

func scanEpisode(r interface{ Scan(...any) error }) (Episode, error) {
	var ep Episode
	var aSt, vSt sql.NullString
	var a, v File
	err := r.Scan(&ep.VideoID, &ep.ChannelID, &ep.ChannelTitle, &ep.Title, &ep.Description, &ep.PublishedAt, &ep.DurationS, &ep.Kind,
		&aSt, &a.Progress, &a.Bytes, &a.Error, &vSt, &v.Progress, &v.Bytes, &v.Error, &ep.PositionS, &ep.Played, &ep.Kept)
	if err != nil {
		return Episode{}, err
	}
	if aSt.Valid {
		a.Status = aSt.String
		ep.Audio = &a
	}
	if vSt.Valid {
		v.Status = vSt.String
		ep.Video = &v
	}
	ep.Thumbnail = "/api/v1/episodes/" + ep.VideoID + "/thumbnail"
	return ep, nil
}

func (s *Service) episodes(ctx context.Context, q string, args ...any) ([]Episode, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Episode{}
	for rows.Next() {
		ep, err := scanEpisode(rows)
		if err != nil {
			return nil, err
		}
		ep.Description = "" // lists stay small; the episode page reads it
		out = append(out, ep)
	}
	return out, rows.Err()
}

// Latest: episodes of the user's channels that are downloaded or on their
// way, of the kinds the user sees, not hidden, newest first; before pages
// by published_at.
func (s *Service) Latest(ctx context.Context, userID, before int64, limit int) ([]Episode, error) {
	return s.LatestPage(ctx, userID, LatestQuery{Before: before, Limit: limit})
}

// LatestQuery shapes the 最新 list: newest first paged by Before, or oldest
// first (Asc) paged by After; Unplayed leaves out the user's played ones;
// Channel narrows it to one channel.
type LatestQuery struct {
	Before, After int64
	Limit         int
	Asc, Unplayed bool
	Channel       string
}

// LatestPage is Latest with the §18.1 sort and filter.
func (s *Service) LatestPage(ctx context.Context, userID int64, q LatestQuery) ([]Episode, error) {
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = pageEpisodes
	}
	order := "e.published_at DESC, e.video_id"
	if q.Asc {
		order = "e.published_at ASC, e.video_id"
	}
	return s.episodes(ctx, episodeSelect+` JOIN channel_follows f ON f.channel_id=e.channel_id AND f.user_id=me.uid
		WHERE af.status IN ('queued','downloading','done') AND COALESCE(p.hidden,0)=0 AND `+visibleFor+`
		  AND (?=0 OR e.published_at<?) AND (?=0 OR e.published_at>?)
		  AND (?=0 OR COALESCE(p.played,0)=0) AND (?='' OR e.channel_id=?)
		ORDER BY `+order+` LIMIT ?`, userID, q.Before, q.Before, q.After, q.After, q.Unplayed, q.Channel, q.Channel, q.Limit)
}

// ChannelGroup is one section of 按频道: a followed channel, its unplayed
// count and its latest episodes.
type ChannelGroup struct {
	Channel  ChannelInfo `json:"channel"`
	Unplayed int         `json:"unplayed"`
	LatestAt *int64      `json:"latest_published_at"`
	Episodes []Episode   `json:"episodes"`
}

// ByChannel groups the user's 最新 by followed channel, the channel with
// the newest episode first, at most per episodes each.
func (s *Service) ByChannel(ctx context.Context, userID int64, per int) ([]ChannelGroup, error) {
	mine, err := s.MyChannels(ctx, userID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(mine, func(i, j int) bool {
		a, b := mine[i].LatestAt, mine[j].LatestAt
		if a == nil || b == nil {
			return a != nil
		}
		return *a > *b
	})
	out := make([]ChannelGroup, 0, len(mine))
	for _, mc := range mine {
		eps, err := s.LatestPage(ctx, userID, LatestQuery{Limit: per, Channel: mc.Channel.ID})
		if err != nil {
			return nil, err
		}
		mc.Channel.Description = "" // sections stay small
		out = append(out, ChannelGroup{Channel: mc.Channel, Unplayed: mc.Unplayed, LatestAt: mc.LatestAt, Episodes: eps})
	}
	return out, nil
}

// Unplayed counts the user's downloaded, unplayed, not hidden episodes (the 频道 badge).
func (s *Service) Unplayed(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `WITH me(uid) AS (SELECT ?) SELECT COUNT(*) FROM me, episodes e
		JOIN channel_follows f ON f.channel_id=e.channel_id AND f.user_id=me.uid
		JOIN episode_files af ON af.video_id=e.video_id AND af.kind='audio' AND af.status='done'
		LEFT JOIN episode_progress p ON p.video_id=e.video_id AND p.user_id=me.uid
		WHERE COALESCE(p.played,0)=0 AND COALESCE(p.hidden,0)=0 AND `+visibleFor, userID).Scan(&n)
	return n, err
}

// Kept: the user's kept episodes, most recently kept first.
func (s *Service) Kept(ctx context.Context, userID int64) ([]Episode, error) {
	return s.episodes(ctx, episodeSelect+` JOIN episode_keeps k2 ON k2.video_id=e.video_id AND k2.user_id=me.uid
		WHERE COALESCE(p.hidden,0)=0 ORDER BY k2.created_at DESC, e.published_at DESC LIMIT 200`, userID)
}

// Episode is one episode for userID, with its description.
func (s *Service) Episode(ctx context.Context, userID int64, videoID string) (Episode, error) {
	if !ytdlp.IsVideoID(videoID) {
		return Episode{}, ErrBadID
	}
	ep, err := scanEpisode(s.DB.QueryRowContext(ctx, episodeSelect+` WHERE e.video_id=?`, userID, videoID))
	if errors.Is(err, sql.ErrNoRows) {
		return Episode{}, ErrNotFound
	}
	return ep, err
}

func (s *Service) channelInfo(ctx context.Context, id string) (ChannelInfo, error) {
	var ci ChannelInfo
	var polled sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT id, title, handle, avatar, description, polled_at, last_error FROM channels WHERE id=?`, id).
		Scan(&ci.ID, &ci.Title, &ci.Handle, &ci.Avatar, &ci.Description, &polled, &ci.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return ChannelInfo{}, ErrNotFound
	}
	if polled.Valid {
		ci.PolledAt = &polled.Int64
	}
	return ci, err
}

// Channel is a stored channel (following one by id needs no yt-dlp when it is known).
func (s *Service) Channel(ctx context.Context, id string) (ytdlp.Channel, error) {
	ci, err := s.channelInfo(ctx, id)
	if err != nil {
		return ytdlp.Channel{}, err
	}
	return ytdlp.Channel{ID: ci.ID, Title: ci.Title, Handle: ci.Handle, Avatar: ci.Avatar, Description: ci.Description}, nil
}

// ChannelPage: the channel, the user's follow, and its newest episodes. A
// channel nobody follows has its feed read here (at most every 30 min), so
// a suggested channel shows its videos before anyone follows it.
func (s *Service) ChannelPage(ctx context.Context, userID int64, channelID string) (ChannelPage, error) {
	if !ytdlp.IsChannelID(channelID) {
		return ChannelPage{}, ErrBadID
	}
	ci, err := s.channelInfo(ctx, channelID)
	if err != nil {
		return ChannelPage{}, err
	}
	page := ChannelPage{Channel: ci}
	if mc, err := s.myChannel(ctx, userID, channelID); err == nil {
		page.Following = &mc.Settings
	} else if !errors.Is(err, ErrNotFollowing) {
		return ChannelPage{}, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_follows WHERE channel_id=?`, channelID).Scan(&page.Followers); err != nil {
		return ChannelPage{}, err
	}
	if page.Followers == 0 && (ci.PolledAt == nil || s.now().Sub(time.Unix(*ci.PolledAt, 0)) > feedStale) {
		if feed, err := s.Feeds.Fetch(ctx, channelID); err != nil {
			s.log().Warn("channels: read feed for page", "channel", channelID, "err", err)
		} else if err := s.storeFeed(ctx, channelID, feed); err == nil {
			now := s.now().Unix()
			s.DB.ExecContext(ctx, `UPDATE channels SET polled_at=?, title=CASE WHEN title='' THEN ? ELSE title END WHERE id=?`, now, feed.Title, channelID)
			page.Channel.PolledAt = &now
		}
	}
	page.Episodes, err = s.episodes(ctx, episodeSelect+` WHERE e.channel_id=? AND e.kind NOT IN ('unavailable','live','upcoming')
		AND COALESCE(p.hidden,0)=0 ORDER BY e.published_at DESC LIMIT ?`, userID, channelID, pageEpisodes)
	return page, err
}

func (s *Service) episodeExists(ctx context.Context, videoID string) error {
	if !ytdlp.IsVideoID(videoID) {
		return ErrBadID
	}
	var ok bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM episodes WHERE video_id=?)`, videoID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// SetProgress stores userID's position (seconds); played, when given, sets
// or clears "played" (a plain position report leaves it as it was).
func (s *Service) SetProgress(ctx context.Context, userID int64, videoID string, positionS float64, played *bool) error {
	if err := s.episodeExists(ctx, videoID); err != nil {
		return err
	}
	if math.IsNaN(positionS) || positionS < 0 {
		positionS = 0
	}
	positionS = min(positionS, 1e6)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO episode_progress(user_id,video_id,position_s,played,updated_at) VALUES (?,?,?,COALESCE(?,0),?)
		ON CONFLICT(user_id,video_id) DO UPDATE SET position_s=excluded.position_s, played=COALESCE(?,played), updated_at=excluded.updated_at`,
		userID, videoID, positionS, played, s.now().Unix(), played)
	return err
}

// SetKeep keeps (or stops keeping) an episode for userID. Keeping one whose
// audio isn't on disk (never fetched, or already expired) fetches it.
func (s *Service) SetKeep(ctx context.Context, userID int64, videoID string, on bool) error {
	if err := s.episodeExists(ctx, videoID); err != nil {
		return err
	}
	now := s.now().Unix()
	if !on {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM episode_keeps WHERE user_id=? AND video_id=?`, userID, videoID)
		s.kickSweep()
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO episode_keeps(user_id,video_id,created_at) VALUES (?,?,?)`, userID, videoID, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO episode_files(video_id,kind,status,created_at,updated_at)
		SELECT ?,'audio','queued',?,? WHERE NOT EXISTS (SELECT 1 FROM episodes WHERE video_id=? AND kind='unavailable')
		ON CONFLICT(video_id,kind) DO UPDATE SET status='queued', attempts=0, timeouts=0, transients=0, next_attempt_at=0, error='', updated_at=excluded.updated_at
		WHERE episode_files.status='expired'`, videoID, now, now, videoID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.kickWork()
	return nil
}

// SetHidden hides an episode for userID only ("delete for me").
func (s *Service) SetHidden(ctx context.Context, userID int64, videoID string, on bool) error {
	if err := s.episodeExists(ctx, videoID); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO episode_progress(user_id,video_id,hidden,updated_at) VALUES (?,?,?,?)
		ON CONFLICT(user_id,video_id) DO UPDATE SET hidden=excluded.hidden, updated_at=excluded.updated_at`,
		userID, videoID, on, s.now().Unix())
	return err
}

// MediaFile is the absolute path of an episode's downloaded audio or video.
func (s *Service) MediaFile(ctx context.Context, videoID, kind string) (string, error) {
	var status, rel string
	err := s.DB.QueryRowContext(ctx, `SELECT status, path FROM episode_files WHERE video_id=? AND kind=?`, videoID, kind).Scan(&status, &rel)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", ErrNotFound
	case err != nil:
		return "", err
	case status == "done":
		// Confined to channels.root: a stored path never leads outside it.
		root, err := filepath.Abs(s.Root)
		if err != nil {
			return "", err
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		if _, ok := fileutil.RelInside(root, p); !ok {
			return "", ErrNotFound
		}
		return p, nil
	case status == "expired":
		return "", ErrExpired
	}
	return "", ErrNotReady
}

// ThumbnailPath is the episode's local thumbnail, when it is on disk.
func (s *Service) ThumbnailPath(ctx context.Context, videoID string) (string, bool) {
	if !ytdlp.IsVideoID(videoID) {
		return "", false
	}
	var ch string
	if err := s.DB.QueryRowContext(ctx, `SELECT channel_id FROM episodes WHERE video_id=?`, videoID).Scan(&ch); err != nil {
		return "", false
	}
	p := filepath.Join(s.Root, ch, videoID+".jpg")
	if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	return p, true
}

// MarkFollowing tags search results the user already follows.
func (s *Service) MarkFollowing(ctx context.Context, userID int64, chans []ytdlp.Channel) []ChannelHit {
	followed := map[string]bool{}
	if rows, err := s.DB.QueryContext(ctx, `SELECT channel_id FROM channel_follows WHERE user_id=?`, userID); err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				followed[id] = true
			}
		}
		rows.Close()
	}
	out := make([]ChannelHit, 0, len(chans))
	for _, c := range chans {
		out = append(out, ChannelHit{Channel: c, Following: followed[c.ID]})
	}
	return out
}
