package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aaronsuns/lark-server/internal/personal"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// 视频 history: the videos a user opened and what they searched
// for, kept 90 days, feeding 为你推荐. Titles, channels and queries are
// untrusted text: stored and returned as data, never interpreted.

const (
	HistoryDays     = 90
	maxWatchRows    = 500 // per user
	maxSearchRows   = 200 // per user
	historyWatches  = 50  // returned by VideoHistory
	historySearches = 20
	maxVideoQuery   = 100 // runes
	mixFresh        = 24 * time.Hour
	mixKeep         = 7 * 24 * time.Hour
	videoPruneDay   = "video.prune_day"
	maxMixEntries   = 50  // per cached Mix
	maxText         = 300 // runes of a stored title or channel name
)

type WatchedVideo struct {
	VideoID   string `json:"video_id"`
	Title     string `json:"title"`
	Channel   string `json:"channel"`
	ChannelID string `json:"channel_id"`
	DurationS int    `json:"duration_s"`
	Thumbnail string `json:"thumbnail"` // /api/v1/videos/<id>/thumbnail
	LastAt    int64  `json:"last_at"`
}

type VideoHistory struct {
	Watches  []WatchedVideo `json:"watches"`  // newest first, ≤ 50
	Searches []string       `json:"searches"` // newest first, ≤ 20
}

// historyGen is userID's clear generation (see ClearVideoHistory).
func (s *Service) historyGen(userID int64) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.videoGen[userID]
}

// maxMixRows is how many video_mixes rows are kept, newest first (a var for tests).
var maxMixRows = 2000

// videoThumb is Lark's thumbnail proxy for a (validated) video id.
func videoThumb(id string) string { return "/api/v1/videos/" + id + "/thumbnail" }

// RecordWatch notes that userID opened v (again: times+1, newest).
func (s *Service) RecordWatch(ctx context.Context, userID int64, v ytdlp.Video) error {
	if !ytdlp.IsVideoID(v.ID) {
		return ErrBadID
	}
	v = boundVideo(v)
	return s.recordHistory(ctx, userID, "watch", v.ID, v.Title, v.Channel, v.ChannelID, v.DurationS, v.ID, maxWatchRows)
}

// boundVideo limits the untrusted text of v before it is stored: title and
// channel at most maxText runes, a channel id only when well-formed.
func boundVideo(v ytdlp.Video) ytdlp.Video {
	v.Title, v.Channel = clip(v.Title, maxText), clip(v.Channel, maxText)
	if !ytdlp.IsChannelID(v.ChannelID) {
		v.ChannelID = ""
	}
	return v
}

// clip cuts s to at most n runes.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// RecordVideoSearch notes that userID searched for query; firstVideoID (the
// first result, when a valid id) is the video whose Mix the search seeds.
func (s *Service) RecordVideoSearch(ctx context.Context, userID int64, query, firstVideoID string) error {
	q := personal.NormQuery(query)
	if n := utf8.RuneCountInString(q); n == 0 || n > maxVideoQuery {
		return ErrBadID
	}
	seed := ""
	if ytdlp.IsVideoID(firstVideoID) {
		seed = firstVideoID
	}
	return s.recordHistory(ctx, userID, "search", strings.ToLower(q), q, "", "", 0, seed, maxSearchRows)
}

func (s *Service) recordHistory(ctx context.Context, userID int64, kind, item, label, channel, channelID string, durationS int, seed string, keep int) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO video_history(user_id,kind,item,label,channel,channel_id,duration_s,seed_video_id,times,last_at)
		VALUES (?,?,?,?,?,?,?,?,1,?)
		ON CONFLICT(user_id,kind,item) DO UPDATE SET times=times+1, last_at=excluded.last_at, label=excluded.label,
		  channel=excluded.channel, channel_id=excluded.channel_id, duration_s=excluded.duration_s,
		  seed_video_id=CASE WHEN excluded.seed_video_id!='' THEN excluded.seed_video_id ELSE seed_video_id END`,
		userID, kind, item, label, channel, channelID, max(durationS, 0), seed, s.now().Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM video_history WHERE user_id=?1 AND kind=?2 AND item NOT IN (
		SELECT item FROM video_history WHERE user_id=?1 AND kind=?2 ORDER BY last_at DESC, rowid DESC LIMIT ?3)`,
		userID, kind, keep); err != nil {
		return err
	}
	return tx.Commit()
}

// VideoHistory is userID's recent watches and searches, newest first, never nil.
func (s *Service) VideoHistory(ctx context.Context, userID int64) (VideoHistory, error) {
	out := VideoHistory{Watches: []WatchedVideo{}, Searches: []string{}}
	since := s.now().Add(-HistoryDays * 24 * time.Hour).Unix()
	rows, err := s.DB.QueryContext(ctx, `SELECT item, label, channel, channel_id, duration_s, last_at FROM video_history
		WHERE user_id=? AND kind='watch' AND last_at>=? ORDER BY last_at DESC, rowid DESC LIMIT ?`, userID, since, historyWatches)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var w WatchedVideo
		if err := rows.Scan(&w.VideoID, &w.Title, &w.Channel, &w.ChannelID, &w.DurationS, &w.LastAt); err != nil {
			rows.Close()
			return out, err
		}
		w.Thumbnail = videoThumb(w.VideoID)
		out.Watches = append(out.Watches, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.DB.QueryContext(ctx, `SELECT label FROM video_history
		WHERE user_id=? AND kind='search' AND last_at>=? ORDER BY last_at DESC, rowid DESC LIMIT ?`, userID, since, historySearches)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var q string
		if err := rows.Scan(&q); err != nil {
			return out, err
		}
		out.Searches = append(out.Searches, q)
	}
	return out, rows.Err()
}

// ClearVideoHistory forgets userID's watches and searches, and the 为你推荐
// built from them. Today's Mix budget stays spent. A refresh already running
// for userID is discarded (its generation is now old).
func (s *Service) ClearVideoHistory(ctx context.Context, userID int64) error {
	s.mu.Lock()
	if s.videoGen == nil {
		s.videoGen = map[int64]uint64{}
	}
	s.videoGen[userID]++
	s.mu.Unlock()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM video_history WHERE user_id=?`,
		`DELETE FROM video_recommendations WHERE user_id=?`,
		`UPDATE video_rec_users SET requested_at=NULL WHERE user_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CachedMix returns videoID's Mix from the DB cache when fetched within 24 h, else
// calls fetch (the caller picks the priority path) and stores its non-live, valid entries.
// When fetch fails and an older row exists, the older entries are returned.
// A Mix with fewer than minMixOthers entries besides videoID gets the seed
// channel's latest uploads appended (withChannelUploads), whichever way it
// was obtained; what that adds is cached with the row.
func (s *Service) CachedMix(ctx context.Context, videoID string, fetch func(ctx context.Context) ([]ytdlp.Video, error)) ([]ytdlp.Video, error) {
	if !ytdlp.IsVideoID(videoID) {
		return nil, ErrBadID
	}
	var raw string
	var fetchedAt int64
	err := s.DB.QueryRowContext(ctx, `SELECT entries, fetched_at FROM video_mixes WHERE video_id=?`, videoID).Scan(&raw, &fetchedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var cached []ytdlp.Video
	have := err == nil && json.Unmarshal([]byte(raw), &cached) == nil
	if have && s.now().Sub(time.Unix(fetchedAt, 0)) < mixFresh {
		return s.withChannelUploads(ctx, videoID, cached, fetchedAt), nil
	}
	vids, err := fetch(ctx)
	if err != nil {
		if have && ctx.Err() == nil {
			s.log().Warn("channels: mix fetch failed, serving the cached one", "video", videoID, "err", err)
			return s.withChannelUploads(ctx, videoID, cached, fetchedAt), nil
		}
		return nil, err
	}
	vids = mixEntries(vids)
	at := s.now().Unix()
	if out, _, read := s.channelUploads(ctx, videoID, vids); read {
		vids = out
		s.markFilled(videoID, at)
	}
	if err := s.putMix(ctx, videoID, vids, at); err != nil {
		s.log().Warn("channels: mix cache", "video", videoID, "err", err)
	}
	return vids, nil
}

// minMixOthers: a Mix with fewer entries than this besides its seed is
// thin (a talk or finance video's Mix often holds only the video itself).
const minMixOthers = 5

// withChannelUploads is a cached Mix with the thin-Mix fallback applied
// (once per cached row: a channel with few uploads stays thin, and its feed
// is not read on every view); anything added is stored back with the row's
// own fetched time (the Mix is still refetched on schedule).
func (s *Service) withChannelUploads(ctx context.Context, videoID string, vids []ytdlp.Video, fetchedAt int64) []ytdlp.Video {
	s.mu.Lock()
	done := s.mixFilled[videoID] == fetchedAt
	s.mu.Unlock()
	if done {
		return vids
	}
	out, added, read := s.channelUploads(ctx, videoID, vids)
	if read {
		s.markFilled(videoID, fetchedAt)
	}
	if !added {
		return vids
	}
	if err := s.putMix(ctx, videoID, out, fetchedAt); err != nil {
		s.log().Warn("channels: mix cache", "video", videoID, "err", err)
	}
	return out
}

// channelUploads appends to a thin Mix (vids, already through mixEntries)
// the seed channel's latest uploads from its RSS feed — no yt-dlp: not the
// seed, not a Short, not one already listed. Their duration is unknown (0).
// The seed's channel is the one a watch of it recorded, else the one its
// own Mix entry names; with neither, or a feed that fails, vids stay as
// they are. added reports whether anything was appended, read whether
// the Mix is settled: not thin, no channel known, or its feed read (a feed
// that failed is tried again on the next read).
func (s *Service) channelUploads(ctx context.Context, videoID string, vids []ytdlp.Video) (out []ytdlp.Video, added, read bool) {
	if s.Feeds == nil {
		return vids, false, true
	}
	seen := map[string]bool{videoID: true}
	others := 0
	for _, v := range vids {
		if v.ID != videoID {
			others++
		}
		seen[v.ID] = true
	}
	if others >= minMixOthers {
		return vids, false, true
	}
	channelID := s.seedChannel(ctx, videoID, vids)
	if channelID == "" {
		return vids, false, true
	}
	feed, err := s.Feeds.Fetch(ctx, channelID)
	if err != nil {
		if ctx.Err() == nil {
			s.log().Warn("channels: thin mix, channel feed", "video", videoID, "channel", channelID, "err", err)
		}
		return vids, false, false
	}
	out = append([]ytdlp.Video{}, vids...)
	for _, fe := range feed.Entries {
		if fe.Short || seen[fe.VideoID] || !ytdlp.IsVideoID(fe.VideoID) {
			continue
		}
		seen[fe.VideoID] = true
		out = append(out, ytdlp.Video{ID: fe.VideoID, Title: fe.Title, Channel: feed.Title, ChannelID: channelID,
			URL: ytdlp.WatchURL(fe.VideoID), Thumbnail: ytdlp.ThumbnailURL(fe.VideoID)})
		added = true
	}
	if !added {
		return vids, false, true
	}
	return mixEntries(out), true, true
}

// markFilled notes that videoID's Mix row fetched at fetchedAt is settled.
func (s *Service) markFilled(videoID string, fetchedAt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mixFilled == nil {
		s.mixFilled = map[string]int64{}
	}
	s.mixFilled[videoID] = fetchedAt
}

// seedChannel is videoID's channel: from a recorded watch of it, else from
// its own entry in its Mix; "" when neither knows.
func (s *Service) seedChannel(ctx context.Context, videoID string, vids []ytdlp.Video) string {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT channel_id FROM video_history WHERE kind='watch' AND item=? AND channel_id!=''
		ORDER BY last_at DESC, rowid DESC LIMIT 1`, videoID).Scan(&id)
	if err == nil && ytdlp.IsChannelID(id) {
		return id
	}
	for _, v := range vids {
		if v.ID == videoID && ytdlp.IsChannelID(v.ChannelID) {
			return v.ChannelID
		}
	}
	return ""
}

// mixEntries keeps the entries worth caching: valid ids, not live, text
// bounded, at most maxMixEntries.
func mixEntries(vids []ytdlp.Video) []ytdlp.Video {
	out := make([]ytdlp.Video, 0, min(len(vids), maxMixEntries))
	for _, v := range vids {
		if len(out) == maxMixEntries {
			break
		}
		if ytdlp.IsVideoID(v.ID) && !v.Live {
			out = append(out, boundVideo(v))
		}
	}
	return out
}

// putMix stores videoID's Mix (already through mixEntries) as fetched at
// fetchedAt, and keeps only the newest maxMixRows Mixes.
func (s *Service) putMix(ctx context.Context, videoID string, vids []ytdlp.Video, fetchedAt int64) error {
	if vids == nil {
		vids = []ytdlp.Video{}
	}
	b, err := json.Marshal(vids)
	if err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO video_mixes(video_id,entries,fetched_at) VALUES (?,?,?)
		ON CONFLICT(video_id) DO UPDATE SET entries=excluded.entries, fetched_at=excluded.fetched_at`, videoID, string(b), fetchedAt); err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM video_mixes WHERE video_id NOT IN (
		SELECT video_id FROM video_mixes ORDER BY fetched_at DESC, rowid DESC LIMIT ?)`, maxMixRows)
	return err
}

// pruneVideo drops history and 为你推荐 older than HistoryDays, the
// 为你推荐 of users with no history left (their reasons quote it), and Mix
// cache rows older than a week; it says how many rows went.
func (s *Service) pruneVideo(ctx context.Context) (int64, error) {
	now := s.now()
	old := now.Add(-HistoryDays * 24 * time.Hour).Unix()
	var n int64
	for _, st := range []struct {
		q    string
		args []any
	}{
		{`DELETE FROM video_history WHERE last_at<?`, []any{old}},
		{`DELETE FROM video_recommendations WHERE created_at<?`, []any{old}},
		{`DELETE FROM video_recommendations WHERE user_id NOT IN (SELECT user_id FROM video_history)`, nil},
		{`DELETE FROM video_mixes WHERE fetched_at<?`, []any{now.Add(-mixKeep).Unix()}},
	} {
		res, err := s.DB.ExecContext(ctx, st.q, st.args...)
		if err != nil {
			return n, err
		}
		k, _ := res.RowsAffected()
		n += k
	}
	s.mu.Lock()
	for id, at := range s.mixFilled {
		if at < now.Add(-mixKeep).Unix() {
			delete(s.mixFilled, id)
		}
	}
	s.mu.Unlock()
	return n, nil
}

// dailyPrune runs pruneVideo once per day (app_state video.prune_day).
func (s *Service) dailyPrune(ctx context.Context, now time.Time) {
	today := now.Format(time.DateOnly)
	var done string
	if err := s.DB.QueryRowContext(ctx, `SELECT value FROM app_state WHERE key=?`, videoPruneDay).Scan(&done); err != nil && !errors.Is(err, sql.ErrNoRows) {
		if ctx.Err() == nil {
			s.log().Warn("channels: video prune day", "err", err)
		}
		return
	}
	if done == today {
		return
	}
	n, err := s.pruneVideo(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log().Warn("channels: video prune", "err", err)
		}
		return
	}
	if n > 0 {
		s.log().Info("channels: pruned 视频 history and Mix cache", "rows", n)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO app_state(key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, videoPruneDay, today); err != nil && ctx.Err() == nil {
		s.log().Warn("channels: video prune day", "err", err)
	}
}
