package recommend

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/lastfm"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func TestBlockedTitle(t *testing.T) {
	blocked := []string{"邓丽君经典歌曲合集", "经典老歌串烧", "儿歌大全", "射雕英雄传 全集", "Chill MIX 2024", "My Playlist",
		"轻音乐 1小时", "Piano for 3 Hours", "1 hour of rain", "LIVE STREAM 24/7", "Lofi live stream"}
	for _, s := range blocked {
		if !BlockedTitle(s) {
			t.Errorf("%q not blocked", s)
		}
	}
	kept := []string{"甜蜜蜜", "Faded (Remix)", "Mixtape Love", "月亮代表我的心 Live", "Hourglass", "Playful"}
	for _, s := range kept {
		if BlockedTitle(s) {
			t.Errorf("%q blocked", s)
		}
	}
}

func TestPlayable(t *testing.T) {
	cases := []struct {
		v    ytdlp.Video
		want bool
	}{
		{vid("aaaaaaaaa01", "Song", "c", 200), true},
		{vid("aaaaaaaaa01", "Song", "c", 60), true},
		{vid("aaaaaaaaa01", "Song", "c", 600), true},
		{vid("aaaaaaaaa01", "Song", "c", 59), false},
		{vid("aaaaaaaaa01", "Song", "c", 601), false},
		{vid("aaaaaaaaa01", "Song", "c", 0), false}, // unknown: usually a stream
		{ytdlp.Video{ID: "aaaaaaaaa01", Title: "Song", DurationS: 200, Live: true}, false},
		{vid("aaaaaaaaa01", "歌曲合集", "c", 200), false},
		{vid("bad", "Song", "c", 200), false}, // not a video id
	}
	for _, c := range cases {
		if got := playable(c.v); got != c.want {
			t.Errorf("%+v: %v", c.v, got)
		}
	}
}

func TestRankScoring(t *testing.T) {
	seeds := []Seed{{TrackID: 1, Kind: KindPlayed, Weight: 1}, {TrackID: 2, Kind: KindFavorite, Weight: 1.2}, {TrackID: 3, Kind: KindPlayed, Weight: 1}}
	v := func(id string) ytdlp.Video { return vid(id, id, "c", 200) }
	sugg := []suggestion{
		{seed: 0, video: v("onlyseed0aa"), pos: 0},
		{seed: 0, video: v("twoseedsaaa"), pos: 5},
		{seed: 1, video: v("twoseedsaaa"), pos: 3},
		{seed: 0, video: v("agreedaaaaa"), pos: 1},
		{seed: 0, video: v("agreedaaaaa"), pos: 0, lastfm: true},
		{seed: 2, video: v("onlyseed2aa"), pos: 0},
		{seed: 2, video: v("onlyseed2aa"), pos: 9}, // the same seed twice counts once
		{seed: 1, video: v("lastfmonlya"), pos: 2, lastfm: true},
	}
	got := rank(seeds, sugg)
	var order []string
	for _, r := range got {
		order = append(order, fmt.Sprintf("%s=%.2f/%d", r.video.ID, r.score, seeds[r.reason].TrackID))
	}
	// two seeds (1 + 1.2) > Last.fm agreement (1 + 0.5) > favorite seed alone (1.2) > played seed (1, earlier position first).
	want := "[twoseedsaaa=2.20/2 agreedaaaaa=1.50/1 lastfmonlya=1.20/2 onlyseed0aa=1.00/1 onlyseed2aa=1.00/3]"
	if fmt.Sprint(order) != want {
		t.Fatalf("%v\nwant %s", order, want)
	}
}

// Two seeds, each with a Mix; every filter applied; the shared suggestion first.
func TestRefreshFiltersAndRanks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, other := e.user("u"), e.user("other")
	s1, s2 := e.track("甜蜜蜜", "邓丽君", 213), e.track("Faded", "Alan Walker", 212)
	e.play(u, s1, 5, t0.Add(-time.Hour), 200)
	e.fav(u, s2, t0.Add(-time.Hour))
	e.download(u, "seedvideo01", "done", s1)
	e.yt.search["Faded Alan Walker"] = []ytdlp.Video{vid("seedvideo02", "Alan Walker - Faded", "Alan Walker", 212)}
	e.track("月亮代表我的心", "鄧麗君", 205) // in the library (traditional spelling of the artist)
	e.download(other, "otherdownld", "done", 0)
	e.download(other, "queuedother", "queued", 0)
	e.download(other, "failedother", "failed", 0) // a failed job doesn't hide it
	e.exec(`INSERT INTO recommendation_dismissals(user_id,video_id,created_at) VALUES (?,?,1)`, u, "dismissedaa")
	e.yt.mix["seedvideo01"] = []ytdlp.Video{
		vid("seedvideo01", "邓丽君 - 甜蜜蜜", "邓丽君", 213),
		vid("libraryhit1", "邓丽君 - 月亮代表我的心 (官方MV)", "邓丽君频道", 205),
		vid("sharedsugg1", "蔡琴 - 恰似你的温柔", "蔡琴", 247),
		vid("otherdownld", "Downloaded", "c", 200),
		vid("queuedother", "Queued", "c", 200),
		vid("failedother", "Failed before", "c", 200),
		vid("dismissedaa", "Dismissed", "c", 200),
		vid("compilation", "邓丽君 50首 合集", "c", 300),
		vid("toolongaaaa", "Long", "c", 601),
		vid("tooshortaaa", "Short", "c", 59),
		{ID: "liveentryaa", Title: "Radio", DurationS: 0, Live: true},
		vid("seedvideo02", "Alan Walker - Faded", "Alan Walker", 212), // another seed's own video
		vid("only1aaaaaa", "Only from seed 1", "c", 200),
	}
	e.yt.mix["seedvideo02"] = []ytdlp.Video{
		vid("seedvideo02", "Alan Walker - Faded", "Alan Walker", 212),
		vid("only2aaaaaa", "Only from seed 2", "c", 200),
		vid("sharedsugg1", "蔡琴 - 恰似你的温柔", "蔡琴", 247),
	}
	if err := e.svc.Refresh(ctx, u); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.List(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res.Items); got != "sharedsugg1,only2aaaaaa,failedother,only1aaaaaa" {
		t.Fatalf("items %s", got)
	}
	it := res.Items[0]
	// The strongest seed is the recent favorite (its recency bonus beats a play seed).
	if it.Reason == nil || it.Reason.TrackID != s2 || it.Reason.Kind != KindFavorite || it.Reason.Title != "Faded" || it.Reason.Artist != "Alan Walker" {
		t.Fatalf("reason %+v", it.Reason)
	}
	if it.Title != "蔡琴 - 恰似你的温柔" || it.Channel != "蔡琴" || it.DurationS != 247 ||
		it.URL != "https://www.youtube.com/watch?v=sharedsugg1" || it.Thumbnail != "https://i.ytimg.com/vi/sharedsugg1/hqdefault.jpg" {
		t.Fatalf("item %+v", it)
	}
	if res.Items[3].Reason.Kind != KindPlayed || res.RefreshedAt == nil || *res.RefreshedAt != t0.Unix() || res.Refreshing {
		t.Fatalf("%+v %+v", res.Items[3].Reason, res)
	}
	if e.gate.n == 0 {
		t.Fatal("yt-dlp work bypassed the limiter gate")
	}
	if _, m := e.yt.calls(); len(m) != 2 {
		t.Fatalf("mixes %v", m)
	}
}

func TestRefreshKeepsTopThirtyAndReplaces(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u")
	s := e.track("Seed", "x", 200)
	e.play(u, s, 3, t0, 200)
	e.download(u, "seedvideo01", "done", s)
	var vs []ytdlp.Video
	for i := range 60 {
		vs = append(vs, vid(fmt.Sprintf("candidat%03d", i), fmt.Sprintf("Song %d", i), "c", 200))
	}
	e.yt.mix["seedvideo01"] = vs
	if err := e.svc.Refresh(ctx, u); err != nil {
		t.Fatal(err)
	}
	res, _ := e.svc.List(ctx, u)
	if len(res.Items) != 30 || res.Items[0].VideoID != "candidat000" || res.Items[29].VideoID != "candidat029" {
		t.Fatalf("%d items %s", len(res.Items), ids(res.Items))
	}
	e.yt.mix["seedvideo01"] = []ytdlp.Video{vid("brandnewaaa", "New", "c", 200)}
	e.clock.Add(time.Hour)
	if err := e.svc.Refresh(ctx, u); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.svc.List(ctx, u); ids(res.Items) != "brandnewaaa" {
		t.Fatalf("not replaced: %s", ids(res.Items))
	}
}

// Every yt-dlp call failing: an error, and yesterday's list stays.
func TestRefreshFailureKeepsOldList(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u")
	s := e.track("Seed", "x", 200)
	e.play(u, s, 3, t0, 200)
	e.download(u, "seedvideo01", "done", s)
	e.yt.mix["seedvideo01"] = []ytdlp.Video{vid("keepmeaaaaa", "Keep", "c", 200)}
	if err := e.svc.Refresh(ctx, u); err != nil {
		t.Fatal(err)
	}
	e.yt.err = errBoom
	if err := e.svc.Refresh(ctx, u); err == nil {
		t.Fatal("want error")
	}
	if res, _ := e.svc.List(ctx, u); ids(res.Items) != "keepmeaaaaa" {
		t.Fatalf("lost the old list: %s", ids(res.Items))
	}
}

// A user without plays or favorites gets an empty list and no yt-dlp work.
func TestRefreshWithoutSeeds(t *testing.T) {
	e := newEnv(t)
	u := e.user("u")
	if err := e.svc.Refresh(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	s, m := e.yt.calls()
	if len(s)+len(m) != 0 {
		t.Fatal("yt-dlp ran without seeds")
	}
	res, _ := e.svc.List(context.Background(), u)
	if len(res.Items) != 0 || res.Items == nil {
		t.Fatalf("%+v", res)
	}
}

// Last.fm similar tracks (top 5 per seed) are resolved to YouTube through
// the cached search; artist.getSimilar is the fallback; agreement with a
// Mix adds weight.
func TestLastFMCandidates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u")
	s1, s2 := e.track("甜蜜蜜", "邓丽君", 213), e.track("Obscure", "Nobody", 200)
	e.play(u, s1, 5, t0, 200)
	e.play(u, s2, 4, t0, 200)
	e.download(u, "seedvideo01", "done", s1)
	e.download(u, "seedvideo02", "done", s2)
	sim := &fakeSimilar{tracks: map[string][]lastfm.SimilarTrack{"邓丽君|甜蜜蜜": {
		{Title: "恰似你的温柔", Artist: "蔡琴", Match: 1}, {Title: "S2", Artist: "A"}, {Title: "S3", Artist: "A"},
		{Title: "S4", Artist: "A"}, {Title: "S5", Artist: "A"}, {Title: "S6", Artist: "A"}, {Title: "S7", Artist: "A"},
	}}, artists: map[string][]lastfm.SimilarArtist{"Nobody": {{Name: "Somebody"}, {Name: "Anybody"}, {Name: "Everybody"}}}}
	e.svc.LastFM = sim
	e.yt.search["恰似你的温柔 蔡琴"] = []ytdlp.Video{vid("compilation", "蔡琴 合集", "c", 200), vid("sharedsugg1", "蔡琴 - 恰似你的温柔", "蔡琴", 247)}
	e.yt.search["S2 A"] = []ytdlp.Video{vid("lastfms2aaa", "A - S2", "A", 180)}
	e.yt.search["Somebody"] = []ytdlp.Video{vid("somebodyaaa", "Somebody - Hit", "Somebody", 200)}
	e.yt.mix["seedvideo01"] = []ytdlp.Video{vid("mixonlyaaaa", "Song M", "c", 200), vid("sharedsugg1", "蔡琴 - 恰似你的温柔", "蔡琴", 247)}
	if err := e.svc.Refresh(ctx, u); err != nil {
		t.Fatal(err)
	}
	res, _ := e.svc.List(ctx, u)
	if got := ids(res.Items); got != "sharedsugg1,mixonlyaaaa,somebodyaaa,lastfms2aaa" {
		t.Fatalf("items %s", got)
	}
	if res.Items[0].Score != 1.5 {
		t.Fatalf("agreement score %v", res.Items[0].Score)
	}
	searches, _ := e.yt.calls()
	// 5 similar tracks + 2 similar artists searched; S6, S7 and Everybody never.
	if len(searches) != 7 {
		t.Fatalf("searches %v", searches)
	}
	e.clock.Add(time.Hour)
	if err := e.svc.Refresh(ctx, u); err != nil {
		t.Fatal(err)
	}
	if again, _ := e.yt.calls(); len(again) != 7 {
		t.Fatalf("cached searches ran again: %v", again[7:])
	}
}

// Last.fm being down only loses its suggestions; the Mixes still count.
func TestLastFMOutageIsNotFatal(t *testing.T) {
	e := newEnv(t)
	u := e.user("u")
	s := e.track("Seed", "x", 200)
	e.play(u, s, 3, t0, 200)
	e.download(u, "seedvideo01", "done", s)
	e.svc.LastFM = &fakeSimilar{err: fmt.Errorf("%w: down", lastfm.ErrUnavailable)}
	e.yt.mix["seedvideo01"] = []ytdlp.Video{vid("mixonlyaaaa", "Song M", "c", 200)}
	if err := e.svc.Refresh(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.svc.List(context.Background(), u); ids(res.Items) != "mixonlyaaaa" {
		t.Fatalf("%s", ids(res.Items))
	}
}

func TestDismissIsPermanentAndPerUser(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, v := e.user("u"), e.user("v")
	for _, usr := range []int64{u, v} {
		s := e.track(fmt.Sprintf("Seed%d", usr), "x", 200)
		e.play(usr, s, 3, t0, 200)
		e.download(usr, fmt.Sprintf("seedvideo%02d", usr), "done", s)
		e.yt.mix[fmt.Sprintf("seedvideo%02d", usr)] = []ytdlp.Video{vid("sharedsugg1", "Shared", "c", 200), vid("secondaaaaa", "Second", "c", 200)}
		if err := e.svc.Refresh(ctx, usr); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.Dismiss(ctx, u, "sharedsugg1"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Dismiss(ctx, u, "not-an-id"); !errors.Is(err, ErrBadVideo) {
		t.Fatalf("bad id: %v", err)
	}
	if res, _ := e.svc.List(ctx, u); ids(res.Items) != "secondaaaaa" {
		t.Fatalf("u: %s", ids(res.Items))
	}
	if res, _ := e.svc.List(ctx, v); ids(res.Items) != "sharedsugg1,secondaaaaa" {
		t.Fatalf("v affected: %s", ids(res.Items))
	}
	e.clock.Add(time.Hour)
	e.svc.Refresh(ctx, u)
	if res, _ := e.svc.List(ctx, u); ids(res.Items) != "secondaaaaa" {
		t.Fatalf("dismissed came back: %s", ids(res.Items))
	}
}

// Downloaded after the refresh: hidden on the next load.
func TestListHidesFinishedDownloads(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u")
	s := e.track("Seed", "x", 200)
	e.play(u, s, 3, t0, 200)
	e.download(u, "seedvideo01", "done", s)
	e.yt.mix["seedvideo01"] = []ytdlp.Video{vid("downloadme1", "D", "c", 200), vid("keepmeaaaaa", "K", "c", 200)}
	e.svc.Refresh(ctx, u)
	e.download(u, "downloadme1", "downloading", 0)
	if res, _ := e.svc.List(ctx, u); ids(res.Items) != "downloadme1,keepmeaaaaa" {
		t.Fatalf("hidden while still downloading: %s", ids(res.Items))
	}
	e.exec(`UPDATE downloads SET status='done' WHERE video_id='downloadme1'`)
	if res, _ := e.svc.List(ctx, u); ids(res.Items) != "keepmeaaaaa" {
		t.Fatalf("%s", ids(res.Items))
	}
}

// No time limit: a request while one is pending or running for that user is
// a no-op (deduped); otherwise it is queued at once — even in the same second
// the previous refresh finished.
func TestRequestDedupesWithoutTimeLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, v := e.user("u"), e.user("v")
	requested := func(id int64) int64 {
		var at int64
		e.db.QueryRow(`SELECT COALESCE(requested_at,0) FROM recommendation_users WHERE user_id=?`, id).Scan(&at)
		return at
	}
	if err := e.svc.Request(ctx, u); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.svc.List(ctx, u); !res.Refreshing {
		t.Fatal("a requested refresh is not shown as refreshing")
	}
	first := requested(u)
	e.clock.Add(time.Second)
	if err := e.svc.Request(ctx, u); err != nil { // pending: deduped
		t.Fatal(err)
	}
	if requested(u) != first {
		t.Fatal("a pending request was queued again")
	}
	if err := e.svc.Request(ctx, v); err != nil {
		t.Fatalf("another user is affected: %v", err)
	}
	// Running now: deduped too.
	e.svc.mu.Lock()
	e.svc.running = u
	e.svc.mu.Unlock()
	e.exec(`UPDATE recommendation_users SET refreshed_at=? WHERE user_id=?`, e.clock.Now().Unix(), u)
	if err := e.svc.Request(ctx, u); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.svc.List(ctx, u); !res.Refreshing {
		t.Fatal("running refresh not shown")
	}
	e.svc.mu.Lock()
	e.svc.running = 0
	e.svc.mu.Unlock()
	if res, _ := e.svc.List(ctx, u); res.Refreshing {
		t.Fatal("a request during the run was queued")
	}
	// Finished: a new request starts at once, same second included.
	if err := e.svc.Request(ctx, u); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.svc.List(ctx, u); !res.Refreshing {
		t.Fatal("a request right after a refresh was not queued")
	}
}

// Nothing new to suggest (every candidate filtered, or no seeds left): the
// user's current list stays; the try is recorded so nothing shows as refreshing.
func TestRefreshWithNothingNewKeepsTheList(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u")
	s := e.track("Seed", "x", 200)
	e.play(u, s, 3, t0, 200)
	e.download(u, "seedvideo01", "done", s)
	e.yt.mix["seedvideo01"] = []ytdlp.Video{vid("keepmeaaaaa", "Keep", "c", 200)}
	if err := e.svc.Refresh(ctx, u); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(time.Hour)
	e.svc.Request(ctx, u)
	e.yt.mix["seedvideo01"] = []ytdlp.Video{vid("compilation", "合集", "c", 200)}
	if err := e.svc.Refresh(ctx, u); err != nil {
		t.Fatal(err)
	}
	res, _ := e.svc.List(ctx, u)
	if ids(res.Items) != "keepmeaaaaa" || res.Refreshing || *res.RefreshedAt != t0.Unix() {
		t.Fatalf("%s refreshing=%v refreshed=%v", ids(res.Items), res.Refreshing, *res.RefreshedAt)
	}
}
