package download

import (
	"context"
	"slices"
	"testing"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func TestJobThumbnailIsAlwaysHQDefault(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	e.fake.entries = []entry{{"v1_0000000x", "Song", "Chan"}}
	jobs, err := e.svc.Enqueue(ctx, e.alice, watchURL("v1_0000000x")) // pasted link: resolve path
	if err != nil {
		t.Fatal(err)
	}
	if got := jobs[0].Thumbnail; got != "https://i.ytimg.com/vi/v1_0000000x/hqdefault.jpg" {
		t.Fatalf("pasted link thumbnail %q", got)
	}
	j, err := e.svc.EnqueueVideo(ctx, e.bob, ytdlp.Video{ID: "v2_0000000x", Title: "T", Channel: "C",
		URL: watchURL("v2_0000000x"), Thumbnail: "https://i.ytimg.com/vi/v2_0000000x/maxresdefault.jpg"}) // search result path
	if err != nil {
		t.Fatal(err)
	}
	if j.Thumbnail != "https://i.ytimg.com/vi/v2_0000000x/hqdefault.jpg" {
		t.Fatalf("search result thumbnail %q", j.Thumbnail)
	}
}

func TestDoneJobWithUnusableTrackIsNotListed(t *testing.T) {
	e := newEnv(t, true)
	j := doneJob(t, e, e.alice)
	for _, bad := range []string{`status='trashed'`, `status='kept', missing_since=5`, `status='kept', missing_since=NULL, broken=1`} {
		if _, err := e.svc.DB.Exec(`UPDATE tracks SET `+bad+` WHERE id=?`, *j.TrackID); err != nil {
			t.Fatal(err)
		}
		if ids := listedIDs(t, e, e.alice, false); len(ids) != 0 {
			t.Fatalf("%s: owner still lists %v", bad, ids)
		}
		if ids := listedIDs(t, e, 0, true); len(ids) != 0 {
			t.Fatalf("%s: admin still lists %v", bad, ids)
		}
	}
	// Restored: listed again.
	if _, err := e.svc.DB.Exec(`UPDATE tracks SET status='kept', missing_since=NULL, broken=0 WHERE id=?`, *j.TrackID); err != nil {
		t.Fatal(err)
	}
	if ids := listedIDs(t, e, e.alice, false); !slices.Equal(ids, []int64{j.ID}) {
		t.Fatalf("restored job not listed: %v", ids)
	}
}

// A failed job (really run and failed) and a cancelled one stay listed: they
// are the jobs Remove is offered on.
func TestFailedAndCancelledJobsStayListed(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	e.fake.fail["f1_0000000x"] = true
	e.fake.entries = []entry{{"c1_0000000x", "Cancelled", "Chan"}}         // the fake resolves every URL to its entries
	cancelled, err := e.svc.Enqueue(ctx, e.alice, watchURL("c1_0000000x")) // Run not started yet: stays queued
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Cancel(ctx, e.alice, false, cancelled[0].ID); err != nil {
		t.Fatal(err)
	}
	e.fake.entries = []entry{{"f1_0000000x", "Bad", "Chan"}}
	e.start(t)
	failed, err := e.svc.Enqueue(ctx, e.alice, watchURL("f1_0000000x"))
	if err != nil {
		t.Fatal(err)
	}
	e.waitStatus(t, failed[0].ID, StatusFailed)
	if j := e.job(t, cancelled[0].ID); j.Status != StatusCancelled {
		t.Fatalf("cancelled job is %q", j.Status)
	}
	got := listedIDs(t, e, e.alice, false)
	if len(got) != 2 || !slices.Contains(got, failed[0].ID) || !slices.Contains(got, cancelled[0].ID) {
		t.Fatalf("failed and cancelled jobs must stay listed: %v", got)
	}
}
