package channels

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func rv(id string, dur int) ytdlp.Video {
	return ytdlp.Video{ID: id, Title: "Video " + id, Channel: "Name", ChannelID: chX, DurationS: dur}
}

func idsOf(items []VideoRec) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.VideoID)
	}
	return out
}

func (f *fakeYT) mixCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.mixCalls)
}

// runRefreshSteps drives refreshStep n times (it runs at most one user per step).
func (e *env) runRefreshSteps(n int) {
	for range n {
		e.svc.refreshStep(context.Background())
	}
}

func TestVideoRecsRankByFrequencyExcludingWatched(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	anna := e.user("anna")
	// anna watched A three times and B once, and searched "q" (first result C).
	for range 3 {
		must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "AAAAAAAAAAA", Title: "A"}))
	}
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "BBBBBBBBBBB", Title: "B"}))
	must(t, e.svc.RecordVideoSearch(ctx, anna, "q", "CCCCCCCCCCC"))
	e.yt.mix = map[string][]ytdlp.Video{
		"AAAAAAAAAAA": {rv("XXXXXXXXXXX", 300), rv("BBBBBBBBBBB", 300), rv("YYYYYYYYYYY", 300)},
		"BBBBBBBBBBB": {rv("YYYYYYYYYYY", 300), rv("ZZZZZZZZZZZ", 30) /* too short */},
		"CCCCCCCCCCC": {rv("YYYYYYYYYYY", 300), {ID: "LLLLLLLLLLL", DurationS: 600, Live: true}},
	}
	g := &countGate{}
	e.svc.Gate = g
	must(t, e.svc.refreshVideoRecs(ctx, anna, true))
	r, err := e.svc.VideoRecs(ctx, anna)
	must(t, err)
	ids := idsOf(r.Items)
	// Y: A(3)+B(1)+q(1)=5; X: A(3)=3; B watched → out; Z short → out; L live → out.
	if !slices.Equal(ids, []string{"YYYYYYYYYYY", "XXXXXXXXXXX"}) {
		t.Fatalf("%v", ids)
	}
	if r.Items[0].ReasonKind != "watch" || r.Items[0].Reason != "A" || r.Items[0].Thumbnail != "/api/v1/videos/YYYYYYYYYYY/thumbnail" ||
		r.Items[0].Score != 5 || r.Items[0].Title != "Video YYYYYYYYYYY" || r.Items[0].ChannelID != chX || r.Items[0].DurationS != 300 {
		t.Errorf("%+v", r.Items[0])
	}
	if r.RefreshedAt == nil || r.Refreshing {
		t.Errorf("%+v", r)
	}
	if g.n != 3 || e.count(`SELECT mixes FROM video_rec_users WHERE user_id=?`, anna) != 3 {
		t.Errorf("Mix calls through the low gate, counted against the budget: gate %d", g.n)
	}
	// A search seed's reason is its query.
	e.yt.mix["CCCCCCCCCCC"] = append(e.yt.mix["CCCCCCCCCCC"], rv("QQQQQQQQQQQ", 300))
	e.exec(`DELETE FROM video_mixes`)
	must(t, e.svc.refreshVideoRecs(ctx, anna, false))
	r, _ = e.svc.VideoRecs(ctx, anna)
	if i := slices.Index(idsOf(r.Items), "QQQQQQQQQQQ"); i < 0 || r.Items[i].ReasonKind != "search" || r.Items[i].Reason != "q" {
		t.Errorf("%+v", r.Items)
	}
	// Watching a recommendation hides it at once.
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "YYYYYYYYYYY", Title: "Y"}))
	if r, _ := e.svc.VideoRecs(ctx, anna); slices.Contains(idsOf(r.Items), "YYYYYYYYYYY") {
		t.Errorf("watched since: %v", idsOf(r.Items))
	}
}

func TestVideoRecsOnVisitDedupeAndBudget(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	anna := e.user("anna")
	e.svc.RefreshAt = "23:00" // not due: only requests run
	if r, _ := e.svc.VideoRecs(ctx, anna); r.Refreshing || r.Items == nil {
		t.Fatalf("no history: nothing to refresh, empty list: %+v", r)
	}
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "AAAAAAAAAAA", Title: "A"}))
	e.yt.mix["AAAAAAAAAAA"] = []ytdlp.Video{rv("XXXXXXXXXXX", 300)}
	e.yt.mix["BBBBBBBBBBB"] = []ytdlp.Video{rv("YYYYYYYYYYY", 300)}
	for range 5 {
		r, err := e.svc.VideoRecs(ctx, anna) // five visits in a row
		must(t, err)
		if !r.Refreshing {
			t.Fatal("first visit with history must request a refresh")
		}
	}
	e.runRefreshSteps(5)
	if e.yt.mixCount() != 1 {
		t.Fatalf("Mix calls %d, want 1 (requests joined)", e.yt.mixCount())
	}
	if r, _ := e.svc.VideoRecs(ctx, anna); r.Refreshing || !slices.Equal(idsOf(r.Items), []string{"XXXXXXXXXXX"}) {
		t.Fatalf("%+v", r)
	}
	// No history change: a visit 40 min later requests nothing.
	e.clock.Add(40 * time.Minute)
	if r, _ := e.svc.VideoRecs(ctx, anna); r.Refreshing {
		t.Error("unchanged history must not refresh")
	}
	// A new watch, but the list is only 10 min old: not yet.
	e.clock.Add(time.Second)
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "BBBBBBBBBBB", Title: "B"}))
	e.exec(`UPDATE video_rec_users SET refreshed_at=? WHERE user_id=?`, e.clock.Now().Add(-10*time.Minute).Unix(), anna)
	if r, _ := e.svc.VideoRecs(ctx, anna); r.Refreshing {
		t.Error("refreshed under 30 min ago: no refresh")
	}
	e.exec(`UPDATE video_rec_users SET refreshed_at=? WHERE user_id=?`, e.clock.Now().Add(-40*time.Minute).Unix(), anna)
	// Now stale: refresh again — A's Mix is cached (no new call), B's is fetched.
	if r, _ := e.svc.VideoRecs(ctx, anna); !r.Refreshing {
		t.Fatal("history changed: refresh")
	}
	e.runRefreshSteps(5)
	if e.yt.mixCount() != 2 {
		t.Fatalf("Mix calls %d, want 2 (cache hit for A)", e.yt.mixCount())
	}
	if n := e.count(`SELECT mixes FROM video_rec_users WHERE user_id=?`, anna); n != 2 {
		t.Errorf("only fetched Mixes count: %d", n)
	}
	// Budget spent: a visit is served and requests nothing.
	e.exec(`UPDATE video_rec_users SET day=?, mixes=40 WHERE user_id=?`, e.clock.Now().Format(time.DateOnly), anna)
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "CCCCCCCCCCC"}))
	e.clock.Add(40 * time.Minute)
	r, err := e.svc.VideoRecs(ctx, anna)
	if err != nil || r.Refreshing || len(r.Items) == 0 {
		t.Errorf("over budget: served, no on-visit refresh: %+v %v", r, err)
	}
}

func TestVideoRecsNightlyOnlyRecentHistory(t *testing.T) {
	e, anna := seedDiscovery(t) // anna follows A and B: discovery runs for her too
	ctx := context.Background()
	bo := e.user("bo")
	g := &countGate{}
	e.svc.Gate = g
	must(t, e.svc.RecordWatch(ctx, bo, ytdlp.Video{ID: "BBBBBBBBBBB", Title: "B"}))
	e.clock.Add(39 * 24 * time.Hour)
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "AAAAAAAAAAA", Title: "A"}))
	e.clock.Add(24 * time.Hour) // bo's watch is 40 days old, anna's one day
	e.yt.mix["AAAAAAAAAAA"] = []ytdlp.Video{rv("XXXXXXXXXXX", 300)}
	e.yt.mix["BBBBBBBBBBB"] = []ytdlp.Video{rv("YYYYYYYYYYY", 300)}
	e.svc.RefreshAt = "11:00" // the clock says 12:00: due
	e.runRefreshSteps(6)
	if r, _ := e.svc.VideoRecs(ctx, anna); r.RefreshedAt == nil || !slices.Equal(idsOf(r.Items), []string{"XXXXXXXXXXX"}) {
		t.Fatalf("anna: %+v", r)
	}
	if e.count(`SELECT COUNT(*) FROM video_rec_users WHERE user_id=?`, bo) != 0 || slices.Contains(e.yt.mixCalls, "BBBBBBBBBBB") {
		t.Fatal("bo's history is too old for the nightly run")
	}
	if sg, _ := e.svc.Suggestions(ctx, anna); sg.RefreshedAt == nil {
		t.Fatal("channel discovery runs in the same loop")
	}
	if e.count(`SELECT mixes FROM video_rec_users WHERE user_id=?`, anna) != 0 || e.count(`SELECT mixes FROM discovery_users WHERE user_id=?`, anna) != 0 {
		t.Fatal("the nightly run is exempt from the budget")
	}
	if g.n != e.yt.mixCount() {
		t.Fatalf("every Mix through the low gate: %d vs %d", g.n, e.yt.mixCount())
	}
	if e.count(`SELECT COUNT(*) FROM app_state WHERE key IN ('channels.discovery_day','video.recs_day')`) != 2 {
		t.Fatal("both nights marked done")
	}
}

func TestClearHistoryDropsRecs(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	anna, bo := e.user("anna"), e.user("bo")
	for _, u := range []int64{anna, bo} {
		must(t, e.svc.RecordWatch(ctx, u, ytdlp.Video{ID: "AAAAAAAAAAA", Title: "A"}))
		must(t, e.svc.RecordVideoSearch(ctx, u, "q", ""))
	}
	e.yt.mix["AAAAAAAAAAA"] = []ytdlp.Video{rv("XXXXXXXXXXX", 300)}
	must(t, e.svc.refreshVideoRecs(ctx, anna, false))
	must(t, e.svc.refreshVideoRecs(ctx, bo, false))
	must(t, e.svc.ClearVideoHistory(ctx, anna))
	r, err := e.svc.VideoRecs(ctx, anna)
	must(t, err)
	if len(r.Items) != 0 || r.Refreshing {
		t.Fatalf("%+v", r)
	}
	if h, _ := e.svc.VideoHistory(ctx, anna); len(h.Watches)+len(h.Searches) != 0 {
		t.Fatalf("%+v", h)
	}
	if r, _ := e.svc.VideoRecs(ctx, bo); len(r.Items) != 1 {
		t.Fatal("bo's list stays")
	}
	if h, _ := e.svc.VideoHistory(ctx, bo); len(h.Watches) != 1 || len(h.Searches) != 1 {
		t.Fatal("bo's history stays")
	}
}

// hookGate runs hook before each low-priority call (a watch landing mid-refresh).
type hookGate struct{ hook func() }

func (g *hookGate) Low(ctx context.Context, fn func(context.Context) error) error {
	if g.hook != nil {
		g.hook()
	}
	return fn(ctx)
}

func TestVideoRecsPruneDropsRecsAndStaleReasons(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	anna, bo := e.user("anna"), e.user("bo")
	must(t, e.svc.RecordVideoSearch(ctx, anna, "离婚律师", "AAAAAAAAAAA"))
	e.yt.mix["AAAAAAAAAAA"] = []ytdlp.Video{rv("XXXXXXXXXXX", 300)}
	must(t, e.svc.refreshVideoRecs(ctx, anna, false))
	if r, _ := e.svc.VideoRecs(ctx, anna); len(r.Items) != 1 || r.Items[0].Reason != "离婚律师" {
		t.Fatalf("setup: %+v", r)
	}
	// bo's recommendation is fresh: it survives anna's prune.
	e.clock.Add(91 * 24 * time.Hour)
	must(t, e.svc.RecordWatch(ctx, bo, ytdlp.Video{ID: "AAAAAAAAAAA", Title: "A"}))
	must(t, e.svc.refreshVideoRecs(ctx, bo, false))
	if _, err := e.svc.pruneVideo(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT COUNT(*) FROM video_recommendations WHERE user_id=?`, anna); n != 0 {
		t.Fatalf("anna's 91-day-old recommendation (reason: her pruned search) stays: %d", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM video_recommendations WHERE user_id=?`, bo); n != 1 {
		t.Fatalf("bo's fresh one went: %d", n)
	}
	// A reason whose seed is gone is never shown, even before the prune removes the row.
	e.exec(`DELETE FROM video_history WHERE user_id=?`, bo)
	if r, _ := e.svc.VideoRecs(ctx, bo); len(r.Items) != 0 {
		t.Fatalf("reason refers to a pruned seed: %+v", r.Items)
	}
	// Recommendations of a user whose history is gone are deleted by the prune.
	if _, err := e.svc.pruneVideo(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT COUNT(*) FROM video_recommendations`); n != 0 {
		t.Fatalf("%d rows left", n)
	}
}

func TestVideoRecsWatchDuringRefreshIsNotLost(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	anna := e.user("anna")
	e.svc.RefreshAt = "23:00"
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "AAAAAAAAAAA", Title: "A"}))
	e.yt.mix["AAAAAAAAAAA"] = []ytdlp.Video{rv("XXXXXXXXXXX", 300)}
	e.yt.mix["BBBBBBBBBBB"] = []ytdlp.Video{rv("YYYYYYYYYYY", 300)}
	once := false
	e.svc.Gate = &hookGate{hook: func() {
		if !once {
			once = true
			e.clock.Add(20 * time.Second)
			must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "BBBBBBBBBBB", Title: "B"}))
			e.clock.Add(70 * time.Second) // the run ends 90 s after it read its seeds
		}
	}}
	if r, _ := e.svc.VideoRecs(ctx, anna); !r.Refreshing {
		t.Fatal("setup: refresh requested")
	}
	e.runRefreshSteps(2)
	e.clock.Add(31 * time.Minute)
	if r, _ := e.svc.VideoRecs(ctx, anna); !r.Refreshing {
		t.Fatal("B was watched after the seeds were read: the next visit past the 30 min throttle refreshes")
	}
	e.runRefreshSteps(2)
	if r, _ := e.svc.VideoRecs(ctx, anna); !slices.Contains(idsOf(r.Items), "YYYYYYYYYYY") {
		t.Fatalf("%v", idsOf(r.Items))
	}
}

func TestClearDuringRefreshDiscardsIt(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	anna := e.user("anna")
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "AAAAAAAAAAA", Title: "秘密标题"}))
	e.yt.mix["AAAAAAAAAAA"] = []ytdlp.Video{rv("XXXXXXXXXXX", 300)}
	e.svc.Gate = &hookGate{hook: func() {
		// Mid-refresh: anna clears, then opens one more video.
		must(t, e.svc.ClearVideoHistory(ctx, anna))
		must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "CCCCCCCCCCC", Title: "C"}))
	}}
	if err := e.svc.refreshVideoRecs(ctx, anna, false); err != nil {
		t.Fatal(err)
	}
	e.svc.Gate = nil
	if n := e.count(`SELECT COUNT(*) FROM video_recommendations WHERE user_id=? OR reason='秘密标题'`, anna); n != 0 {
		t.Fatalf("a refresh started before the clear stored %d rows", n)
	}
}

func TestVideoRecsPerUserIsolation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	anna, bo := e.user("anna"), e.user("bo")
	e.svc.RefreshAt = "23:00"
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "AAAAAAAAAAA", Title: "A"}))
	e.yt.mix["AAAAAAAAAAA"] = []ytdlp.Video{rv("XXXXXXXXXXX", 300)}
	e.svc.VideoRecs(ctx, anna)
	if r, _ := e.svc.VideoRecs(ctx, bo); r.Refreshing || len(r.Items) != 0 || r.RefreshedAt != nil {
		t.Fatalf("anna's pending refresh shows for bo: %+v", r)
	}
	e.runRefreshSteps(2)
	if r, _ := e.svc.VideoRecs(ctx, anna); len(r.Items) != 1 {
		t.Fatalf("setup: %+v", r)
	}
	if r, _ := e.svc.VideoRecs(ctx, bo); r.Refreshing || len(r.Items) != 0 || r.RefreshedAt != nil {
		t.Fatalf("bo sees anna's list: %+v", r)
	}
}

func TestDeletingAUserDropsTheirVideoData(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	anna, bo := e.user("anna"), e.user("bo")
	for _, u := range []int64{anna, bo} {
		must(t, e.svc.RecordWatch(ctx, u, ytdlp.Video{ID: "AAAAAAAAAAA", Title: "A"}))
		must(t, e.svc.RecordVideoSearch(ctx, u, "q", "AAAAAAAAAAA"))
	}
	e.yt.mix["AAAAAAAAAAA"] = []ytdlp.Video{rv("XXXXXXXXXXX", 300)}
	must(t, e.svc.refreshVideoRecs(ctx, anna, true))
	must(t, e.svc.refreshVideoRecs(ctx, bo, true))
	must(t, (&auth.Store{DB: e.db}).DeleteUser(ctx, anna))
	for _, tbl := range []string{"video_history", "video_recommendations", "video_rec_users"} {
		if n := e.count(`SELECT COUNT(*) FROM `+tbl+` WHERE user_id=?`, anna); n != 0 {
			t.Errorf("%s: %d rows of the deleted user", tbl, n)
		}
		if n := e.count(`SELECT COUNT(*) FROM `+tbl+` WHERE user_id=?`, bo); n == 0 {
			t.Errorf("%s: bo's rows went too", tbl)
		}
	}
}

func TestStoredUntrustedTextIsBounded(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	anna := e.user("anna")
	long := strings.Repeat("长", 1000)
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "AAAAAAAAAAA", Title: long, Channel: long, ChannelID: "UC'; DROP TABLE x;--"}))
	h, _ := e.svc.VideoHistory(ctx, anna)
	w := h.Watches[0]
	if utf8.RuneCountInString(w.Title) != 300 || utf8.RuneCountInString(w.Channel) != 300 || w.ChannelID != "" {
		t.Fatalf("title %d channel %d channel_id %q", utf8.RuneCountInString(w.Title), utf8.RuneCountInString(w.Channel), w.ChannelID)
	}
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "AAAAAAAAAAA", ChannelID: chA}))
	if h, _ := e.svc.VideoHistory(ctx, anna); h.Watches[0].ChannelID != chA {
		t.Fatalf("a valid channel id is kept: %q", h.Watches[0].ChannelID)
	}
	// A Mix's entries: at most 50, text clipped.
	var big []ytdlp.Video
	for i := range 80 {
		big = append(big, ytdlp.Video{ID: fmt.Sprintf("m%010d", i), Title: long, Channel: long, ChannelID: "junk", DurationS: 300})
	}
	got, err := e.svc.CachedMix(ctx, "AAAAAAAAAAA", func(context.Context) ([]ytdlp.Video, error) { return big, nil })
	must(t, err)
	if len(got) != 50 || utf8.RuneCountInString(got[0].Title) != 300 || utf8.RuneCountInString(got[0].Channel) != 300 || got[0].ChannelID != "" {
		t.Fatalf("%d entries, title %d", len(got), utf8.RuneCountInString(got[0].Title))
	}
	// The cache keeps the newest maxMixRows rows.
	defer func(n int) { maxMixRows = n }(maxMixRows)
	maxMixRows = 20
	for i := range maxMixRows + 5 {
		e.clock.Add(time.Second)
		must(t, e.svc.putMix(ctx, fmt.Sprintf("c%010d", i), nil, e.clock.Now().Unix()))
	}
	if n := e.count(`SELECT COUNT(*) FROM video_mixes`); n != maxMixRows {
		t.Fatalf("mix rows %d", n)
	}
	if e.count(`SELECT COUNT(*) FROM video_mixes WHERE video_id=?`, fmt.Sprintf("c%010d", maxMixRows+4)) != 1 {
		t.Fatal("the newest row must stay")
	}
}

// 为你推荐 reads the same Mixes: a thin one's channel uploads (duration
// unknown, which is allowed) are recommended too; the seed stays out.
func TestVideoRecsUseTheThinMixFallback(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	anna := e.user("anna")
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "AAAAAAAAAAA", Title: "A", ChannelID: chA}))
	e.yt.mix = map[string][]ytdlp.Video{"AAAAAAAAAAA": {rv("AAAAAAAAAAA", 900)}}
	e.feeds.feeds[chA] = Feed{Title: "C", Entries: []FeedEntry{{VideoID: "AAAAAAAAAAA", Title: "A"}, e.entry(1, time.Hour), e.entry(2, 2*time.Hour)}}
	must(t, e.svc.refreshVideoRecs(ctx, anna, false))
	r, err := e.svc.VideoRecs(ctx, anna)
	must(t, err)
	if ids := idsOf(r.Items); !slices.Equal(ids, []string{vid(1), vid(2)}) {
		t.Fatalf("%v", ids)
	}
	if it := r.Items[0]; it.DurationS != 0 || it.ChannelID != chA || it.Reason != "A" {
		t.Errorf("%+v", it)
	}
}
