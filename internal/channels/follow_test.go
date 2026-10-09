package channels

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func TestFollowDefaultsAndIdempotent(t *testing.T) {
	e := newEnv(t)
	u := e.user("anna")
	mc := e.follow(u, chA)
	want := Settings{Media: "audio"}
	if mc.Settings != want || mc.FollowedAt != t0.Unix() || mc.Channel.Title != "Channel aaaa" {
		t.Fatalf("%+v", mc)
	}
	var backfill, since, next int64
	e.db.QueryRow(`SELECT f.backfill_pending, f.want_since, c.next_poll_at FROM channel_follows f JOIN channels c ON c.id=f.channel_id`).
		Scan(&backfill, &since, &next)
	if backfill != 1 || since != t0.Unix() || next != 0 {
		t.Fatalf("backfill %d since %d next %d", backfill, since, next)
	}
	e.clock.Add(time.Hour)
	again, err := e.svc.Follow(context.Background(), u, ytdlp.Channel{ID: chA, Title: ""})
	if err != nil || again.FollowedAt != t0.Unix() || again.Channel.Title != "Channel aaaa" {
		t.Fatalf("re-follow changed it: %+v %v", again, err)
	}
	if _, err := e.svc.Follow(context.Background(), u, ytdlp.Channel{ID: "UCshort"}); !errors.Is(err, ErrBadID) {
		t.Fatal(err)
	}
}

func TestFollowLimit(t *testing.T) {
	e := newEnv(t)
	u := e.user("anna")
	for i := range MaxFollows {
		e.follow(u, fmt.Sprintf("UC%022d", i))
	}
	if _, err := e.svc.Follow(context.Background(), u, ytdlp.Channel{ID: chA}); !errors.Is(err, ErrFollowLimit) {
		t.Fatalf("101st follow: %v", err)
	}
	if _, err := e.svc.Follow(context.Background(), u, ytdlp.Channel{ID: fmt.Sprintf("UC%022d", 5)}); err != nil {
		t.Fatalf("re-following at the limit: %v", err)
	}
}

func TestUpdateSettings(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, other := e.user("anna"), e.user("bo")
	e.follow(u, chA)
	seven := 7
	got, err := e.svc.UpdateSettings(ctx, u, chA, Settings{Media: "video", KeepDays: &seven, Paused: true, IncludeShorts: true, IncludeLive: true})
	if err != nil || got.Media != "video" || *got.KeepDays != 7 || !got.Paused || !got.IncludeShorts || !got.IncludeLive {
		t.Fatalf("%+v %v", got, err)
	}
	zero, huge := 0, 4000
	for _, bad := range []Settings{{Media: "flac"}, {Media: "audio", KeepDays: &zero}, {Media: "audio", KeepDays: &huge}} {
		if _, err := e.svc.UpdateSettings(ctx, u, chA, bad); !errors.Is(err, ErrBadSettings) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if _, err := e.svc.UpdateSettings(ctx, other, chA, Settings{Media: "audio"}); !errors.Is(err, ErrNotFollowing) {
		t.Fatalf("someone else's follow: %v", err)
	}
	// Un-pausing: episodes published while paused are not fetched.
	e.clock.Add(48 * time.Hour)
	if _, err := e.svc.UpdateSettings(ctx, u, chA, Settings{Media: "video"}); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT want_since FROM channel_follows WHERE user_id=?`, u); int64(n) != t0.Add(48*time.Hour).Unix() {
		t.Fatalf("want_since %d", n)
	}
}

func TestUnfollowKeepsHistoryAndMyChannelsIsPrivate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, other := e.user("anna"), e.user("bo")
	e.follow(u, chA)
	e.follow(other, chB)
	e.exec(`INSERT INTO episodes(video_id,channel_id,title,published_at,kind,seen_at) VALUES (?,?,?,?,?,?)`, vid(1), chA, "E", t0.Unix(), "video", t0.Unix())
	mine, err := e.svc.MyChannels(ctx, u)
	if err != nil || len(mine) != 1 || mine[0].Channel.ID != chA {
		t.Fatalf("%+v %v", mine, err)
	}
	if err := e.svc.Unfollow(ctx, u, chA); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Unfollow(ctx, u, chA); !errors.Is(err, ErrNotFollowing) {
		t.Fatal(err)
	}
	if e.count(`SELECT COUNT(*) FROM episodes WHERE channel_id=?`, chA) != 1 || e.count(`SELECT COUNT(*) FROM channels`) != 2 {
		t.Fatal("unfollow must keep the channel and its episodes (history)")
	}
	if mine, _ := e.svc.MyChannels(ctx, u); len(mine) != 0 {
		t.Fatalf("%+v", mine)
	}
}
