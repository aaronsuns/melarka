package channels

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRecordWatchAndSearch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	anna, bo := e.user("anna"), e.user("bo")
	v := ytdlp.Video{ID: "rvOZh8idOrU", Title: "<b>投行</b>", Channel: "刘翔的投资频道", ChannelID: "UC0e5c4U67Vm6sAVK0vxN3Uw", DurationS: 880}
	must(t, e.svc.RecordWatch(ctx, anna, v))
	e.clock.Add(time.Minute)
	must(t, e.svc.RecordWatch(ctx, anna, v)) // twice: one row, times=2, newest
	must(t, e.svc.RecordVideoSearch(ctx, anna, "  美债  危机 ", "PncPZ-E1GjE"))
	must(t, e.svc.RecordVideoSearch(ctx, anna, "美债 危机", "PncPZ-E1GjE")) // same key
	h, err := e.svc.VideoHistory(ctx, anna)
	must(t, err)
	if len(h.Watches) != 1 || h.Watches[0].Title != "<b>投行</b>" || h.Watches[0].Thumbnail != "/api/v1/videos/rvOZh8idOrU/thumbnail" ||
		h.Watches[0].ChannelID != v.ChannelID || h.Watches[0].DurationS != 880 || h.Watches[0].LastAt != e.clock.Now().Unix() {
		t.Fatalf("%+v", h.Watches)
	}
	if !slices.Equal(h.Searches, []string{"美债 危机"}) {
		t.Fatalf("%v", h.Searches)
	}
	if n := e.count(`SELECT times FROM video_history WHERE user_id=? AND kind='watch'`, anna); n != 2 {
		t.Errorf("times %d", n)
	}
	if n := e.count(`SELECT times FROM video_history WHERE user_id=? AND kind='search'`, anna); n != 2 {
		t.Errorf("search times %d", n)
	}
	if other, _ := e.svc.VideoHistory(ctx, bo); len(other.Watches)+len(other.Searches) != 0 || other.Watches == nil || other.Searches == nil {
		t.Errorf("history leaked to another user (or nil lists): %+v", other)
	}
	if err := e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "../../etc"}); !errors.Is(err, ErrBadID) {
		t.Errorf("bad id: %v", err)
	}
	if err := e.svc.RecordVideoSearch(ctx, anna, strings.Repeat("长", 101), ""); !errors.Is(err, ErrBadID) {
		t.Errorf("101-rune query: %v", err)
	}
	if err := e.svc.RecordVideoSearch(ctx, anna, "   ", ""); !errors.Is(err, ErrBadID) {
		t.Errorf("empty query: %v", err)
	}
	// A search without a valid first result keeps no seed, and never wipes an earlier one.
	must(t, e.svc.RecordVideoSearch(ctx, anna, "美债 危机", "not-an-id"))
	var seed string
	e.db.QueryRow(`SELECT seed_video_id FROM video_history WHERE user_id=? AND kind='search'`, anna).Scan(&seed)
	if seed != "PncPZ-E1GjE" {
		t.Errorf("seed %q", seed)
	}
	must(t, e.svc.RecordVideoSearch(ctx, anna, "Hello  World", ""))
	e.db.QueryRow(`SELECT seed_video_id FROM video_history WHERE user_id=? AND item='hello world'`, anna).Scan(&seed)
	if seed != "" {
		t.Errorf("seed %q", seed)
	}
	if h, _ := e.svc.VideoHistory(ctx, anna); !slices.Equal(h.Searches, []string{"Hello World", "美债 危机"}) {
		t.Errorf("%v", h.Searches)
	}
}

func TestHistoryPrune(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	anna := e.user("anna")
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "rvOZh8idOrU", Title: "old"}))
	must(t, e.svc.putMix(ctx, "rvOZh8idOrU", []ytdlp.Video{{ID: "PncPZ-E1GjE"}}, e.clock.Now().Unix()))
	e.clock.Add(91 * 24 * time.Hour)
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "PncPZ-E1GjE", Title: "new"}))
	must(t, e.svc.putMix(ctx, "PncPZ-E1GjE", []ytdlp.Video{{ID: "rvOZh8idOrU"}}, e.clock.Now().Unix()))
	n, err := e.svc.pruneVideo(ctx)
	must(t, err)
	if n != 2 { // the 91-day-old watch and the 91-day-old Mix
		t.Fatalf("pruned %d", n)
	}
	if h, _ := e.svc.VideoHistory(ctx, anna); len(h.Watches) != 1 || h.Watches[0].Title != "new" {
		t.Fatalf("%+v", h.Watches)
	}
	// Per-user caps: watch 501 drops the oldest; search 201 likewise.
	for i := range 500 {
		e.exec(`INSERT INTO video_history(user_id,kind,item,last_at) VALUES (?,'watch',?,?)`, anna, fmt.Sprintf("w%010d", i), int64(i))
		e.exec(`INSERT INTO video_history(user_id,kind,item,last_at) VALUES (?,'search',?,?)`, anna, fmt.Sprintf("s%d", i), int64(i))
	}
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "zzzzzzzzzzz"}))
	must(t, e.svc.RecordVideoSearch(ctx, anna, "latest", ""))
	if n := e.count(`SELECT COUNT(*) FROM video_history WHERE user_id=? AND kind='watch'`, anna); n != 500 {
		t.Errorf("watches %d", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM video_history WHERE user_id=? AND kind='search'`, anna); n != 200 {
		t.Errorf("searches %d", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM video_history WHERE user_id=? AND item IN ('zzzzzzzzzzz','PncPZ-E1GjE','latest')`, anna); n != 3 {
		t.Errorf("the newest rows must stay: %d", n)
	}
	// Once a day from the refresh loop.
	e.svc.RefreshAt = "23:59"
	e.clock.Add(100 * 24 * time.Hour)
	e.svc.refreshStep(ctx)
	if n := e.count(`SELECT COUNT(*) FROM video_history`); n != 0 {
		t.Errorf("daily prune: %d left", n)
	}
	if e.count(`SELECT COUNT(*) FROM app_state WHERE key='video.prune_day' AND value=?`, e.clock.Now().Format(time.DateOnly)) != 1 {
		t.Error("prune day not recorded")
	}
}

func TestCachedMix(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	calls := 0
	var fetchErr error
	fetch := func(context.Context) ([]ytdlp.Video, error) {
		calls++
		if fetchErr != nil {
			return nil, fetchErr
		}
		return []ytdlp.Video{{ID: "PncPZ-E1GjE", Title: "a"}, {ID: "live0000000", Live: true}, {ID: "bad"}}, nil
	}
	got, err := e.svc.CachedMix(ctx, "rvOZh8idOrU", fetch)
	must(t, err)
	got2, _ := e.svc.CachedMix(ctx, "rvOZh8idOrU", fetch)
	if calls != 1 || len(got) != 1 || len(got2) != 1 || got2[0].ID != "PncPZ-E1GjE" || got2[0].Title != "a" {
		t.Fatalf("calls %d got %v %v", calls, got, got2)
	}
	e.clock.Add(25 * time.Hour)
	e.svc.CachedMix(ctx, "rvOZh8idOrU", fetch)
	if calls != 2 {
		t.Errorf("stale cache not refetched")
	}
	// A failed fetch with a stale row answers the stale entries.
	e.clock.Add(25 * time.Hour)
	fetchErr = errors.New("yt-dlp: boom")
	got, err = e.svc.CachedMix(ctx, "rvOZh8idOrU", fetch)
	if err != nil || len(got) != 1 || calls != 3 {
		t.Fatalf("stale fallback: %v %v %d", got, err, calls)
	}
	// Without a row the error is returned.
	if _, err := e.svc.CachedMix(ctx, "PncPZ-E1GjE", fetch); err == nil {
		t.Fatal("want the fetch error")
	}
	if _, err := e.svc.CachedMix(ctx, "../etc", fetch); !errors.Is(err, ErrBadID) {
		t.Fatalf("bad id: %v", err)
	}
}

// Production fix R2: a talk channel's Mix often holds only the video itself.
// With fewer than 5 other entries, the seed channel's latest uploads (its
// RSS feed, no yt-dlp) are appended: not the seed, not a Short, not one
// already there; their duration is unknown (0).
func TestThinMixFallsBackToTheChannelFeed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	seed := "rvOZh8idOrU"
	fetches := 0
	mix := []ytdlp.Video{
		{ID: seed, Title: "seed", Channel: "刘翔的投资频道", ChannelID: chA, DurationS: 880},
		{ID: "PncPZ-E1GjE", Title: "other", ChannelID: chB, DurationS: 300},
	}
	fetch := func(context.Context) ([]ytdlp.Video, error) { fetches++; return mix, nil }
	short := e.entry(1, time.Hour)
	short.Short = true
	e.feeds.feeds[chA] = Feed{Title: "刘翔的投资频道", Entries: []FeedEntry{
		{VideoID: seed, Title: "seed again"}, short, e.entry(2, 2*time.Hour), {VideoID: "PncPZ-E1GjE", Title: "dup"}, e.entry(3, 3*time.Hour)}}
	got, err := e.svc.CachedMix(ctx, seed, fetch)
	must(t, err)
	ids := func(vs []ytdlp.Video) []string {
		var out []string
		for _, v := range vs {
			out = append(out, v.ID)
		}
		return out
	}
	if want := []string{seed, "PncPZ-E1GjE", vid(2), vid(3)}; !slices.Equal(ids(got), want) {
		t.Fatalf("%v, want %v", ids(got), want)
	}
	if g := got[2]; g.Title != "Episode 2" || g.Channel != "刘翔的投资频道" || g.ChannelID != chA || g.DurationS != 0 ||
		g.URL != ytdlp.WatchURL(vid(2)) || g.Thumbnail != ytdlp.ThumbnailURL(vid(2)) {
		t.Errorf("%+v", g)
	}
	// Cached with the fallback: no second fetch of either.
	again, _ := e.svc.CachedMix(ctx, seed, fetch)
	if fetches != 1 || len(e.feeds.calls) != 1 || !slices.Equal(ids(again), ids(got)) {
		t.Fatalf("fetches %d feeds %v again %v", fetches, e.feeds.calls, ids(again))
	}

	// Five or more others: the Mix as it is, no feed read.
	e.feeds.calls = nil
	full := []ytdlp.Video{{ID: "AAAAAAAAAAA", ChannelID: chA}}
	for i := range 5 {
		full = append(full, ytdlp.Video{ID: vid(10 + i)})
	}
	got, _ = e.svc.CachedMix(ctx, "AAAAAAAAAAA", func(context.Context) ([]ytdlp.Video, error) { return full, nil })
	if len(got) != 6 || len(e.feeds.calls) != 0 {
		t.Fatalf("%v %v", ids(got), e.feeds.calls)
	}

	// The seed's channel known from a watch, the Mix empty.
	anna := e.user("anna")
	must(t, e.svc.RecordWatch(ctx, anna, ytdlp.Video{ID: "BBBBBBBBBBB", Title: "B", ChannelID: chB}))
	e.feeds.feeds[chB] = Feed{Title: "B channel", Entries: []FeedEntry{e.entry(20, time.Hour)}}
	got, _ = e.svc.CachedMix(ctx, "BBBBBBBBBBB", func(context.Context) ([]ytdlp.Video, error) { return nil, nil })
	if !slices.Equal(ids(got), []string{vid(20)}) || got[0].Channel != "B channel" {
		t.Fatalf("%+v", got)
	}

	// No channel known: nothing to add. A feed that fails: the Mix as it is.
	e.feeds.calls = nil
	got, err = e.svc.CachedMix(ctx, "CCCCCCCCCCC", func(context.Context) ([]ytdlp.Video, error) { return []ytdlp.Video{{ID: vid(30)}}, nil })
	if err != nil || !slices.Equal(ids(got), []string{vid(30)}) || len(e.feeds.calls) != 0 {
		t.Fatalf("%v %v %v", ids(got), err, e.feeds.calls)
	}
	e.feeds.errs[chB] = errors.New("feed: 500")
	got, err = e.svc.CachedMix(ctx, "DDDDDDDDDDD", func(context.Context) ([]ytdlp.Video, error) {
		return []ytdlp.Video{{ID: "DDDDDDDDDDD", ChannelID: chB}, {ID: vid(31)}}, nil
	})
	if err != nil || !slices.Equal(ids(got), []string{"DDDDDDDDDDD", vid(31)}) {
		t.Fatalf("%v %v", ids(got), err)
	}
}

// A thin Mix cached before the fallback existed (fresh for a day) gets it
// when read; the row keeps its fetched time, so the Mix is still refetched
// on schedule.
func TestThinCachedMixGetsTheFallbackOnRead(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	seed := "rvOZh8idOrU"
	must(t, e.svc.putMix(ctx, seed, []ytdlp.Video{{ID: seed, Title: "seed", ChannelID: chA, DurationS: 880}}, e.clock.Now().Unix()))
	at := e.clock.Now().Unix()
	e.clock.Add(time.Hour)
	e.feeds.feeds[chA] = Feed{Title: "C", Entries: []FeedEntry{e.entry(1, time.Hour), e.entry(2, time.Hour)}}
	got, err := e.svc.CachedMix(ctx, seed, func(context.Context) ([]ytdlp.Video, error) {
		t.Fatal("a fresh row is not refetched")
		return nil, nil
	})
	if err != nil || len(got) != 3 || got[1].ID != vid(1) {
		t.Fatalf("%+v %v", got, err)
	}
	if e.count(`SELECT COUNT(*) FROM video_mixes WHERE video_id=? AND fetched_at=?`, seed, at) != 1 {
		t.Fatal("the row must keep its fetched time")
	}
}
