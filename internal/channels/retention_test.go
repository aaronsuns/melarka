package channels

import (
	"context"
	"os"
	"testing"
	"time"
)

// done seeds an episode published `ago` ago with done audio of size bytes on disk.
func (e *env) done(ch string, n int, ago time.Duration, size int) {
	e.t.Helper()
	e.seedQueued(ch, n, ago)
	os.MkdirAll(e.path(ch, ""), 0o755)
	os.WriteFile(e.path(ch, vid(n)+".m4a"), make([]byte, size), 0o644)
	os.WriteFile(e.path(ch, vid(n)+".jpg"), []byte("jpg"), 0o644)
	e.exec(`INSERT INTO episode_files(video_id,kind,status,path,bytes,created_at,updated_at) VALUES (?,'audio','done',?,?,1,1)`,
		vid(n), ch+"/"+vid(n)+".m4a", size)
}

func TestSweepByKeepDaysKeepsAndGrace(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.user("anna"), e.user("bo")
	e.follow(a, chA)
	e.follow(b, chA)
	thirty := 30
	if _, err := e.svc.UpdateSettings(ctx, b, chA, Settings{Media: "audio", KeepDays: &thirty}); err != nil {
		t.Fatal(err)
	}
	e.done(chA, 1, 20*24*time.Hour, 10) // 20 days: inside bo's 30
	e.done(chA, 2, 40*24*time.Hour, 10) // 40 days: past everyone's
	e.done(chA, 3, 40*24*time.Hour, 10) // kept by anna
	e.done(chA, 4, 40*24*time.Hour, 10) // anna is listening (touched yesterday)
	e.done(chA, 5, 40*24*time.Hour, 10) // anna's progress is 3 days old: no longer protected
	e.exec(`INSERT INTO episode_keeps(user_id,video_id,created_at) VALUES (?,?,1)`, a, vid(3))
	e.exec(`INSERT INTO episode_progress(user_id,video_id,position_s,updated_at) VALUES (?,?,600,?)`, a, vid(4), t0.Add(-24*time.Hour).Unix())
	e.exec(`INSERT INTO episode_progress(user_id,video_id,position_s,updated_at) VALUES (?,?,600,?)`, a, vid(5), t0.Add(-72*time.Hour).Unix())
	n, err := e.svc.Sweep(ctx)
	if err != nil || n != 2 {
		t.Fatalf("expired %d %v", n, err)
	}
	for i, want := range map[int]string{1: "done", 2: "expired", 3: "done", 4: "done", 5: "expired"} {
		if got := e.fileStatus(vid(i), "audio"); got != want {
			t.Errorf("%s: %s, want %s", vid(i), got, want)
		}
	}
	if exists(e.path(chA, vid(2)+".m4a")) || exists(e.path(chA, vid(2)+".jpg")) || !exists(e.path(chA, vid(1)+".m4a")) {
		t.Fatal("expired files are deleted from disk, the others stay")
	}
	if e.count(`SELECT COUNT(*) FROM episodes`) != 5 || e.count(`SELECT bytes FROM episode_files WHERE video_id=?`, vid(2)) != 0 {
		t.Fatal("rows stay for history, with no bytes")
	}
}

func TestSweepUnfollowedChannelAndQueuedFiles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.done(chB, 1, time.Hour, 10) // nobody follows chB
	e.seedQueued(chB, 2, time.Hour, "audio")
	e.seedQueued(chB, 3, time.Hour, "audio")
	e.exec(`UPDATE episode_files SET status='downloading' WHERE video_id=?`, vid(3))
	if _, err := e.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if e.fileStatus(vid(1), "audio") != "expired" || e.fileStatus(vid(2), "audio") != "expired" {
		t.Fatal("nobody follows the channel: its files go, and queued ones are never fetched")
	}
	if e.fileStatus(vid(3), "audio") != "downloading" {
		t.Fatal("a download in progress is left alone")
	}
}

// Over the cap, the oldest unkept go first; mid-playback last; kept never.
func TestSweepEvictsOverCap(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user("anna")
	e.follow(a, chA)
	e.done(chA, 1, 5*24*time.Hour, 400) // oldest, kept
	e.done(chA, 2, 4*24*time.Hour, 400) // mid-playback
	e.done(chA, 3, 3*24*time.Hour, 400)
	e.done(chA, 4, 2*24*time.Hour, 400)
	e.done(chA, 5, 1*24*time.Hour, 400)
	e.exec(`INSERT INTO episode_keeps(user_id,video_id,created_at) VALUES (?,?,1)`, a, vid(1))
	e.exec(`INSERT INTO episode_progress(user_id,video_id,position_s,updated_at) VALUES (?,?,60,?)`, a, vid(2), t0.Unix())
	e.svc.MaxBytes = 1300 // 2000 on disk: two must go
	if _, err := e.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	got := map[int]string{}
	for i := 1; i <= 5; i++ {
		got[i] = e.fileStatus(vid(i), "audio")
	}
	if got[1] != "done" || got[2] != "done" || got[3] != "expired" || got[4] != "expired" || got[5] != "done" {
		t.Fatalf("%v", got)
	}
	e.svc.MaxBytes = 500 // only the kept one and the one being listened to are left besides vid 5
	e.svc.Sweep(ctx)
	if e.fileStatus(vid(5), "audio") != "expired" || e.fileStatus(vid(2), "audio") != "expired" || e.fileStatus(vid(1), "audio") != "done" {
		t.Fatal("mid-playback goes after every other candidate; kept never")
	}
}

// A backfill pick older than keep_days (20, 22 and 40 days with keep 10) is
// read, downloaded, survives the sweep right after and shows in Latest: its
// retention clock starts when it was queued. It goes keep_days later.
func TestBackfillOlderThanKeepDaysSurvivesTheSweep(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("anna")
	e.follow(u, chA)
	e.feeds.feeds[chA] = Feed{Title: "A", Entries: []FeedEntry{
		e.entry(1, 20*24*time.Hour), e.entry(2, 22*24*time.Hour), e.entry(3, 40*24*time.Hour), e.entry(4, 50*24*time.Hour)}}
	if err := e.svc.PollChannel(ctx, chA); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		e.svc.workStep(ctx)
	}
	for i := 1; i <= 3; i++ {
		if got := e.fileStatus(vid(i), "audio"); got != "done" {
			t.Fatalf("%s: %q, want done (a backfill pick is read even outside the 30-day window)", vid(i), got)
		}
	}
	if e.fileStatus(vid(4), "audio") != "" {
		t.Fatal("only the newest three are backfilled")
	}
	if e.count(`SELECT COUNT(*) FROM episodes WHERE kind='unknown' AND video_id=?`, vid(4)) != 1 {
		t.Fatal("old episodes past the backfill are not read")
	}
	if n, err := e.svc.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("sweep right after the backfill expired %d (%v)", n, err)
	}
	eps, err := e.svc.Latest(ctx, u, 0, 50)
	if err != nil || len(eps) != 3 {
		t.Fatalf("Latest: %d episodes (%v), want 3", len(eps), err)
	}
	e.clock.Add(9 * 24 * time.Hour)
	if n, _ := e.svc.Sweep(ctx); n != 0 {
		t.Fatalf("expired %d after 9 days, want 0", n)
	}
	e.clock.Add(2 * 24 * time.Hour)
	if n, _ := e.svc.Sweep(ctx); n != 3 {
		t.Fatalf("expired %d after 11 days, want 3", n)
	}
}
