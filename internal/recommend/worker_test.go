package recommend

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// runWorker runs the worker until the test ends.
func (e *env) runWorker() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); e.svc.Run(ctx) }()
	e.t.Cleanup(func() { cancel(); <-done })
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (e *env) activeUser(name, videoID string) int64 {
	u := e.user(name)
	s := e.track("Seed "+name, "x", 200)
	e.play(u, s, 3, t0.Add(-time.Hour), 200)
	e.download(u, videoID, "done", s)
	e.yt.mix[videoID] = []ytdlp.Video{vid(("recommend" + name + "xx")[:11], "Rec", "c", 200)}
	return u
}

func TestWorkerRefreshesOnDemand(t *testing.T) {
	e := newEnv(t)
	e.svc.RefreshAt = "23:59" // the nightly run is not due yet
	u := e.activeUser("u", "seedvideo01")
	e.yt.mix["seedvideo01"] = []ytdlp.Video{vid("ondemandaaa", "Rec", "c", 200)}
	e.runWorker()
	time.Sleep(50 * time.Millisecond)
	if _, m := e.yt.calls(); len(m) != 0 {
		t.Fatal("worked before anything was due")
	}
	if err := e.svc.Request(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	eventually(t, "on-demand refresh", func() bool {
		res, _ := e.svc.List(context.Background(), u)
		return !res.Refreshing && ids(res.Items) == "ondemandaaa"
	})
}

// Nightly: every user with plays or favorites once, users without none.
func TestWorkerNightlySkipsInactiveUsers(t *testing.T) {
	e := newEnv(t)
	e.svc.RefreshAt = "02:30" // the clock says 12:00: due
	e.svc.Pause = 10 * time.Millisecond
	u := e.activeUser("u", "seedvideo01")
	idle := e.user("idle")
	e.runWorker()
	eventually(t, "nightly run finished", func() bool {
		var day string
		e.db.QueryRow(`SELECT value FROM app_state WHERE key='recommendations.nightly_day'`).Scan(&day)
		return day == "2026-10-04"
	})
	res, _ := e.svc.List(context.Background(), u)
	if res.RefreshedAt == nil {
		t.Fatal("active user not refreshed")
	}
	var n int
	e.db.QueryRow(`SELECT COUNT(*) FROM recommendation_users WHERE user_id=?`, idle).Scan(&n)
	if n != 0 {
		t.Fatal("idle user was refreshed")
	}
	time.Sleep(100 * time.Millisecond)
	if _, m := e.yt.calls(); len(m) != 1 {
		t.Fatalf("mixes %v: refreshed more than once", m)
	}
}

func TestWorkerNotDueBeforeRefreshTime(t *testing.T) {
	e := newEnv(t)
	e.clock.t = time.Date(2026, 10, 4, 1, 0, 0, 0, time.Local)
	e.svc.RefreshAt = "02:30"
	e.activeUser("u", "seedvideo01")
	e.runWorker()
	time.Sleep(150 * time.Millisecond)
	if _, m := e.yt.calls(); len(m) != 0 {
		t.Fatal("refreshed before 02:30")
	}
}

// Every refresh failing: each user is tried once, ErrBackoff apart, and
// nothing is retried in a loop.
func TestWorkerNeverSpinsAndBacksOff(t *testing.T) {
	e := newEnv(t)
	e.svc.RefreshAt = "02:30"
	e.svc.ErrBackoff = 300 * time.Millisecond
	e.yt.err = errBoom
	u := e.activeUser("u", "seedvideo01")
	e.activeUser("v", "seedvideo02")
	e.runWorker()
	eventually(t, "both users tried", func() bool { return len(e.yt.times()) >= 2 })
	// An on-demand request after a failure is tried once more, still after the backoff.
	e.clock.Add(11 * time.Minute)
	if err := e.svc.Request(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	at := e.yt.times()
	if len(at) != 3 {
		t.Fatalf("%d yt-dlp calls, want 3 (one per attempt)", len(at))
	}
	for i := 1; i < len(at); i++ {
		if gap := at[i].Sub(at[i-1]); gap < 250*time.Millisecond {
			t.Fatalf("attempt %d only %v after the previous failure", i, gap)
		}
	}
	var attempted sql.NullInt64
	e.db.QueryRow(`SELECT attempted_at FROM recommendation_users WHERE user_id=?`, u).Scan(&attempted)
	if !attempted.Valid {
		t.Fatal("failure not recorded")
	}
	if res, _ := e.svc.List(context.Background(), u); res.Refreshing {
		t.Fatal("a failed refresh still shows as refreshing")
	}
}

func TestNextNightly(t *testing.T) {
	loc := time.Local
	at := func(h, m int) time.Time { return time.Date(2026, 10, 4, h, m, 0, 0, loc) }
	due, err := dueAt(at(12, 0), "02:30")
	if err != nil || !due.Equal(at(2, 30)) {
		t.Fatalf("%v %v", due, err)
	}
	if _, err := dueAt(at(12, 0), "25:00"); err == nil {
		t.Fatal("bad time accepted")
	}
	if _, err := ParseRefreshAt("2:30"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "24:00", "02:60", "noon", "02:30:00"} {
		if _, err := ParseRefreshAt(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
