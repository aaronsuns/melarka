package api

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// searchLimiter bounds concurrent yt-dlp search processes to searchTokens (each
// search holds as many tokens as it runs processes) and
// collapses identical in-flight queries (by key) into one underlying call — a
// search shells out to yt-dlp, which hits YouTube, and several members can
// easily type the same query at once. Zero value is ready to use.
//
// Waiters are counted per key (like the transcode cache's jobs): a search
// still queued for a slot whose every caller has gone is dropped before it
// takes one, so abandoned keystrokes never pile up yt-dlp runs. Once a search
// has started it runs to completion (bounded by searchTimeout) whatever its
// callers do: killing it would waste the work already done, and fn caches
// its own result.
type searchLimiter struct {
	once  sync.Once
	sem   *semaphore.Weighted
	mu    sync.Mutex
	calls map[string]*searchCall
	// queued counts interactive searches created but not yet holding their
	// tokens; low-priority work never takes a token while it is non-zero.
	queued atomic.Int32

	onDrop    func(key string)        // test hook: a queued search was dropped
	onWaiters func(key string, n int) // test hook: a key's waiter count changed
}

type searchCall struct {
	done      chan struct{} // closed once result/err are set
	abandoned chan struct{} // closed when the last waiter leaves before start
	waiters   int
	started   bool
	result    ytdlp.SearchResult
	err       error
}

func (l *searchLimiter) init() {
	l.once.Do(func() {
		l.sem = semaphore.NewWeighted(searchTokens)
		l.calls = map[string]*searchCall{}
	})
}

// do runs (or joins the in-flight call for) key and waits for its result
// until ctx — the caller's own request — is done.
func (l *searchLimiter) do(ctx context.Context, key string, weight int64, fn func(ctx context.Context) (ytdlp.SearchResult, error)) (ytdlp.SearchResult, error) {
	l.init()
	l.mu.Lock()
	c := l.calls[key]
	if c == nil {
		c = &searchCall{done: make(chan struct{}), abandoned: make(chan struct{})}
		l.calls[key] = c
		l.queued.Add(1)
		go l.run(key, c, weight, fn)
	}
	c.waiters++
	l.notifyWaiters(key, c.waiters)
	l.mu.Unlock()

	select {
	case <-c.done:
		return c.result, c.err
	case <-ctx.Done():
		l.mu.Lock()
		c.waiters--
		l.notifyWaiters(key, c.waiters)
		if c.waiters == 0 && !c.started {
			close(c.abandoned)
			delete(l.calls, key)
		}
		l.mu.Unlock()
		return ytdlp.SearchResult{}, ctx.Err()
	}
}

// run waits for a slot, then runs fn once with a context independent of any
// one request, bounded by searchTimeout (queue wait included).
func (l *searchLimiter) run(key string, c *searchCall, weight int64, fn func(ctx context.Context) (ytdlp.SearchResult, error)) {
	ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
	defer cancel()
	finish := func(res ytdlp.SearchResult, err error) {
		l.mu.Lock()
		if l.calls[key] == c {
			delete(l.calls, key)
		}
		c.result, c.err = res, err
		l.mu.Unlock()
		close(c.done)
	}
	// Waiting for tokens ends early if every caller leaves (abandoned) or
	// the timeout hits; semaphore.Acquire is FIFO, so a heavy query is not
	// starved by lighter ones.
	actx, acancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-c.abandoned:
			acancel()
		case <-actx.Done():
		}
	}()
	err := l.sem.Acquire(actx, weight)
	acancel()
	l.queued.Add(-1)
	if err != nil {
		select {
		case <-c.abandoned:
			l.dropped(key)
		default:
			finish(ytdlp.SearchResult{}, ctx.Err())
		}
		return
	}
	defer l.sem.Release(weight)
	l.mu.Lock()
	if c.waiters == 0 { // abandoned while we were taking the slot
		l.mu.Unlock()
		l.dropped(key)
		return
	}
	c.started = true
	l.mu.Unlock()
	finish(fn(ctx))
}

func (l *searchLimiter) dropped(key string) {
	if l.onDrop != nil {
		l.onDrop(key)
	}
}

func (l *searchLimiter) notifyWaiters(key string, n int) {
	if l.onWaiters != nil {
		l.onWaiters(key, n)
	}
}

// waiting is how many interactive searches are queued for tokens.
func (l *searchLimiter) waiting() int { return int(l.queued.Load()) }

// lowPollMax caps how long low-priority work sleeps between attempts.
const lowPollMax = 2 * time.Second

// low runs background (recommendation) yt-dlp work holding one token, taken
// only while no interactive search is queued: semaphore.TryAcquire already
// fails when anyone waits in the semaphore, and queued covers a search
// whose goroutine hasn't reached it yet. It polls (50 ms doubling to
// lowPollMax) until it gets a token or ctx ends, so interactive searches are
// never starved by it and it never spins.
func (l *searchLimiter) low(ctx context.Context, fn func(ctx context.Context) error) error {
	l.init()
	wait := 50 * time.Millisecond
	for {
		if l.queued.Load() == 0 && l.sem.TryAcquire(1) {
			defer l.sem.Release(1)
			return fn(ctx)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait = min(wait*2, lowPollMax)
	}
}
