package download

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func favs(t *testing.T, e *env, user int64) []int64 {
	t.Helper()
	rows, err := e.store.DB.Query(`SELECT track_id FROM favorites WHERE user_id=? ORDER BY track_id`, user)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids
}

// waitFavs polls until user has want favorites: the job turns "done" a moment
// before the worker favorites its requesters.
func waitFavs(t *testing.T, e *env, user int64, want []int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Equal(favs(t, e, user), want) {
		if time.Now().After(deadline) {
			t.Fatalf("user %d favorites %v, want %v", user, favs(t, e, user), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDownloadFavoritesEveryRequester(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"v1_0000000x", "Song", "Chan"}}
	e.fake.block["v1_0000000x"] = true
	ctx := context.Background()
	carol := addUser(t, e.store, "carol", "member")
	e.store.DB.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (99,'dave','x','member',0)`)
	e.start(t)
	a, err := e.svc.Enqueue(ctx, e.alice, "https://www.youtube.com/watch?v=v1_0000000x")
	if err != nil {
		t.Fatal(err)
	}
	e.waitStarted(t)
	b, _ := e.svc.Enqueue(ctx, e.bob, "https://youtu.be/v1_0000000x") // dedupes onto alice's running job
	if b[0].ID != a[0].ID {
		t.Fatalf("not deduped: %d vs %d", b[0].ID, a[0].ID)
	}
	close(e.fake.release)
	j := e.waitStatus(t, a[0].ID, StatusDone)
	tid := *j.TrackID
	waitFavs(t, e, e.alice, []int64{tid})
	waitFavs(t, e, e.bob, []int64{tid})
	if len(favs(t, e, e.admin)) != 0 {
		t.Fatal("non-requester favorited")
	}
	// After completion: a new request dedupes onto the done job and favorites at once.
	e.store.DB.Exec(`INSERT INTO dislikes(user_id,track_id,created_at) VALUES (?,?,0)`, carol, tid)
	if _, err := e.svc.EnqueueVideo(ctx, carol, ytdlp.Video{ID: "v1_0000000x", Title: "Song", URL: "https://www.youtube.com/watch?v=v1_0000000x"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(favs(t, e, carol), []int64{tid}) {
		t.Fatalf("carol %v", favs(t, e, carol))
	}
	var disliked int
	e.store.DB.QueryRow(`SELECT COUNT(*) FROM dislikes WHERE user_id=? AND track_id=?`, carol, tid).Scan(&disliked)
	if disliked != 0 {
		t.Fatal("requesting a song must clear the requester's dislike, like favoriting does")
	}
	// Unfavoriting stays manual: a later scan or re-enqueue by someone else doesn't re-add it.
	e.store.DB.Exec(`DELETE FROM favorites WHERE user_id=?`, e.alice)
	e.svc.EnqueueVideo(ctx, 99, ytdlp.Video{ID: "v1_0000000x", Title: "Song", URL: "https://www.youtube.com/watch?v=v1_0000000x"})
	if len(favs(t, e, e.alice)) != 0 {
		t.Fatal("someone else's request re-favorited for alice")
	}
}

func TestFailedJobFavoritesNobodyUntilRetrySucceeds(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"f1_0000000x", "Song", "Chan"}}
	e.fake.fail["f1_0000000x"] = true
	ctx := context.Background()
	e.start(t)
	a, err := e.svc.Enqueue(ctx, e.alice, "https://www.youtube.com/watch?v=f1_0000000x")
	if err != nil {
		t.Fatal(err)
	}
	e.waitStatus(t, a[0].ID, StatusFailed)
	if len(favs(t, e, e.alice)) != 0 {
		t.Fatal("failed job favorited something")
	}
	e.fake.set(e.fake.fail, "f1_0000000x", false)
	if err := e.svc.Retry(ctx, e.alice, false, a[0].ID); err != nil {
		t.Fatal(err)
	}
	j := e.waitStatus(t, a[0].ID, StatusDone)
	waitFavs(t, e, e.alice, []int64{*j.TrackID})
	if len(favs(t, e, e.bob)) != 0 {
		t.Fatal("non-requester favorited")
	}
}

// A requester whose job snapshot is stale (the job finished between the
// dedupe read and the INSERT) must still be favorited.
func TestRecordRequestOnJobThatFinishedMeanwhile(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"r1_0000000x", "Song", "Chan"}}
	ctx := context.Background()
	e.start(t)
	a, err := e.svc.Enqueue(ctx, e.alice, "https://www.youtube.com/watch?v=r1_0000000x")
	if err != nil {
		t.Fatal(err)
	}
	done := e.waitStatus(t, a[0].ID, StatusDone)
	stale := done
	stale.Status, stale.TrackID = StatusDownloading, nil
	if err := e.svc.recordRequest(ctx, stale, e.bob); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(favs(t, e, e.bob), []int64{*done.TrackID}) {
		t.Fatalf("bob %v, want [%d]", favs(t, e, e.bob), *done.TrackID)
	}
	// Idempotent.
	if err := e.svc.recordRequest(ctx, stale, e.bob); err != nil {
		t.Fatal(err)
	}
}
