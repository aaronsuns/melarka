package recommend

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// Affinity: a favorite counts 1; each listen adds the square of how much of
// the track it played, a skip takes 0.5, and listens fade (half after 30 days,
// none after 90). Favorites mostly skipped, short plays and others' plays don't seed.
func TestSeedsRankByListeningAndFavorites(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, other := e.user("u"), e.user("other")
	full, favLoved := e.track("Full", "a1", 200), e.track("FavLoved", "a2", 200)
	half, old, favNew := e.track("Half", "a3", 200), e.track("Old", "a4", 200), e.track("FavNew", "a5", 200)
	favSkipped, ancient, short := e.track("FavSkipped", "a6", 200), e.track("Ancient", "a7", 200), e.track("Short", "a8", 200)
	gone, disliked, theirs := e.track("Gone", "a9", 200), e.track("Disliked", "a10", 200), e.track("Theirs", "a11", 200)
	e.play(u, full, 4, t0, 200)                   // 4
	e.fav(u, favLoved, t0.Add(-400*24*time.Hour)) // an old favorite still loved: 1 + 2
	e.play(u, favLoved, 2, t0, 200)
	e.play(u, half, 4, t0, 100)                      // 4 × 0.25 = 1
	e.play(u, old, 4, t0.Add(-60*24*time.Hour), 200) // 4 × 0.25 (two half-lives) = 1
	e.fav(u, favNew, t0)                             // 1
	e.fav(u, favSkipped, t0)                         // 1 − 3 × 0.5 < 0
	e.skip(u, favSkipped, 3, t0, 10)
	e.play(u, ancient, 9, t0.Add(-91*24*time.Hour), 200) // past 90 days
	e.play(u, short, 9, t0, 20)                          // stopped early, not skipped: says nothing
	e.play(u, gone, 9, t0, 200)
	e.exec(`UPDATE tracks SET status='trashed' WHERE id=?`, gone)
	e.play(u, disliked, 9, t0, 200)
	e.exec(`INSERT INTO dislikes(user_id,track_id,created_at) VALUES (?,?,0)`, u, disliked)
	e.play(other, theirs, 20, t0, 200)

	seeds, err := e.svc.Seeds(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range seeds {
		got = append(got, fmt.Sprintf("%s:%s", s.Title, s.Kind))
	}
	want := "[Full:played FavLoved:played Half:played Old:played FavNew:favorite]"
	if fmt.Sprint(got) != want {
		t.Fatalf("seeds %v, want %s", got, want)
	}
	if seeds[0].DurationS != 200 || seeds[0].Artist != "a1" {
		t.Fatalf("seed %+v", seeds[0])
	}
	// Weights run from 1 to 1.5 with affinity: the best 1.5, a lone favorite (1 of 4) 1.125.
	if seeds[0].Weight != 1.5 || seeds[4].Weight != 1.125 {
		t.Fatalf("weights %v %v", seeds[0].Weight, seeds[4].Weight)
	}
}

// Ten seeds: the five best always, five drawn by affinity (the same all day,
// another draw tomorrow), and never more than two of one artist.
func TestSeedsFiveBestThenADailyDrawWithAnArtistCap(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u")
	for i := range 30 {
		e.play(u, e.track(fmt.Sprintf("P%d", i), fmt.Sprintf("artist%d", i), 200), 40-i, t0, 200)
	}
	for i := range 4 {
		e.play(u, e.track(fmt.Sprintf("Same%d", i), "Same", 200), 100, t0, 200)
	}
	titles := func() []string {
		seeds, err := e.svc.Seeds(ctx, u)
		if err != nil || len(seeds) != 10 {
			t.Fatalf("%d %v", len(seeds), err)
		}
		var out []string
		for _, s := range seeds {
			out = append(out, s.Title)
		}
		return out
	}
	first := titles()
	if got := fmt.Sprint(first[:5]); got != "[Same0 Same1 P0 P1 P2]" {
		t.Fatalf("the best five %s", got)
	}
	for _, title := range first[5:] {
		if title == "Same2" || title == "Same3" {
			t.Fatalf("a third seed of one artist: %v", first)
		}
	}
	if again := titles(); fmt.Sprint(again) != fmt.Sprint(first) {
		t.Fatalf("another draw the same day: %v vs %v", again, first)
	}
	drawn := map[string]bool{}
	for range 7 {
		e.clock.Add(24 * time.Hour)
		day := titles()
		if fmt.Sprint(day[:5]) != fmt.Sprint(first[:5]) {
			t.Fatalf("the best five changed: %v", day)
		}
		for _, title := range day[5:] {
			drawn[title] = true
		}
	}
	if len(drawn) <= 5 {
		t.Fatalf("a week of draws used only %d tracks: %v", len(drawn), drawn)
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

// At most 20 seed searches per user per day.
func TestSeedSearchBudgetPerDay(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("u")
	for i := range 22 {
		tr := e.track(fmt.Sprintf("S%d", i), "x", 0)
		e.svc.seedVideo(ctx, u, Seed{TrackID: tr, Title: fmt.Sprintf("S%d", i), Artist: "x"})
	}
	if q, _ := e.yt.calls(); len(q) != 20 {
		t.Fatalf("searches %d, want 20", len(q))
	}
	e.clock.Add(24 * time.Hour)
	tr := e.track("Tomorrow", "x", 0)
	e.svc.seedVideo(ctx, u, Seed{TrackID: tr, Title: "Tomorrow", Artist: "x"})
	if q, _ := e.yt.calls(); len(q) != 21 {
		t.Fatalf("next day's search not run: %d", len(q))
	}
}
