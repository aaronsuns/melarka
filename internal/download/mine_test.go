package download

import (
	"context"
	"slices"
	"testing"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func TestTrackIDsMineDedupedUsableNewestFirst(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	e.start(t)
	vid := func(id, title string) ytdlp.Video {
		return ytdlp.Video{ID: id, Title: title, Channel: "Chan", URL: watchURL(id)}
	}
	a, err := e.svc.EnqueueVideo(ctx, e.alice, vid("v1_0000000x", "One"))
	if err != nil {
		t.Fatal(err)
	}
	ja := e.waitStatus(t, a.ID, StatusDone)
	b, err := e.svc.EnqueueVideo(ctx, e.bob, vid("v2_0000000x", "Two"))
	if err != nil {
		t.Fatal(err)
	}
	jb := e.waitStatus(t, b.ID, StatusDone)
	// Bob asks for Alice's song too: deduped onto her job, so it is his download as well.
	if again, err := e.svc.EnqueueVideo(ctx, e.bob, vid("v1_0000000x", "One")); err != nil || again.ID != ja.ID {
		t.Fatalf("dedupe: %+v %v", again, err)
	}
	if _, err := e.svc.DB.Exec(`UPDATE download_requests SET created_at = CASE
		WHEN user_id=? AND download_id=? THEN 300 WHEN download_id=? THEN 200 ELSE 100 END`, e.bob, ja.ID, jb.ID); err != nil {
		t.Fatal(err)
	}
	ids := func(user int64, all bool) []int64 {
		t.Helper()
		got, err := e.svc.TrackIDs(ctx, user, all, 0)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	ta, tb := *ja.TrackID, *jb.TrackID
	if got := ids(e.alice, false); !slices.Equal(got, []int64{ta}) {
		t.Fatalf("alice %v", got)
	}
	if got := ids(e.bob, false); !slices.Equal(got, []int64{ta, tb}) { // his request on ta (300) is newest
		t.Fatalf("bob %v", got)
	}
	if got := ids(e.admin, false); len(got) != 0 {
		t.Fatalf("admin's own %v", got)
	}
	if got := ids(0, true); !slices.Equal(got, []int64{ta, tb}) {
		t.Fatalf("all %v", got)
	}
	// A trashed song leaves every list; a hidden job still counts.
	e.svc.DB.Exec(`UPDATE tracks SET status='trashed' WHERE id=?`, tb)
	if got := ids(e.bob, false); !slices.Equal(got, []int64{ta}) {
		t.Fatalf("after trash %v", got)
	}
	if err := e.svc.Cancel(ctx, e.alice, false, ja.ID); err != nil { // hide
		t.Fatal(err)
	}
	if got := ids(e.alice, false); !slices.Equal(got, []int64{ta}) {
		t.Fatalf("hidden job dropped: %v", got)
	}
	if got := ids(999, false); len(got) != 0 { // unknown user: empty, no error
		t.Fatalf("unknown user %v", got)
	}
}
