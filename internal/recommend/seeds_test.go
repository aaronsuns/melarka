package recommend

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func TestSeedsMostPlayedThenNewestFavoritesDeduped(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, other := e.user("u"), e.user("other")
	a, b, c := e.track("A", "x", 200), e.track("B", "x", 200), e.track("C", "x", 200)
	short, old, gone := e.track("Short", "x", 200), e.track("Old", "x", 200), e.track("Gone", "x", 200)
	f1, f2 := e.track("F1", "y", 200), e.track("F2", "y", 200)
	e.play(u, b, 5, t0.Add(-time.Hour), 120)
	e.play(u, a, 3, t0.Add(-2*time.Hour), 120)
	e.play(u, c, 1, t0.Add(-24*time.Hour), 30)
	e.play(u, short, 9, t0.Add(-time.Hour), 29)      // under 30 s: not a real listen
	e.play(u, old, 9, t0.Add(-31*24*time.Hour), 200) // older than 30 days
	e.play(other, f1, 20, t0.Add(-time.Hour), 200)   // someone else's plays
	e.play(u, gone, 9, t0.Add(-time.Hour), 200)
	e.exec(`UPDATE tracks SET status='trashed' WHERE id=?`, gone)
	e.fav(u, f1, t0.Add(-48*time.Hour))
	e.fav(u, f2, t0.Add(-time.Hour)) // newest favorite first
	e.fav(u, a, t0)                  // already a played seed: not twice

	seeds, err := e.svc.Seeds(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range seeds {
		got = append(got, fmt.Sprintf("%s:%s", s.Title, s.Kind))
	}
	want := "[B:played A:played C:played F2:favorite F1:favorite]"
	if fmt.Sprint(got) != want {
		t.Fatalf("seeds %v, want %s", got, want)
	}
	if seeds[0].DurationS != 200 || seeds[0].Artist != "x" {
		t.Fatalf("seed %+v", seeds[0])
	}
	// A favorite seed earns a small recency bonus: newer beats older.
	if !(seeds[3].Weight > seeds[4].Weight && seeds[4].Weight >= 1 && seeds[0].Weight == 1) {
		t.Fatalf("weights %v %v %v", seeds[0].Weight, seeds[3].Weight, seeds[4].Weight)
	}
}

func TestSeedsCapAtTenMixingPlaysAndFavorites(t *testing.T) {
	e := newEnv(t)
	u := e.user("u")
	for i := range 12 {
		e.play(u, e.track(fmt.Sprintf("P%d", i), "x", 200), 20-i, t0.Add(-time.Hour), 100)
		e.fav(u, e.track(fmt.Sprintf("F%d", i), "y", 200), t0.Add(-time.Duration(i)*time.Hour))
	}
	seeds, err := e.svc.Seeds(context.Background(), u)
	if err != nil || len(seeds) != 10 {
		t.Fatalf("%d %v", len(seeds), err)
	}
	played, favs := 0, 0
	for _, s := range seeds {
		if s.Kind == KindPlayed {
			played++
		} else {
			favs++
		}
	}
	if played != 5 || favs != 5 || seeds[0].Title != "P0" {
		t.Fatalf("played %d favs %d first %s", played, favs, seeds[0].Title)
	}
	// Only favorites: all ten slots go to them.
	v := e.user("v")
	for i := range 12 {
		e.fav(v, e.track(fmt.Sprintf("G%d", i), "z", 200), t0)
	}
	if seeds, _ := e.svc.Seeds(context.Background(), v); len(seeds) != 10 {
		t.Fatalf("favorites-only seeds %d", len(seeds))
	}
}

// A seed that came from YouTube uses its download job's video id: no search.
func TestSeedVideoFromDownloadJob(t *testing.T) {
	e := newEnv(t)
	u := e.user("u")
	tr := e.track("甜蜜蜜", "邓丽君", 213)
	e.download(u, "seedvideo01", "done", tr)
	id, err := e.svc.seedVideo(context.Background(), u, Seed{TrackID: tr, Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 213})
	if err != nil || id != "seedvideo01" {
		t.Fatalf("%q %v", id, err)
	}
	if s, _ := e.yt.calls(); len(s) != 0 {
		t.Fatalf("searched %v", s)
	}
}

// Otherwise one search "title artist", the first result within ±10 s, cached:
// the track is never searched again; a miss is remembered for 30 days.
func TestSeedVideoSearchPicksDurationMatchAndCaches(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u")
	tr := e.track("甜蜜蜜", "邓丽君", 213)
	e.yt.search["甜蜜蜜 邓丽君"] = []ytdlp.Video{vid("longversion", "甜蜜蜜 1小时", "c", 3600), vid("rightlength", "甜蜜蜜", "c", 220)}
	s := Seed{TrackID: tr, Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 213}
	for range 2 {
		id, err := e.svc.seedVideo(ctx, u, s)
		if err != nil || id != "rightlength" {
			t.Fatalf("%q %v", id, err)
		}
	}
	if q, _ := e.yt.calls(); len(q) != 1 {
		t.Fatalf("searches %v", q)
	}

	miss := e.track("无名", "无名氏", 100)
	ms := Seed{TrackID: miss, Title: "无名", Artist: "无名氏", DurationS: 100}
	e.yt.search["无名 无名氏"] = []ytdlp.Video{vid("wronglength", "无名", "c", 300)}
	for range 2 {
		if id, err := e.svc.seedVideo(ctx, u, ms); err != nil || id != "" {
			t.Fatalf("miss: %q %v", id, err)
		}
	}
	if q, _ := e.yt.calls(); len(q) != 2 {
		t.Fatalf("a miss was searched again: %v", q)
	}
	e.clock.Add(31 * 24 * time.Hour)
	e.svc.seedVideo(ctx, u, ms)
	if q, _ := e.yt.calls(); len(q) != 3 {
		t.Fatalf("a 31-day-old miss was not retried: %v", q)
	}
	// Unknown duration: the first result is taken.
	unk := e.track("Unknown", "Dur", 0)
	e.yt.search["Unknown Dur"] = []ytdlp.Video{vid("firstresult", "Unknown", "c", 999)}
	if id, _ := e.svc.seedVideo(ctx, u, Seed{TrackID: unk, Title: "Unknown", Artist: "Dur"}); id != "firstresult" {
		t.Fatalf("unknown duration: %q", id)
	}
}

// A failed search is an error, not a remembered miss.
func TestSeedVideoSearchErrorIsNotAMiss(t *testing.T) {
	e := newEnv(t)
	u := e.user("u")
	tr := e.track("T", "A", 100)
	e.yt.err = errBoom
	if _, err := e.svc.seedVideo(context.Background(), u, Seed{TrackID: tr, Title: "T", Artist: "A"}); err == nil {
		t.Fatal("want error")
	}
	var n int
	e.db.QueryRow(`SELECT COUNT(*) FROM seed_videos`).Scan(&n)
	if n != 0 {
		t.Fatal("error cached as a miss")
	}
}

// At most 10 seed searches per user per day.
func TestSeedSearchBudgetPerDay(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u")
	for i := range 12 {
		tr := e.track(fmt.Sprintf("S%d", i), "x", 0)
		e.svc.seedVideo(ctx, u, Seed{TrackID: tr, Title: fmt.Sprintf("S%d", i), Artist: "x"})
	}
	if q, _ := e.yt.calls(); len(q) != 10 {
		t.Fatalf("searches %d, want 10", len(q))
	}
	e.clock.Add(24 * time.Hour)
	tr := e.track("Tomorrow", "x", 0)
	e.svc.seedVideo(ctx, u, Seed{TrackID: tr, Title: "Tomorrow", Artist: "x"})
	if q, _ := e.yt.calls(); len(q) != 11 {
		t.Fatalf("next day's search not run: %d", len(q))
	}
}
