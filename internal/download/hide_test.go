package download

import (
	"context"
	"testing"
)

func listedIDs(t *testing.T, e *env, user int64, all bool) []int64 {
	t.Helper()
	jobs, err := e.svc.List(context.Background(), user, all, 100)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, j := range jobs {
		ids = append(ids, j.ID)
	}
	return ids
}

func doneJob(t *testing.T, e *env, user int64) Job {
	t.Helper()
	e.fake.entries = []entry{{"v1_0000000x", "Song", "Chan"}}
	e.start(t)
	jobs, err := e.svc.Enqueue(context.Background(), user, watchURL("v1_0000000x"))
	if err != nil {
		t.Fatal(err)
	}
	j := e.waitStatus(t, jobs[0].ID, StatusDone)
	waitFavs(t, e, user, []int64{*j.TrackID})
	return j
}

func TestRemovingDoneJobHidesItAndKeepsTrackAndFavorites(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	j := doneJob(t, e, e.alice)
	if !j.TrackAvailable {
		t.Fatal("done job with a live track should be available")
	}
	if err := e.svc.Cancel(ctx, e.alice, false, j.ID); err != nil {
		t.Fatal(err)
	}
	if ids := listedIDs(t, e, e.alice, false); len(ids) != 0 {
		t.Fatalf("hidden job still listed for owner: %v", ids)
	}
	if ids := listedIDs(t, e, 0, true); len(ids) != 0 {
		t.Fatalf("hidden job still listed for admin: %v", ids)
	}
	if n := e.trackCount(t); n != 1 {
		t.Fatalf("tracks=%d", n)
	}
	if got := favs(t, e, e.alice); len(got) != 1 || got[0] != *j.TrackID {
		t.Fatalf("favorites=%v", got)
	}
	var reqs int
	e.svc.DB.QueryRow(`SELECT COUNT(*) FROM download_requests WHERE download_id=?`, j.ID).Scan(&reqs)
	if reqs != 1 {
		t.Fatalf("download_requests rows=%d, want 1", reqs)
	}
}

func TestRerequestingHiddenJobUnhidesItWithoutDownloadingAgain(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	j := doneJob(t, e, e.alice)
	if err := e.svc.Cancel(ctx, e.alice, false, j.ID); err != nil {
		t.Fatal(err)
	}
	calls := len(e.fake.calls())
	again, err := e.svc.Enqueue(ctx, e.alice, watchURL("v1_0000000x"))
	if err != nil || len(again) != 1 || again[0].ID != j.ID {
		t.Fatalf("re-request: %+v %v", again, err)
	}
	if ids := listedIDs(t, e, e.alice, false); len(ids) != 1 || ids[0] != j.ID {
		t.Fatalf("job not un-hidden: %v", ids)
	}
	if n := e.countStatus(t, StatusQueued) + e.countStatus(t, StatusDownloading); n != 0 {
		t.Fatalf("a new download was queued (%d)", n)
	}
	if got := len(e.fake.calls()); got != calls {
		t.Fatalf("yt-dlp ran again: %v", e.fake.calls())
	}
	if e.trackCount(t) != 1 {
		t.Fatal("track count changed")
	}
}

func TestMemberCannotHideAnotherMembersJob(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	j := doneJob(t, e, e.alice)
	if err := e.svc.Cancel(ctx, e.bob, false, j.ID); err != ErrNotFound {
		t.Fatalf("bob hide: %v, want ErrNotFound", err)
	}
	if ids := listedIDs(t, e, e.alice, false); len(ids) != 1 {
		t.Fatalf("alice's job vanished: %v", ids)
	}
	if err := e.svc.Cancel(ctx, e.admin, true, j.ID); err != nil {
		t.Fatalf("admin hide: %v", err)
	}
	if ids := listedIDs(t, e, e.alice, false); len(ids) != 0 {
		t.Fatalf("admin hide ignored: %v", ids)
	}
}

func TestTrackAvailableFalseWhenSongGone(t *testing.T) {
	e := newEnv(t, true)
	j := doneJob(t, e, e.alice)
	if _, err := e.svc.DB.Exec(`UPDATE tracks SET status='trashed' WHERE id=?`, *j.TrackID); err != nil {
		t.Fatal(err)
	}
	if got := e.job(t, j.ID); got.TrackAvailable || got.TrackID == nil {
		t.Fatalf("trashed track: %+v", got)
	}
	if _, err := e.svc.DB.Exec(`UPDATE tracks SET status='kept', missing_since=5 WHERE id=?`, *j.TrackID); err != nil {
		t.Fatal(err)
	}
	if got := e.job(t, j.ID); got.TrackAvailable {
		t.Fatal("missing track still available")
	}
}

func TestRemovingQueuedJobCancelsInsteadOfHiding(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	e.fake.entries = []entry{{"v1_0000000x", "Song", "Chan"}}
	jobs, err := e.svc.Enqueue(ctx, e.alice, watchURL("v1_0000000x")) // Run not started: stays queued
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Cancel(ctx, e.alice, false, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := e.job(t, jobs[0].ID); got.Status != StatusCancelled {
		t.Fatalf("status %q, want cancelled", got.Status)
	}
	if ids := listedIDs(t, e, e.alice, false); len(ids) != 1 {
		t.Fatalf("cancelled job should still be listed: %v", ids)
	}
}
