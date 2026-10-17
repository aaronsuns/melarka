package recommend

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func TestQualityFactor(t *testing.T) {
	near := func(a, b float64) bool { return math.Abs(a-b) < 0.01 }
	for _, c := range []struct {
		views, likes int64
		want         float64
	}{
		{0, 0, 0.8},                 // unknown: neutral
		{234_239_467, 3_653_497, 1}, // a classic: many views, 1.6 % likes
		{1_000, 0, 0.6 + 0.075},     // few views, likes unknown: half the like share
		{5_000, 20, 0.6 + 0.0437 + 0.04},
		{10_000_000, 15_000, 0.6 + 0.25 + 0.015}, // popular but rarely liked
	} {
		if got := qualityFactor(c.views, c.likes); !near(got, c.want) {
			t.Errorf("qualityFactor(%d, %d) = %.3f, want %.3f", c.views, c.likes, got, c.want)
		}
	}
}

func boosted(svc *Service, ctx context.Context, user int64, rs []ranked) []string {
	var out []string
	for _, r := range svc.boost(ctx, user, rs) {
		out = append(out, r.video.ID)
	}
	return out
}

// A well-liked song overtakes a slightly closer match nobody watches; stats are cached for 30 days.
func TestBoostRanksByQualityAndCaches(t *testing.T) {
	e := newEnv(t)
	u := e.user("alice")
	e.yt.stats = map[string][2]int64{
		"lowq_000000": {3_000, 5},
		"good_000000": {50_000_000, 900_000},
	}
	rs := []ranked{
		{video: ytdlp.Video{ID: "lowq_000000"}, score: 1.1},
		{video: ytdlp.Video{ID: "good_000000"}, score: 1.0},
		{video: ytdlp.Video{ID: "gone_000000"}, score: 0.9}, // stats fail: neutral
	}
	ctx := context.Background()
	if got := boosted(e.svc, ctx, u, rs); !slices.Equal(got, []string{"good_000000", "gone_000000", "lowq_000000"}) {
		t.Fatalf("order %v", got) // good 1.0×1.0; gone 0.9×0.8 = 0.72; lowq 1.1×0.65 = 0.71
	}
	if len(e.yt.statsAsked) != 3 {
		t.Fatalf("asked %v", e.yt.statsAsked)
	}
	e.clock.Add(29 * 24 * time.Hour)
	boosted(e.svc, ctx, u, rs)
	if len(e.yt.statsAsked) != 4 || e.yt.statsAsked[3] != "gone_000000" { // only the failed one again
		t.Fatalf("cache: asked %v", e.yt.statsAsked)
	}
	e.clock.Add(2 * 24 * time.Hour)
	boosted(e.svc, ctx, u, rs)
	if len(e.yt.statsAsked) != 7 {
		t.Fatalf("after 31 days: asked %v", e.yt.statsAsked)
	}
}

// Only the top of the list is looked up, within a daily budget; the rest count as neutral.
func TestBoostPoolAndBudget(t *testing.T) {
	e := newEnv(t)
	u := e.user("alice")
	e.yt.stats = map[string][2]int64{}
	var rs []ranked
	for i := range boostPool + 10 {
		id := ytdlp.Video{ID: string(rune('a'+i%26)) + string(rune('a'+i/26)) + "_00000000"}
		e.yt.stats[id.ID] = [2]int64{1_000_000, 10_000}
		rs = append(rs, ranked{video: id, score: 2 - float64(i)/100})
	}
	ctx := context.Background()
	got := e.svc.boost(ctx, u, rs)
	if len(got) != len(rs) || len(e.yt.statsAsked) != boostPool {
		t.Fatalf("got %d, asked %d", len(got), len(e.yt.statsAsked))
	}
	// A second refresh the same day finds them cached: no budget needed.
	e.svc.boost(ctx, u, rs)
	if len(e.yt.statsAsked) != boostPool {
		t.Fatalf("asked again: %d", len(e.yt.statsAsked))
	}
	// New candidates the same day: the budget (statsLookupsDay) is spent.
	var more []ranked
	for i := range boostPool {
		id := ytdlp.Video{ID: "new" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "_00000"}
		e.yt.stats[id.ID] = [2]int64{1, 1}
		more = append(more, ranked{video: id, score: 1})
	}
	e.svc.boost(ctx, u, more)
	if n := len(e.yt.statsAsked) - boostPool; n != statsLookupsDay-boostPool {
		t.Fatalf("new lookups the same day: %d, want %d", n, statsLookupsDay-boostPool)
	}
}

// A seed song's video is the most watched upload of the right length, not just YouTube's first hit.
func TestSeedVideoPrefersPopularVersion(t *testing.T) {
	e := newEnv(t)
	u := e.user("alice")
	tr := e.track("流年", "王菲", 263)
	e.fav(u, tr, t0)
	e.yt.search["流年 王菲"] = []ytdlp.Video{
		{ID: "fanup_00000", DurationS: 265, Views: 3_000},
		{ID: "wrongl_0000", DurationS: 400, Views: 90_000_000},
		{ID: "offic_00000", DurationS: 268, Views: 3_000_000},
	}
	seeds, err := e.svc.Seeds(context.Background(), u)
	if err != nil || len(seeds) != 1 {
		t.Fatalf("%+v %v", seeds, err)
	}
	if v, err := e.svc.seedVideo(context.Background(), u, seeds[0]); err != nil || v != "offic_00000" {
		t.Fatalf("seed video %q %v", v, err)
	}
}
