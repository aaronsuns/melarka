package api

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// A search still queued for a slot whose every caller has gone away is
// dropped before it ever takes a slot — a member typing (and abandoning)
// queries must not leave a backlog of yt-dlp runs nobody will read.
func TestSearchLimiterDropsAbandonedQueuedSearch(t *testing.T) {
	var l searchLimiter
	dropped := make(chan string, 1)
	l.onDrop = func(key string) { dropped <- key }

	gate := make(chan struct{})
	started := make(chan struct{}, searchTokens)
	busy := make(chan error, searchTokens)
	for i := range searchTokens {
		go func() {
			_, err := l.do(context.Background(), fmt.Sprintf("busy%d", i), 1, func(context.Context) (ytdlp.SearchResult, error) {
				started <- struct{}{}
				<-gate
				return ytdlp.SearchResult{}, nil
			})
			busy <- err
		}()
	}
	for range searchTokens {
		<-started
	}

	var ran atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := l.do(ctx, "abandoned", 1, func(context.Context) (ytdlp.SearchResult, error) {
			ran.Add(1)
			return ytdlp.SearchResult{}, nil
		})
		errc <- err
	}()
	cancel()
	if err := <-errc; err == nil {
		t.Fatal("abandoned caller: want ctx error")
	}
	select {
	case k := <-dropped:
		if k != "abandoned" {
			t.Fatalf("dropped %q", k)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("abandoned queued search was not dropped")
	}
	close(gate)
	for range searchTokens {
		if err := <-busy; err != nil {
			t.Fatal(err)
		}
	}
	// A fresh call for the same key runs normally afterwards.
	if _, err := l.do(context.Background(), "abandoned", 1, func(context.Context) (ytdlp.SearchResult, error) {
		ran.Add(1)
		return ytdlp.SearchResult{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if n := ran.Load(); n != 1 {
		t.Fatalf("fn ran %d times, want 1 (only the fresh call)", n)
	}
}

// One caller leaving doesn't drop a search another caller still waits for,
// and a search that already started runs to completion.
func TestSearchLimiterKeepsSearchWithRemainingWaiter(t *testing.T) {
	var l searchLimiter
	// l.do spawns the actual joining in a goroutine the test doesn't control,
	// so "second caller has joined" must be observed through a hook rather
	// than assumed from goroutine scheduling order — otherwise the first
	// caller can cancel, and its search can even finish, before the second
	// caller's l.do ever reaches the point where it joins the same call.
	joined := make(chan struct{})
	l.onWaiters = func(key string, n int) {
		if key == "k" && n == 2 {
			close(joined)
		}
	}
	gate := make(chan struct{})
	started := make(chan struct{})
	fn := func(context.Context) (ytdlp.SearchResult, error) {
		close(started)
		<-gate
		return ytdlp.SearchResult{Videos: []ytdlp.Video{{ID: "x"}}}, nil
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	r1 := make(chan error, 1)
	go func() { _, err := l.do(ctx1, "k", 1, fn); r1 <- err }()
	<-started
	r2 := make(chan ytdlp.SearchResult, 1)
	go func() {
		v, _ := l.do(context.Background(), "k", 1, func(context.Context) (ytdlp.SearchResult, error) {
			t.Error("second caller must join, not run its own search")
			return ytdlp.SearchResult{}, nil
		})
		r2 <- v
	}()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("second caller never joined the in-flight call")
	}
	cancel1()
	<-r1
	close(gate)
	if v := <-r2; len(v.Videos) != 1 {
		t.Fatalf("second caller got %v", v)
	}
}

// Background (recommendation) yt-dlp work takes one token only when no
// interactive search is waiting: with every token busy and a member's search
// queued, a low-priority call never jumps ahead of it.
func TestSearchLimiterLowPriorityNeverStarvesInteractive(t *testing.T) {
	var l searchLimiter
	gate := make(chan struct{})
	started := make(chan struct{}, searchTokens)
	for i := range searchTokens {
		go l.do(context.Background(), fmt.Sprintf("busy%d", i), 1, func(context.Context) (ytdlp.SearchResult, error) {
			started <- struct{}{}
			<-gate
			return ytdlp.SearchResult{}, nil
		})
	}
	for range searchTokens {
		<-started
	}
	order := make(chan string, 2)
	interactiveDone := make(chan struct{})
	go func() {
		defer close(interactiveDone)
		l.do(context.Background(), "member", searchQueryTokens, func(context.Context) (ytdlp.SearchResult, error) {
			order <- "interactive"
			return ytdlp.SearchResult{}, nil
		})
	}()
	// The member's search is queued (its call exists) before the background work asks.
	waitFor(t, func() bool { return l.waiting() == 1 })
	lowDone := make(chan error, 1)
	go func() {
		lowDone <- l.low(context.Background(), func(context.Context) error {
			order <- "low"
			return nil
		})
	}()
	time.Sleep(50 * time.Millisecond)
	close(gate) // every busy search finishes at once
	if first := <-order; first != "interactive" {
		t.Fatalf("%s ran first", first)
	}
	if err := <-lowDone; err != nil {
		t.Fatal(err)
	}
	<-interactiveDone
}

// A low-priority call holds exactly one token: interactive searches keep
// the other three; and waiting for a token ends with ctx.
func TestSearchLimiterLowPriorityHoldsOneTokenAndHonoursCtx(t *testing.T) {
	var l searchLimiter
	release := make(chan struct{})
	inLow := make(chan struct{})
	go l.low(context.Background(), func(context.Context) error {
		close(inLow)
		<-release
		return nil
	})
	<-inLow
	var running atomic.Int32
	all := make(chan struct{})
	for i := range searchTokens - 1 {
		go l.do(context.Background(), fmt.Sprintf("s%d", i), 1, func(context.Context) (ytdlp.SearchResult, error) {
			if running.Add(1) == searchTokens-1 {
				close(all)
			}
			<-release
			return ytdlp.SearchResult{}, nil
		})
	}
	select {
	case <-all:
	case <-time.After(5 * time.Second):
		t.Fatalf("only %d interactive searches ran beside the low-priority one", running.Load())
	}
	// Every token is now taken: a second low-priority call waits, then gives up with its ctx.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ran := false
	if err := l.low(ctx, func(context.Context) error { ran = true; return nil }); err == nil || ran {
		t.Fatalf("err %v ran %v", err, ran)
	}
	close(release)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never became true")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
