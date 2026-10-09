package download

import (
	"context"
	"testing"
)

func TestBusy(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	if b, err := e.svc.Busy(ctx); err != nil || b {
		t.Fatalf("empty queue busy=%v %v", b, err)
	}
	e.fake.entries = []entry{{"v1_0000000x", "Song", "Chan"}}
	jobs, err := e.svc.Enqueue(ctx, e.alice, watchURL("v1_0000000x"))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := e.svc.Busy(ctx); !b {
		t.Fatal("a queued job must make the queue busy")
	}
	if _, err := e.svc.DB.Exec(`UPDATE downloads SET status='failed' WHERE id=?`, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	if b, _ := e.svc.Busy(ctx); b {
		t.Fatal("failed jobs don't hold episodes back")
	}
}
