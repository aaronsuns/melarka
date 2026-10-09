package download

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// A done job whose track has gone missing from disk (or is broken) no
// longer blocks a fresh download of the same video.
func TestDedupeIgnoresMissingOrBrokenTrack(t *testing.T) {
	for _, col := range []string{"missing_since", "broken"} {
		t.Run(col, func(t *testing.T) {
			e := newEnv(t, true)
			e.fake.entries = []entry{{"m1_0000000x", "Song", "Chan"}}
			ctx := context.Background()
			e.start(t)
			first, err := e.svc.Enqueue(ctx, e.alice, watchURL("m1_0000000x"))
			if err != nil {
				t.Fatal(err)
			}
			j := e.waitStatus(t, first[0].ID, StatusDone)
			if _, err := e.svc.DB.Exec(`UPDATE tracks SET `+col+`=1 WHERE id=?`, *j.TrackID); err != nil {
				t.Fatal(err)
			}
			again, err := e.svc.Enqueue(ctx, e.alice, watchURL("m1_0000000x"))
			if err != nil || len(again) != 1 || again[0].ID == first[0].ID {
				t.Fatalf("re-enqueue after %s: %+v %v, want a new job", col, again, err)
			}
		})
	}
}

// A retry that dedupe blocks reports ErrAlreadyQueued instead of silently
// doing nothing.
func TestRetryBlockedByDedupeIsErrAlreadyQueued(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"q1_0000000x", "Dup", "Chan"}}
	ctx := context.Background()
	first, err := e.svc.Enqueue(ctx, e.alice, watchURL("q1_0000000x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Cancel(ctx, e.alice, false, first[0].ID); err != nil {
		t.Fatal(err)
	}
	second, err := e.svc.Enqueue(ctx, e.alice, watchURL("q1_0000000x"))
	if err != nil || second[0].ID == first[0].ID {
		t.Fatalf("second: %+v %v", second, err)
	}
	if err := e.svc.Retry(ctx, e.alice, false, first[0].ID); !errors.Is(err, ErrAlreadyQueued) {
		t.Fatalf("retry err = %v, want ErrAlreadyQueued", err)
	}
	// Retrying a job that isn't failed/cancelled stays a silent no-op.
	if err := e.svc.Retry(ctx, e.alice, false, second[0].ID); err != nil {
		t.Fatalf("retry queued job: %v", err)
	}
}

// URL enqueues resolve at most two at a time, each bounded by the resolve
// timeout.
func TestEnqueueResolveLimiterAndTimeout(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"l1_0000000x", "Song", "Chan"}}
	e.fake.outputGate = make(chan struct{})
	e.fake.outputStarted = make(chan struct{}, 10)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.svc.Enqueue(ctx, e.alice, watchURL("l1_0000000x"))
			errs <- err
		}()
	}
	for range 2 {
		select {
		case <-e.fake.outputStarted:
		case <-time.After(10 * time.Second):
			t.Fatal("two resolves did not start")
		}
	}
	select {
	case <-e.fake.outputStarted:
		t.Fatal("a third resolve ran while two were in flight")
	case <-time.After(100 * time.Millisecond):
	}
	close(e.fake.outputGate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	e.fake.mu.Lock()
	maxIn := e.fake.maxOutInflight
	e.fake.mu.Unlock()
	if maxIn != 2 {
		t.Fatalf("max resolves in flight = %d, want 2", maxIn)
	}

	// Timeout: a resolve that never answers is cut off.
	e.fake.mu.Lock()
	e.fake.outputGate = make(chan struct{})
	e.fake.mu.Unlock()
	e.svc.resolveTimeout = 50 * time.Millisecond
	if _, err := e.svc.Enqueue(ctx, e.alice, watchURL("l1_0000000x")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hung resolve err = %v, want DeadlineExceeded", err)
	}
}

func TestResolveTimeoutDefault(t *testing.T) {
	if got := (&Service{}).resolveTimeoutOrDefault(); got != 60*time.Second {
		t.Fatalf("default resolve timeout = %v, want 60s", got)
	}
}
