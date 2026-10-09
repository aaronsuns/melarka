package api

import (
	"sync"
	"time"
)

// prepareLimiter is a per-user token bucket for POST /tracks/prepare: a
// burst of prepareBurst, refilled at prepareBurst per minute. Zero-value ready.
type prepareLimiter struct {
	mu      sync.Mutex
	buckets map[int64]*tokenBucket
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

const prepareBurst = 10

func (l *prepareLimiter) allow(uid int64, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = map[int64]*tokenBucket{}
	}
	b, ok := l.buckets[uid]
	if !ok {
		b = &tokenBucket{tokens: prepareBurst, last: now}
		l.buckets[uid] = b
	}
	b.tokens += now.Sub(b.last).Minutes() * prepareBurst
	if b.tokens > prepareBurst {
		b.tokens = prepareBurst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
