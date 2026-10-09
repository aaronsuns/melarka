package channels

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

const (
	chX = "UCxxxxxxxxxxxxxxxxxxxxxx"
	chY = "UCyyyyyyyyyyyyyyyyyyyyyy"
	chZ = "UCzzzzzzzzzzzzzzzzzzzzzz"
)

func mv(id, ch string, dur int) ytdlp.Video {
	return ytdlp.Video{ID: id, Title: "Video " + id, Channel: "Name " + ch[2:5], ChannelID: ch, DurationS: dur, URL: ytdlp.WatchURL(id)}
}

type countGate struct {
	mu sync.Mutex
	n  int
}

func (g *countGate) Low(ctx context.Context, fn func(context.Context) error) error {
	g.mu.Lock()
	g.n++
	g.mu.Unlock()
	return fn(ctx)
}

// seedDiscovery: anna follows A and B; A's newest episode is vid 1, B's vid 2.
func seedDiscovery(t *testing.T) (*env, int64) {
	e := newEnv(t)
	a := e.user("anna")
	e.follow(a, chA)
	e.follow(a, chB)
	e.seedQueued(chA, 1, time.Hour)
	e.seedQueued(chA, 5, 50*time.Hour)
	e.seedQueued(chB, 2, 2*time.Hour)
	e.yt.mix[vid(1)] = []ytdlp.Video{mv(vid(1), chA, 900), mv("mixvid00001", chX, 600), mv("mixvid00002", chX, 700),
		mv("mixvid00003", chY, 800), mv("mixvid00004", chB, 800)}
	e.yt.mix[vid(2)] = []ytdlp.Video{mv("mixvid00005", chY, 500), mv("mixvid00006", chZ, 400)}
	return e, a
}

func TestDiscoverRanksByFollowedChannelsPointingThere(t *testing.T) {
	e, a := seedDiscovery(t)
	g := &countGate{}
	e.svc.Gate = g
	ctx := context.Background()
	if err := e.svc.Discover(ctx, a); err != nil {
		t.Fatal(err)
	}
	sg, err := e.svc.Suggestions(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	var chans, vids []string
	for _, c := range sg.Channels {
		chans = append(chans, c.ID)
	}
	for _, v := range sg.Videos {
		vids = append(vids, v.VideoID)
	}
	if !slices.Equal(chans, []string{chY, chX, chZ}) {
		t.Fatalf("channels %v (Y is reached from both A and B)", chans)
	}
	if sg.Channels[0].Score != 2 || sg.Channels[0].SampleVideoID != "mixvid00003" || sg.Channels[0].Title != "Name yyy" {
		t.Fatalf("%+v", sg.Channels[0])
	}
	// All appear in one Mix each: best Mix position first, then id.
	if !slices.Equal(vids, []string{"mixvid00005", "mixvid00001", "mixvid00006", "mixvid00002", "mixvid00003"}) {
		t.Fatalf("videos %v (never the seed or a followed channel's)", vids)
	}
	if sg.Videos[0].Thumbnail != "https://i.ytimg.com/vi/mixvid00005/hqdefault.jpg" || sg.RefreshedAt == nil {
		t.Fatalf("%+v", sg)
	}
	if g.n != 3 || !slices.Equal(e.yt.mixCalls, []string{vid(1), vid(2), vid(5)}) {
		t.Fatalf("one Mix per seed through the low-priority gate: %d %v", g.n, e.yt.mixCalls)
	}
}

func TestDiscoverFilters(t *testing.T) {
	e, a := seedDiscovery(t)
	ctx := context.Background()
	e.yt.mix[vid(2)] = append(e.yt.mix[vid(2)], mv("mixvid00007", chZ, 30), mv("mixvid00008", chZ, 20000),
		ytdlp.Video{ID: "mixvid00009", Title: "Live now", ChannelID: chZ, DurationS: 0, Live: true, URL: ytdlp.WatchURL("mixvid00009")},
		ytdlp.Video{ID: "mixvid00010", Title: "经典老歌合集", ChannelID: chZ, DurationS: 3000, URL: ytdlp.WatchURL("mixvid00010")},
		mv("mixvid00011", chZ, 300))
	e.exec(`INSERT INTO downloads(user_id,url,video_id,status,created_at,updated_at) VALUES (?,?,?,'done',1,1)`, a, ytdlp.WatchURL("mixvid00011"), "mixvid00011")
	if err := e.svc.DismissSuggestion(ctx, a, "channel", chX); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DismissSuggestion(ctx, a, "video", "mixvid00005"); err != nil {
		t.Fatal(err)
	}
	e.svc.Discover(ctx, a)
	sg, _ := e.svc.Suggestions(ctx, a)
	for _, c := range sg.Channels {
		if c.ID == chX {
			t.Fatal("dismissed channel suggested")
		}
	}
	for _, v := range sg.Videos {
		switch v.VideoID {
		case "mixvid00005", "mixvid00007", "mixvid00008", "mixvid00009", "mixvid00010", "mixvid00011", "mixvid00001", "mixvid00002":
			t.Fatalf("%s must be filtered (dismissed, too short/long, live, compilation, in the library, or of a dismissed channel)", v.VideoID)
		}
	}
	if err := e.svc.DismissSuggestion(ctx, a, "video", "../etc"); !errors.Is(err, ErrBadID) {
		t.Fatal(err)
	}
	// Following a suggested channel removes it from the list at once.
	e.follow(a, chY)
	if sg, _ := e.svc.Suggestions(ctx, a); len(sg.Channels) != 1 || sg.Channels[0].ID != chZ {
		t.Fatalf("%+v", sg.Channels)
	}
	if sg, _ := e.svc.Suggestions(ctx, e.user("bo")); len(sg.Channels)+len(sg.Videos) != 0 {
		t.Fatal("suggestions are per user")
	}
}

func TestDiscoverKeepsTheOldListWhenEveryMixFails(t *testing.T) {
	e, a := seedDiscovery(t)
	ctx := context.Background()
	e.svc.Discover(ctx, a)
	e.yt.mixErr = errors.New("yt-dlp: Sign in to confirm you're not a bot")
	if err := e.svc.Discover(ctx, a); err == nil {
		t.Fatal("want an error")
	}
	if sg, _ := e.svc.Suggestions(ctx, a); len(sg.Channels) != 3 {
		t.Fatal("the previous list stays")
	}
}

func TestRequestDiscoveryDedupesAndBudgets(t *testing.T) {
	e, a := seedDiscovery(t)
	ctx := context.Background()
	if err := e.svc.RequestDiscovery(ctx, a); err != nil {
		t.Fatal(err)
	}
	if sg, _ := e.svc.Suggestions(ctx, a); !sg.Refreshing {
		t.Fatal("pending shows as refreshing")
	}
	if err := e.svc.RequestDiscovery(ctx, a); err != nil {
		t.Fatalf("a request while one is pending is joined: %v", err)
	}
	if e.count(`SELECT COUNT(*) FROM discovery_users WHERE requested_at IS NOT NULL`) != 1 {
		t.Fatal("one row")
	}
	// 3 Mixes per refresh, 50 a day: the 17th refresh passes 50, the 18th request is refused.
	for i := range 17 {
		if err := e.svc.Discover(ctx, a); err != nil {
			t.Fatal(err)
		}
		if i < 16 {
			e.clock.Add(time.Second)
			if err := e.svc.RequestDiscovery(ctx, a); err != nil {
				t.Fatalf("run %d: %v", i+2, err)
			}
		}
	}
	e.clock.Add(time.Second)
	if err := e.svc.RequestDiscovery(ctx, a); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("past the daily budget: %v", err)
	}
	e.clock.Add(24 * time.Hour)
	if err := e.svc.RequestDiscovery(ctx, a); err != nil {
		t.Fatalf("a new day: %v", err)
	}
}

func TestFailedMixesCountAgainstTheBudget(t *testing.T) {
	e, a := seedDiscovery(t)
	ctx := context.Background()
	e.yt.mixErr = errors.New("boom")
	e.svc.Discover(ctx, a)
	if n := e.count(`SELECT mixes FROM discovery_users WHERE user_id=?`, a); n != 3 {
		t.Fatalf("mixes=%d", n)
	}
}

func TestDiscoveryIsPerUser(t *testing.T) {
	e, a := seedDiscovery(t)
	ctx := context.Background()
	b := e.user("bo")
	e.exec(`INSERT INTO downloads(user_id,url,video_id,status,created_at,updated_at) VALUES (?,?,?,'done',1,1)`, b, ytdlp.WatchURL("mixvid00005"), "mixvid00005")
	if err := e.svc.Discover(ctx, a); err != nil {
		t.Fatal(err)
	}
	sg, _ := e.svc.Suggestions(ctx, a)
	if !slices.ContainsFunc(sg.Videos, func(v SuggestedVideo) bool { return v.VideoID == "mixvid00005" }) {
		t.Fatalf("bo's download hid anna's suggestion: %+v", sg.Videos)
	}
	e.exec(`INSERT INTO downloads(user_id,url,video_id,status,created_at,updated_at) VALUES (?,?,?,'done',1,1)`, a, ytdlp.WatchURL("mixvid00005"), "mixvid00005")
	if sg, _ := e.svc.Suggestions(ctx, a); slices.ContainsFunc(sg.Videos, func(v SuggestedVideo) bool { return v.VideoID == "mixvid00005" }) {
		t.Fatal("her own download hides it")
	}
}

func TestDismissingAChannelHidesItsVideos(t *testing.T) {
	e, a := seedDiscovery(t)
	ctx := context.Background()
	e.svc.Discover(ctx, a)
	if err := e.svc.DismissSuggestion(ctx, a, "channel", chX); err != nil {
		t.Fatal(err)
	}
	sg, _ := e.svc.Suggestions(ctx, a)
	for _, v := range sg.Videos {
		if v.ChannelID == chX {
			t.Fatalf("%+v", v)
		}
	}
}

func TestDiscoverStepFailureAndRequests(t *testing.T) {
	e, a := seedDiscovery(t)
	ctx := context.Background()
	e.svc.RefreshAt = "23:00" // not due: only requests run
	if err := e.svc.RequestDiscovery(ctx, a); err != nil {
		t.Fatal(err)
	}
	e.yt.mixErr = errors.New("boom")
	if w := e.svc.refreshStep(ctx); w != time.Minute {
		t.Fatalf("failure backs off: %s", w)
	}
	if sg, _ := e.svc.Suggestions(ctx, a); sg.Refreshing || e.count(`SELECT COUNT(*) FROM discovery_users WHERE attempted_at IS NOT NULL`) != 1 {
		t.Fatal("the failed attempt ends the pending state")
	}
	e.clock.Add(time.Second)
	e.yt.mixErr = nil
	e.svc.RequestDiscovery(ctx, a)
	e.svc.refreshStep(ctx)
	if sg, _ := e.svc.Suggestions(ctx, a); sg.RefreshedAt == nil || sg.Refreshing {
		t.Fatalf("the pending request was served: %+v", sg)
	}
}

func TestDiscoverStepNightly(t *testing.T) {
	e, a := seedDiscovery(t)
	ctx := context.Background()
	e.svc.RefreshAt = "13:00" // t0 is 12:00 UTC
	if w := e.svc.refreshStep(ctx); w != time.Hour || len(e.yt.mixCalls) != 0 {
		t.Fatalf("before the nightly time: %s %v", w, e.yt.mixCalls)
	}
	e.clock.Add(90 * time.Minute)
	e.svc.refreshStep(ctx)
	if sg, _ := e.svc.Suggestions(ctx, a); sg.RefreshedAt == nil {
		t.Fatal("nightly refresh ran for anna")
	}
	e.yt.mixCalls = nil
	e.svc.refreshStep(ctx) // nobody else: the night is marked done
	e.svc.refreshStep(ctx)
	if e.count(`SELECT mixes FROM discovery_users WHERE user_id=?`, a) != 0 {
		t.Fatal("the nightly run doesn't use the budget")
	}
	if len(e.yt.mixCalls) != 0 || e.count(`SELECT COUNT(*) FROM app_state WHERE key='channels.discovery_day'`) != 1 {
		t.Fatal("one nightly run per day")
	}
}

func TestRequestAfterNightlyRefresh(t *testing.T) {
	e, a := seedDiscovery(t)
	ctx := context.Background()
	e.svc.RefreshAt = "11:00" // t0 is 12:00: due
	e.svc.refreshStep(ctx)    // nightly: the row has no requested_at
	if e.count(`SELECT COUNT(*) FROM discovery_users WHERE user_id=? AND requested_at IS NULL`, a) != 1 {
		t.Fatal("setup: expected a row without requested_at")
	}
	e.clock.Add(time.Second)
	if err := e.svc.RequestDiscovery(ctx, a); err != nil {
		t.Fatalf("refresh after a nightly run: %v", err)
	}
	if sg, _ := e.svc.Suggestions(ctx, a); !sg.Refreshing {
		t.Fatal("pending")
	}
}
