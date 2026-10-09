package channels

import (
	"context"
	"testing"
	"time"
)

// §18.1: 最新 oldest first and 只看未播放, per user.
func TestLatestOrderAndUnplayedFilter(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.user("anna"), e.user("bo")
	e.follow(a, chA)
	e.follow(b, chA)
	e.done(chA, 1, 3*time.Hour, 10)
	e.done(chA, 2, 2*time.Hour, 10)
	e.done(chA, 3, time.Hour, 10)
	played := true
	if err := e.svc.SetProgress(ctx, a, vid(2), 0, &played); err != nil {
		t.Fatal(err)
	}
	ids := func(items []Episode) []string {
		out := []string{}
		for _, it := range items {
			out = append(out, it.VideoID)
		}
		return out
	}
	eq := func(got []Episode, want ...string) {
		t.Helper()
		g := ids(got)
		if len(g) != len(want) {
			t.Fatalf("got %v want %v", g, want)
		}
		for i := range g {
			if g[i] != want[i] {
				t.Fatalf("got %v want %v", g, want)
			}
		}
	}
	asc, err := e.svc.LatestPage(ctx, a, LatestQuery{Asc: true})
	if err != nil {
		t.Fatal(err)
	}
	eq(asc, vid(1), vid(2), vid(3))
	// after= pages an oldest-first list.
	page, _ := e.svc.LatestPage(ctx, a, LatestQuery{Asc: true, After: asc[0].PublishedAt})
	eq(page, vid(2), vid(3))
	un, _ := e.svc.LatestPage(ctx, a, LatestQuery{Unplayed: true})
	eq(un, vid(3), vid(1))
	unAsc, _ := e.svc.LatestPage(ctx, a, LatestQuery{Unplayed: true, Asc: true, Limit: 1})
	eq(unAsc, vid(1))
	// bo played nothing: anna's "played" is hers alone.
	bo, _ := e.svc.LatestPage(ctx, b, LatestQuery{Unplayed: true})
	eq(bo, vid(3), vid(2), vid(1))
}

// §18.1 按频道: one section per followed channel (newest activity first),
// its unplayed count and its latest episodes, only for the asking user.
func TestByChannel(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.user("anna"), e.user("bo")
	e.follow(a, chA)
	e.follow(a, chB)
	e.follow(b, chB)
	e.done(chA, 1, 5*time.Hour, 10)
	e.done(chA, 2, 4*time.Hour, 10)
	e.done(chB, 3, time.Hour, 10)
	for n := 10; n < 14; n++ {
		e.done(chA, n, 6*time.Hour+time.Duration(n)*time.Minute, 10)
	}
	played := true
	if err := e.svc.SetProgress(ctx, a, vid(2), 0, &played); err != nil {
		t.Fatal(err)
	}
	groups, err := e.svc.ByChannel(ctx, a, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].Channel.ID != chB || groups[1].Channel.ID != chA {
		t.Fatalf("order: %+v", groups)
	}
	if groups[0].Unplayed != 1 || len(groups[0].Episodes) != 1 || groups[0].Episodes[0].VideoID != vid(3) {
		t.Fatalf("chB: %+v", groups[0])
	}
	g := groups[1]
	if g.Unplayed != 5 || len(g.Episodes) != 3 || g.Episodes[0].VideoID != vid(2) || g.Episodes[1].VideoID != vid(1) || !g.Episodes[0].Played {
		t.Fatalf("chA: unplayed %d %+v", g.Unplayed, g.Episodes)
	}
	bg, _ := e.svc.ByChannel(ctx, b, 10)
	if len(bg) != 1 || bg[0].Channel.ID != chB {
		t.Fatalf("bo sees only his channel: %+v", bg)
	}
	none, _ := e.svc.ByChannel(ctx, e.user("cy"), 10)
	if none == nil || len(none) != 0 {
		t.Fatalf("no follows: %#v", none)
	}
}
