package download

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// Short of disk on the download target: a new download is refused, and a queued one fails
// with lark:no_space instead of filling the disk; once there is room again a retry works.
func TestDownloadsNeedFreeDisk(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"v1_0000000x", "Song", "Chan"}}
	var free atomic.Int64
	free.Store(5 << 30)
	e.svc.MinFree = 10 << 30
	var asked atomic.Value
	e.svc.FreeBytes = func(dir string) (int64, error) { asked.Store(dir); return free.Load(), nil }
	ctx := context.Background()

	if _, err := e.svc.Enqueue(ctx, e.alice, watchURL("v1_0000000x")); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("enqueue with 5 GiB free: %v", err)
	}
	if _, err := e.svc.EnqueueVideo(ctx, e.alice, ytdlp.Video{ID: "v1_0000000x", Title: "Song", Channel: "Chan", URL: watchURL("v1_0000000x")}); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("enqueue video with 5 GiB free: %v", err)
	}
	if asked.Load() != e.root {
		t.Fatalf("free space asked of %v, want the target library %s", asked.Load(), e.root)
	}

	free.Store(20 << 30) // room to queue, then the disk fills before the worker starts it
	jobs, err := e.svc.Enqueue(ctx, e.alice, watchURL("v1_0000000x"))
	if err != nil {
		t.Fatal(err)
	}
	free.Store(1 << 30)
	e.start(t)
	j := e.waitStatus(t, jobs[0].ID, StatusFailed)
	if j.Error != "lark:no_space" || e.trackCount(t) != 0 {
		t.Fatalf("job %+v, tracks %d", j, e.trackCount(t))
	}

	free.Store(20 << 30)
	if err := e.svc.Retry(ctx, e.alice, false, j.ID); err != nil {
		t.Fatal(err)
	}
	e.waitStatus(t, j.ID, StatusDone)
}

func TestDownloadsMinFreeZeroIsNoCheck(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"v1_0000000x", "Song", "Chan"}}
	e.svc.FreeBytes = func(string) (int64, error) { return 0, nil }
	if _, err := e.svc.Enqueue(context.Background(), e.alice, watchURL("v1_0000000x")); err != nil {
		t.Fatalf("MinFree 0: %v", err)
	}
}
