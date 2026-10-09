// Package radio picks the next tracks for a user's personal radio.
package radio

import (
	"context"
	"database/sql"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"
)

type Radio struct {
	DB   *sql.DB
	Now  func() time.Time
	Rand *rand.Rand
}

const (
	wFavorite = 3.0
	wArtist   = 2.0
	wTag      = 1.0
	wFiller   = 0.2
)

func (r *Radio) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Radio) float() float64 {
	if r.Rand != nil {
		return r.Rand.Float64()
	}
	return rand.Float64()
}

func (r *Radio) idSet(ctx context.Context, q string, args ...any) (map[int64]bool, error) {
	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type cand struct {
	id       int64
	artistID sql.NullInt64
	kept     bool
}

func (r *Radio) Next(ctx context.Context, userID int64, n int, exclude []int64) ([]int64, error) {
	if n <= 0 || n > 100 {
		n = 20
	}
	now := r.now().Unix()
	rows, err := r.DB.QueryContext(ctx, `SELECT t.id, t.artist_id, t.status='kept' FROM tracks t
		WHERE t.status!='trashed' AND t.broken=0 AND t.missing_since IS NULL
		AND NOT EXISTS (SELECT 1 FROM dislikes d WHERE d.user_id=? AND d.track_id=t.id)`, userID)
	if err != nil {
		return nil, err
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.artistID, &c.kept); err != nil {
			rows.Close()
			return nil, err
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	favs, err := r.idSet(ctx, `SELECT track_id FROM favorites WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	topArtists, err := r.idSet(ctx, `SELECT t.artist_id FROM play_events e JOIN tracks t ON t.id=e.track_id
		WHERE e.user_id=? AND e.started_at>? AND t.artist_id IS NOT NULL GROUP BY t.artist_id ORDER BY COUNT(*) DESC LIMIT 10`,
		userID, now-90*86400)
	if err != nil {
		return nil, err
	}
	recentTags, err := r.idSet(ctx, `SELECT DISTINCT tt.tag_id FROM play_events e JOIN track_tags tt ON tt.track_id=e.track_id AND tt.removed=0
		WHERE e.user_id=? AND e.started_at>?
		AND (SELECT COUNT(DISTINCT track_id) FROM track_tags x WHERE x.tag_id=tt.tag_id AND x.removed=0) * 2 <= (SELECT COUNT(*) FROM tracks)`,
		userID, now-7*86400)
	if err != nil {
		return nil, err
	}
	tagged := map[int64]bool{}
	if len(recentTags) > 0 {
		// Filter by tag_id in SQL rather than loading every track_tags row
		// and filtering in Go: track_tags has no index on tag_id, so an
		// unfiltered scan is a full-table load on every call.
		ph := strings.TrimSuffix(strings.Repeat("?,", len(recentTags)), ",")
		args := make([]any, 0, len(recentTags))
		for tagID := range recentTags {
			args = append(args, tagID)
		}
		tagged, err = r.idSet(ctx, `SELECT DISTINCT track_id FROM track_tags WHERE removed=0 AND tag_id IN (`+ph+`)`, args...)
		if err != nil {
			return nil, err
		}
	}
	blocked, err := r.idSet(ctx, `SELECT track_id FROM play_events WHERE user_id=? AND (started_at>? OR (skipped=1 AND started_at>?))`,
		userID, now-2*3600, now-7*86400)
	if err != nil {
		return nil, err
	}
	for _, id := range exclude {
		blocked[id] = true
	}
	skips := map[int64]int{}
	srows, err := r.DB.QueryContext(ctx, `SELECT track_id, COUNT(*) FROM play_events WHERE user_id=? AND skipped=1 AND started_at>? GROUP BY track_id`, userID, now-30*86400)
	if err != nil {
		return nil, err
	}
	for srows.Next() {
		var id int64
		var c int
		if err := srows.Scan(&id, &c); err != nil {
			srows.Close()
			return nil, err
		}
		skips[id] = c
	}
	if err := srows.Err(); err != nil {
		srows.Close()
		return nil, err
	}
	srows.Close()

	type keyed struct {
		id  int64
		key float64
	}
	var ks []keyed
	for _, c := range cands {
		if blocked[c.id] {
			continue
		}
		w := 0.0
		switch {
		case favs[c.id]:
			w = wFavorite
		case c.artistID.Valid && topArtists[c.artistID.Int64]:
			w = wArtist
		case tagged[c.id]:
			w = wTag
		case c.kept:
			w = wFiller
		}
		if w == 0 {
			continue
		}
		w *= math.Pow(0.5, float64(skips[c.id]))
		u := r.float()
		ks = append(ks, keyed{c.id, math.Pow(u, 1/w)})
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i].key > ks[j].key })
	if len(ks) > n {
		ks = ks[:n]
	}
	out := make([]int64, len(ks))
	for i, k := range ks {
		out[i] = k.id
	}
	return out, nil
}
