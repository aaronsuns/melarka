package recommend

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// Seed is one of the user's tracks recommendations grow from.
type Seed struct {
	TrackID       int64
	Title, Artist string
	DurationS     int
	Kind          string  // KindPlayed | KindFavorite
	Weight        float64 // 1, plus a small recency bonus for a new favorite
}

const usableTrack = `t.status!='trashed' AND t.missing_since IS NULL AND t.broken=0`

// Seeds: up to 10 — the most played tracks of the last 30 days (plays of at
// least 30 s) and the newest favorites, deduped, half and half when both
// have enough. Disliked and unusable tracks never seed.
func (s *Service) Seeds(ctx context.Context, userID int64) ([]Seed, error) {
	now := s.now()
	played, err := s.seedIDs(ctx, `SELECT e.track_id, 0 FROM play_events e JOIN tracks t ON t.id=e.track_id
		WHERE e.user_id=? AND e.started_at>=? AND e.played_seconds>=? AND `+usableTrack+`
		  AND NOT EXISTS (SELECT 1 FROM dislikes d WHERE d.user_id=e.user_id AND d.track_id=e.track_id)
		GROUP BY e.track_id ORDER BY COUNT(*) DESC, MAX(e.started_at) DESC, e.track_id LIMIT ?`,
		userID, now.Add(-playWindow).Unix(), minPlayedSeconds, maxSeeds)
	if err != nil {
		return nil, err
	}
	favs, err := s.seedIDs(ctx, `SELECT f.track_id, f.created_at FROM favorites f JOIN tracks t ON t.id=f.track_id
		WHERE f.user_id=? AND `+usableTrack+`
		  AND NOT EXISTS (SELECT 1 FROM dislikes d WHERE d.user_id=f.user_id AND d.track_id=f.track_id)
		ORDER BY f.created_at DESC, f.track_id DESC LIMIT ?`, userID, 2*maxSeeds)
	if err != nil {
		return nil, err
	}
	isPlayed := map[int64]bool{}
	for _, p := range played {
		isPlayed[p.id] = true
	}
	var newFavs []seedRow
	for _, f := range favs {
		if !isPlayed[f.id] {
			newFavs = append(newFavs, f)
		}
	}
	nP := min(len(played), max(maxPlayedSeeds, maxSeeds-len(newFavs)))
	nF := min(len(newFavs), maxSeeds-nP)
	rows := append(played[:nP:nP], newFavs[:nF]...)
	idList := make([]int64, len(rows))
	for i, r := range rows {
		idList[i] = r.id
	}
	tracks, err := s.Library.TracksByIDs(ctx, userID, idList)
	if err != nil {
		return nil, err
	}
	byID := map[int64]int{}
	for i, t := range tracks {
		byID[t.ID] = i
	}
	seeds := []Seed{}
	for i, r := range rows {
		ti, ok := byID[r.id]
		if !ok {
			continue
		}
		t := tracks[ti]
		sd := Seed{TrackID: t.ID, Title: t.Title, Artist: t.Artist, DurationS: int(t.DurationMS / 1000), Kind: KindPlayed, Weight: 1}
		if i >= nP {
			sd.Kind = KindFavorite
			age := now.Sub(time.Unix(r.at, 0))
			sd.Weight += favoriteBonus * max(0, 1-float64(age)/float64(playWindow))
		}
		seeds = append(seeds, sd)
	}
	return seeds, nil
}

type seedRow struct {
	id, at int64
}

func (s *Service) seedIDs(ctx context.Context, q string, args ...any) ([]seedRow, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []seedRow
	for rows.Next() {
		var r seedRow
		if err := rows.Scan(&r.id, &r.at); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// seedVideo is the YouTube video a seed track is: its download job's video
// when it came from YouTube, else a cached search for "title artist" (the
// first result within ±10 s when the duration is known). "" means none
// (a remembered miss, or today's search budget is spent).
func (s *Service) seedVideo(ctx context.Context, userID int64, sd Seed) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT video_id FROM downloads WHERE track_id=? AND video_id!='' AND status='done'
		ORDER BY id DESC LIMIT 1`, sd.TrackID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	now := s.now()
	var cached sql.NullString
	var at int64
	err = s.DB.QueryRowContext(ctx, `SELECT video_id, searched_at FROM seed_videos WHERE track_id=?`, sd.TrackID).Scan(&cached, &at)
	switch {
	case err == nil && cached.Valid:
		return cached.String, nil
	case err == nil && now.Sub(time.Unix(at, 0)) < missRetry:
		return "", nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return "", err
	}
	q := searchQuery(sd.Title, sd.Artist)
	if q == "" {
		return "", nil
	}
	ok, err := s.spend(ctx, userID, "seed_searches", seedSearchesDay)
	if err != nil || !ok {
		return "", err
	}
	var results []ytdlp.Video
	if err := s.low(ctx, func(ctx context.Context) error {
		var err error
		results, err = s.YT.SearchTop(ctx, q, 5)
		return err
	}); err != nil {
		return "", err
	}
	var pick sql.NullString
	for _, v := range results {
		if !ytdlp.IsVideoID(v.ID) {
			continue
		}
		if sd.DurationS <= 0 || abs(v.DurationS-sd.DurationS) <= durationTolerance {
			pick = sql.NullString{String: v.ID, Valid: true}
			break
		}
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO seed_videos(track_id,video_id,searched_at) VALUES (?,?,?)
		ON CONFLICT(track_id) DO UPDATE SET video_id=excluded.video_id, searched_at=excluded.searched_at`,
		sd.TrackID, pick, now.Unix()); err != nil {
		return "", err
	}
	return pick.String, nil
}

// searchQuery joins non-empty parts, trimmed to yt-dlp's 100-rune query limit.
func searchQuery(parts ...string) string {
	var ps []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			ps = append(ps, p)
		}
	}
	q := strings.Join(ps, " ")
	for utf8.RuneCountInString(q) > 100 {
		_, n := utf8.DecodeLastRuneInString(q)
		q = q[:len(q)-n]
	}
	return strings.TrimSpace(q)
}

// spend takes one search from the user's daily budget (column counter, cap
// per day); false when it is used up.
func (s *Service) spend(ctx context.Context, userID int64, counter string, limit int) (bool, error) {
	day := s.now().Format(time.DateOnly)
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO recommendation_users(user_id) VALUES (?) ON CONFLICT(user_id) DO NOTHING`, userID); err != nil {
		return false, err
	}
	// counter is one of two fixed column names, never user input.
	if _, err := s.DB.ExecContext(ctx, `UPDATE recommendation_users SET
		seed_searches = CASE WHEN search_day=? THEN seed_searches ELSE 0 END,
		other_searches = CASE WHEN search_day=? THEN other_searches ELSE 0 END,
		search_day = ?
		WHERE user_id=?`, day, day, day, userID); err != nil {
		return false, err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE recommendation_users SET `+counter+`=`+counter+`+1 WHERE user_id=? AND `+counter+`<?`, userID, limit)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
