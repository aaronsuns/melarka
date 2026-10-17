package ytdlp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
)

// popularStep is what one place lower in YouTube's order costs, in orders of
// magnitude of views: a video needs about twice the views per place to
// overtake one above it.
const popularStep = 0.3

// Popular returns vs re-ranked by YouTube's order blended with the view
// count: log10(views) − popularStep × position. YouTube's relevance stays the
// main signal; a far more watched version (the official upload, the song
// everyone plays) moves up a few places. An unknown count counts as the
// median of the known ones; with none known the order is YouTube's. vs is
// not modified.
func Popular(vs []Video) []Video {
	out := slices.Clone(vs)
	var known []int64
	for _, v := range vs {
		if v.Views > 0 {
			known = append(known, v.Views)
		}
	}
	if len(known) == 0 {
		return out
	}
	slices.Sort(known)
	median := known[len(known)/2]
	score := make(map[string]float64, len(vs))
	for i, v := range vs {
		n := v.Views
		if n <= 0 {
			n = median
		}
		score[v.ID] = math.Log10(float64(n)+1) - popularStep*float64(i)
	}
	sort.SliceStable(out, func(i, j int) bool { return score[out[i].ID] > score[out[j].ID] })
	return out
}

// Stats reads one video's view and like counts. Flat lists never carry
// likes, so this is a full extraction (a few seconds): callers cache it.
func (c *Client) Stats(ctx context.Context, videoID string) (views, likes int64, err error) {
	if !IsVideoID(videoID) {
		return 0, 0, ErrBadURL
	}
	args := append(c.baseArgs(), "-J", "--no-warnings", "--skip-download", "--no-playlist", WatchURL(videoID))
	out, err := c.Runner.Output(ctx, args)
	if err != nil {
		return 0, 0, err
	}
	var e ytEntry
	if err := json.Unmarshal(out, &e); err != nil {
		return 0, 0, fmt.Errorf("ytdlp: parse json: %w", err)
	}
	return e.ViewCount, e.LikeCount, nil
}
