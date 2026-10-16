package recommend

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"math/rand/v2"
	"sort"
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
	Weight        float64 // 1 to 1.5, with the track's affinity
}

const usableTrack = `t.status!='trashed' AND t.missing_since IS NULL AND t.broken=0`

// Seeds: up to 10 of the tracks the user likes most. Each track's affinity
// is 1 for a favorite plus its listening of the last 90 days: a listen adds
// the square of how much of the track it played (1 for a full listen, 0.25
// for half), a skip in the first 30 s takes 0.5, and a listen counts half
// after 30 days. So a favorite skipped often ranks low and a song played to
// the end again and again ranks high, favorite or not. The 5 best are always
// seeds; the rest are drawn by affinity, a new draw each day, so the whole
// collection takes its turn. At most 2 seeds share an artist. Disliked and
// unusable tracks never seed; a track the user doesn't like (affinity ≤ 0)
// neither.
func (s *Service) Seeds(ctx context.Context, userID int64) ([]Seed, error) {
	now := s.now()
	cands := map[int64]*candidate{}
	get := func(id int64) *candidate {
		c := cands[id]
		if c == nil {
			c = &candidate{id: id}
			cands[id] = c
		}
		return c
	}
	favs, err := s.DB.QueryContext(ctx, `SELECT f.track_id FROM favorites f JOIN tracks t ON t.id=f.track_id
		WHERE f.user_id=? AND `+usableTrack+`
		  AND NOT EXISTS (SELECT 1 FROM dislikes d WHERE d.user_id=f.user_id AND d.track_id=f.track_id)`, userID)
	if err != nil {
		return nil, err
	}
	for favs.Next() {
		var id int64
		if err := favs.Scan(&id); err != nil {
			favs.Close()
			return nil, err
		}
		get(id).favorite = true
	}
	if err := favs.Close(); err != nil {
		return nil, err
	}
	plays, err := s.DB.QueryContext(ctx, `SELECT e.track_id, e.started_at, e.played_seconds, e.skipped, t.duration_ms
		FROM play_events e JOIN tracks t ON t.id=e.track_id
		WHERE e.user_id=? AND e.started_at>=? AND `+usableTrack+`
		  AND NOT EXISTS (SELECT 1 FROM dislikes d WHERE d.user_id=e.user_id AND d.track_id=e.track_id)`,
		userID, now.Add(-playWindow).Unix())
	if err != nil {
		return nil, err
	}
	for plays.Next() {
		var id, at, secs, durMS int64
		var skipped bool
		if err := plays.Scan(&id, &at, &secs, &skipped, &durMS); err != nil {
			plays.Close()
			return nil, err
		}
		age := max(0, now.Sub(time.Unix(at, 0)))
		get(id).listening += listenValue(secs, skipped, durMS) * math.Pow(0.5, float64(age)/float64(playHalfLife))
	}
	if err := plays.Close(); err != nil {
		return nil, err
	}

	var ranked []*candidate
	for _, c := range cands {
		if c.affinity() > 0 {
			ranked = append(ranked, c)
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if a, b := ranked[i].affinity(), ranked[j].affinity(); a != b {
			return a > b
		}
		return ranked[i].id < ranked[j].id
	})
	ids := make([]int64, len(ranked))
	for i, c := range ranked {
		ids[i] = c.id
	}
	tracks, err := s.Library.TracksByIDs(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	byID := map[int64]int{}
	for i, t := range tracks {
		byID[t.ID] = i
	}
	pool := ranked[:0]
	for _, c := range ranked {
		if ti, ok := byID[c.id]; ok {
			c.artist = strings.ToLower(strings.TrimSpace(tracks[ti].Artist))
			pool = append(pool, c)
		}
	}

	perArtist := map[string]int{}
	fits := func(c *candidate) bool { return c.artist == "" || perArtist[c.artist] < maxSeedsPerArtist }
	var picked []*candidate
	take := func(i int) {
		c := pool[i]
		picked = append(picked, c)
		perArtist[c.artist]++
		pool = append(pool[:i], pool[i+1:]...)
	}
	for i := 0; i < len(pool) && len(picked) < coreSeeds; {
		if fits(pool[i]) {
			take(i)
		} else {
			i++
		}
	}
	// The draw: the same all day (a retried refresh agrees), another tomorrow.
	day := uint64(now.Unix()) / 86400
	rng := rand.New(rand.NewPCG(uint64(userID), day))
	for len(picked) < maxSeeds {
		total := 0.0
		for _, c := range pool {
			if fits(c) {
				total += c.affinity()
			}
		}
		if total <= 0 {
			break
		}
		r := rng.Float64() * total
		pick := -1
		for i, c := range pool {
			if !fits(c) {
				continue
			}
			pick = i
			if r -= c.affinity(); r < 0 {
				break
			}
		}
		take(pick)
	}

	best := 0.0
	if len(picked) > 0 {
		best = picked[0].affinity()
	}
	seeds := []Seed{}
	for _, c := range picked {
		t := tracks[byID[c.id]]
		sd := Seed{TrackID: t.ID, Title: t.Title, Artist: t.Artist, DurationS: int(t.DurationMS / 1000), Kind: KindFavorite,
			Weight: 1 + seedWeightSpread*c.affinity()/best}
		if !c.favorite || c.listening >= favoriteAffinity {
			sd.Kind = KindPlayed // listened to more than its favorite counts: "因为你常听"
		}
		seeds = append(seeds, sd)
	}
	return seeds, nil
}

// candidate: a track that may seed, and why.
type candidate struct {
	id        int64
	favorite  bool
	listening float64 // the decayed sum of listenValue
	artist    string  // normalized, for the per-artist cap
}

func (c *candidate) affinity() float64 {
	a := c.listening
	if c.favorite {
		a += favoriteAffinity
	}
	return a
}

// listenValue: what one play says about liking the track. A skip in the first
// 30 s is a no; another short play (stopped, switched away) says nothing; a
// longer one the square of how much of the track it played, so finishing it
// counts most. Unknown length: a full listen.
func listenValue(playedS int64, skipped bool, durMS int64) float64 {
	if skipped {
		return -skipPenalty
	}
	if playedS < minPlayedSeconds {
		return 0
	}
	if durMS <= 0 {
		return 1
	}
	c := min(1, float64(playedS)*1000/float64(durMS))
	return c * c
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
