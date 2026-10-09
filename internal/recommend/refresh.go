package recommend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aaronsuns/lark-server/internal/lastfm"
	"github.com/aaronsuns/lark-server/internal/lyrics"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// Refresh rebuilds userID's recommendations. A user without seeds gets an
// empty list, unless they already have one: an empty result never replaces it. When every yt-dlp call failed (YouTube or yt-dlp broken) it
// returns an error and leaves the previous list in place.
func (s *Service) Refresh(ctx context.Context, userID int64) error {
	seeds, err := s.Seeds(ctx, userID)
	if err != nil {
		return err
	}
	var sugg []suggestion
	seedVideos := map[string]bool{}
	tried, failed := 0, 0
	var lastErr error
	lastfmOK := s.LastFM != nil
	for i, sd := range seeds {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		vid, err := s.seedVideo(ctx, userID, sd)
		if err != nil {
			tried, failed, lastErr = tried+1, failed+1, err
			s.log().Warn("recommendations: seed video", "track", sd.TrackID, "err", err)
		}
		if vid != "" {
			seedVideos[vid] = true
			var mix []ytdlp.Video
			err := s.low(ctx, func(ctx context.Context) error {
				var err error
				mix, err = s.YT.Mix(ctx, vid, mixEntries)
				return err
			})
			tried++
			if err != nil {
				failed, lastErr = failed+1, err
				s.log().Warn("recommendations: mix", "video", vid, "err", err)
			}
			for pos, v := range mix {
				sugg = append(sugg, suggestion{seed: i, video: v, pos: pos})
			}
		}
		if lastfmOK {
			got, err := s.lastfmSuggestions(ctx, userID, i, sd)
			if errors.Is(err, lastfm.ErrUnavailable) {
				lastfmOK = false // down for this run: Mixes only
				s.log().Warn("recommendations: last.fm unavailable", "err", err)
			} else if err != nil {
				tried, failed, lastErr = tried+1, failed+1, err
				s.log().Warn("recommendations: last.fm", "track", sd.TrackID, "err", err)
			} else if len(got) > 0 {
				tried++
			}
			sugg = append(sugg, got...)
		}
	}
	if tried > 0 && failed == tried {
		return fmt.Errorf("recommendations: every lookup failed: %w", lastErr)
	}
	excl, err := s.exclusions(ctx, userID)
	if err != nil {
		return err
	}
	for v := range seedVideos {
		excl.videos[v] = true
	}
	var kept []suggestion
	for _, sg := range sugg {
		if playable(sg.video) && !excl.videos[sg.video.ID] && !excl.inLibrary(sg.video) {
			kept = append(kept, sg)
		}
	}
	top := rank(seeds, kept)
	if len(top) > keepTop {
		top = top[:keepTop]
	}
	if len(top) == 0 {
		// Nothing new to suggest: never wipe a list the user still has; record
		// the try (so it no longer shows as refreshing) and keep it.
		var has bool
		if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM recommendations WHERE user_id=?)`, userID).Scan(&has); err != nil {
			return err
		}
		if has {
			_, err := s.DB.ExecContext(ctx, `INSERT INTO recommendation_users(user_id,attempted_at) VALUES (?,?)
				ON CONFLICT(user_id) DO UPDATE SET attempted_at=excluded.attempted_at`, userID, s.now().Unix())
			return err
		}
	}
	return s.store(ctx, userID, seeds, top)
}

// lastfmSuggestions: Last.fm's top similar tracks for the seed (or, when it
// knows none, a song of each of the most similar artists), each resolved to
// YouTube by a cached search. Errors from Last.fm itself are returned; a
// failed YouTube search only drops that one suggestion.
func (s *Service) lastfmSuggestions(ctx context.Context, userID int64, seed int, sd Seed) ([]suggestion, error) {
	if sd.Artist == "" || sd.Title == "" {
		return nil, nil
	}
	var queries []string
	tracks, err := s.LastFM.TrackSimilar(ctx, sd.Artist, sd.Title, lastfmPerSeed)
	if err != nil && !errors.Is(err, lastfm.ErrNotFound) {
		return nil, err
	}
	for _, t := range tracks[:min(len(tracks), lastfmPerSeed)] {
		queries = append(queries, searchQuery(t.Title, t.Artist))
	}
	if len(queries) == 0 {
		artists, err := s.LastFM.ArtistSimilar(ctx, sd.Artist, lastfmArtists)
		if err != nil && !errors.Is(err, lastfm.ErrNotFound) {
			return nil, err
		}
		for _, a := range artists[:min(len(artists), lastfmArtists)] {
			queries = append(queries, searchQuery(a.Name))
		}
	}
	var out []suggestion
	for pos, q := range queries {
		v, ok, err := s.searchVideo(ctx, userID, q)
		if err != nil {
			s.log().Warn("recommendations: resolve last.fm suggestion", "query", q, "err", err)
			continue
		}
		if ok {
			out = append(out, suggestion{seed: seed, video: v, pos: pos, lastfm: true})
		}
	}
	return out, nil
}

// searchVideo resolves a query to its first playable YouTube result, cached
// in search_videos (hits for 90 days, misses for 30), within the user's
// daily search budget.
func (s *Service) searchVideo(ctx context.Context, userID int64, q string) (ytdlp.Video, bool, error) {
	if q == "" {
		return ytdlp.Video{}, false, nil
	}
	key := strings.ToLower(q)
	now := s.now()
	var id sql.NullString
	var v ytdlp.Video
	var at int64
	err := s.DB.QueryRowContext(ctx, `SELECT video_id, title, channel, duration_s, searched_at FROM search_videos WHERE query=?`, key).
		Scan(&id, &v.Title, &v.Channel, &v.DurationS, &at)
	switch {
	case err == nil && id.Valid && now.Sub(time.Unix(at, 0)) < hitRetry:
		v.ID, v.URL, v.Thumbnail = id.String, ytdlp.WatchURL(id.String), ytdlp.ThumbnailURL(id.String)
		return v, true, nil
	case err == nil && !id.Valid && now.Sub(time.Unix(at, 0)) < missRetry:
		return ytdlp.Video{}, false, nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return ytdlp.Video{}, false, err
	}
	ok, err := s.spend(ctx, userID, "other_searches", otherSearchesDay)
	if err != nil || !ok {
		return ytdlp.Video{}, false, err
	}
	var results []ytdlp.Video
	if err := s.low(ctx, func(ctx context.Context) error {
		var err error
		results, err = s.YT.SearchTop(ctx, q, 5)
		return err
	}); err != nil {
		return ytdlp.Video{}, false, err
	}
	var pick ytdlp.Video
	found := false
	for _, r := range results {
		if playable(r) {
			pick, found = r, true
			break
		}
	}
	var vid sql.NullString
	if found {
		pick.URL, pick.Thumbnail = ytdlp.WatchURL(pick.ID), ytdlp.ThumbnailURL(pick.ID)
		vid = sql.NullString{String: pick.ID, Valid: true}
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO search_videos(query,video_id,title,channel,duration_s,searched_at) VALUES (?,?,?,?,?,?)
		ON CONFLICT(query) DO UPDATE SET video_id=excluded.video_id, title=excluded.title, channel=excluded.channel,
		duration_s=excluded.duration_s, searched_at=excluded.searched_at`,
		key, vid, pick.Title, pick.Channel, pick.DurationS, now.Unix()); err != nil {
		return ytdlp.Video{}, false, err
	}
	return pick, found, nil
}

// exclusion is what a refresh must never suggest to this user.
type exclusion struct {
	videos  map[string]bool     // downloaded/queued by anyone, dismissed by the user
	library map[string][]string // normalized title → normalized artists in the library
}

func (s *Service) exclusions(ctx context.Context, userID int64) (exclusion, error) {
	ex := exclusion{videos: map[string]bool{}, library: map[string][]string{}}
	rows, err := s.DB.QueryContext(ctx, `SELECT video_id FROM downloads WHERE video_id!='' AND status IN ('queued','downloading','done')
		UNION SELECT video_id FROM recommendation_dismissals WHERE user_id=?`, userID)
	if err != nil {
		return ex, err
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return ex, err
		}
		ex.videos[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ex, err
	}
	rows, err = s.DB.QueryContext(ctx, `SELECT COALESCE(NULLIF(o.title,''), t.tag_title), COALESCE(NULLIF(o.artist,''), t.tag_artist)
		FROM tracks t LEFT JOIN track_overrides o ON o.track_id=t.id WHERE t.status!='trashed' AND t.missing_since IS NULL`)
	if err != nil {
		return ex, err
	}
	defer rows.Close()
	for rows.Next() {
		var title, artist string
		if err := rows.Scan(&title, &artist); err != nil {
			return ex, err
		}
		if nt := lyrics.Norm(title); nt != "" {
			ex.library[nt] = append(ex.library[nt], lyrics.Norm(artist))
		}
	}
	return ex, rows.Err()
}

// inLibrary: the video's cleaned title matches a library song's, and so
// does its artist — the cleaned one, or the library artist's name appearing
// anywhere in the raw title and channel. Comparison is lyrics.Norm
// (pinyin), so 邓丽君 and 鄧麗君 agree.
func (ex exclusion) inLibrary(v ytdlp.Video) bool {
	title, artist := ytdlp.CleanTitle(v.Title, v.Channel)
	arts, ok := ex.library[lyrics.Norm(title)]
	if !ok {
		return false
	}
	na := lyrics.Norm(artist)
	raw := lyrics.Norm(v.Title + " " + v.Channel)
	for _, a := range arts {
		if a == na || (a != "" && strings.Contains(raw, a)) {
			return true
		}
	}
	return false
}

// store replaces the user's list and marks the refresh done.
func (s *Service) store(ctx context.Context, userID int64, seeds []Seed, top []ranked) error {
	now := s.now().Unix()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM recommendations WHERE user_id=?`, userID); err != nil {
		return err
	}
	for _, r := range top {
		sd := seeds[r.reason]
		if _, err := tx.ExecContext(ctx, `INSERT INTO recommendations(user_id,video_id,title,channel,duration_s,thumbnail,score,
			reason_track_id,reason_kind,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			userID, r.video.ID, r.video.Title, r.video.Channel, r.video.DurationS, ytdlp.ThumbnailURL(r.video.ID),
			r.score, sd.TrackID, sd.Kind, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO recommendation_users(user_id,refreshed_at) VALUES (?,?)
		ON CONFLICT(user_id) DO UPDATE SET refreshed_at=excluded.refreshed_at`, userID, now); err != nil {
		return err
	}
	return tx.Commit()
}
