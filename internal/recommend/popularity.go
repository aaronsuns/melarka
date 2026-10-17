package recommend

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sort"
	"time"
)

const (
	// boostPool: how many of the best-ranked candidates get a quality factor.
	boostPool = 40
	// statsLookupsDay: view/like extractions per user and day (a few seconds each).
	statsLookupsDay = 40
	// statsFresh: how long a video's counts are trusted; statsKeep: when the row goes.
	statsFresh = 30 * 24 * time.Hour
	statsKeep  = 90 * 24 * time.Hour
	// neutralQuality: the factor of a video whose counts are unknown.
	neutralQuality = 0.8
)

// qualityFactor is 0.6..1 from YouTube's counts: a quarter of the range for
// views (1k → 0, 10M → full), 0.15 for the like ratio (1.5 % or more → full;
// unknown likes → half). Unknown views give neutralQuality. It scales the
// seed score, so a song the user's taste suggests still beats a popular one
// it doesn't.
func qualityFactor(views, likes int64) float64 {
	if views <= 0 {
		return neutralQuality
	}
	v := clamp01((math.Log10(float64(views)) - 3) / 4)
	l := 0.5
	if likes > 0 {
		l = clamp01(float64(likes) / float64(views) / 0.015)
	}
	return 0.6 + 0.25*v + 0.15*l
}

func clamp01(x float64) float64 { return math.Max(0, math.Min(1, x)) }

// boost multiplies the first boostPool candidates' scores by their quality
// factor (the rest by neutralQuality, so they compare fairly) and sorts
// again. Counts come from video_stats when fresh, else from YT.Stats within
// the user's daily budget; a failed or skipped lookup counts as unknown.
func (s *Service) boost(ctx context.Context, userID int64, rs []ranked) []ranked {
	now := s.now()
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM video_stats WHERE fetched_at < ?`, now.Add(-statsKeep).Unix()); err != nil {
		s.log().Warn("recommendations: prune video stats", "err", err)
	}
	out := append([]ranked(nil), rs...)
	for i := range out {
		f := neutralQuality
		if i < boostPool && ctx.Err() == nil {
			views, likes := s.videoStats(ctx, userID, out[i].video.ID, now)
			if views <= 0 {
				views = out[i].video.Views // a search result's count, without likes
			}
			f = qualityFactor(views, likes)
		}
		out[i].score *= f
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.pos != b.pos {
			return a.pos < b.pos
		}
		return a.video.ID < b.video.ID
	})
	return out
}

// videoStats: id's counts from the cache, or fetched and cached; 0, 0 when unknown.
func (s *Service) videoStats(ctx context.Context, userID int64, id string, now time.Time) (views, likes int64) {
	var at int64
	err := s.DB.QueryRowContext(ctx, `SELECT view_count, like_count, fetched_at FROM video_stats WHERE video_id=?`, id).Scan(&views, &likes, &at)
	switch {
	case err == nil && now.Sub(time.Unix(at, 0)) < statsFresh:
		return views, likes
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		s.log().Warn("recommendations: read video stats", "video", id, "err", err)
		return 0, 0
	}
	if ok, err := s.spend(ctx, userID, "stats_lookups", statsLookupsDay); err != nil || !ok {
		return 0, 0
	}
	err = s.low(ctx, func(ctx context.Context) error {
		var err error
		views, likes, err = s.YT.Stats(ctx, id)
		return err
	})
	if err != nil {
		s.log().Warn("recommendations: video stats", "video", id, "err", err)
		return 0, 0
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO video_stats(video_id,view_count,like_count,fetched_at) VALUES (?,?,?,?)
		ON CONFLICT(video_id) DO UPDATE SET view_count=excluded.view_count, like_count=excluded.like_count, fetched_at=excluded.fetched_at`,
		id, views, likes, now.Unix()); err != nil {
		s.log().Warn("recommendations: store video stats", "video", id, "err", err)
	}
	return views, likes
}
