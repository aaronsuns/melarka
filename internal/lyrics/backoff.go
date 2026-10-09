package lyrics

import (
	"sync"
	"time"
)

// Backoff remembers tracks whose lookups failed (an outage, a timeout, a
// failed conversion) so they aren't asked about again too soon. On-demand
// requests (Active) wait the plain base since the last failure (a
// 10-minute in-memory backoff). Prefetch scheduling (ActiveIDs) waits longer
// per consecutive failure — base, 2·base, 4·base … capped at max — so a
// track that keeps failing drops behind the others instead of being picked
// again the moment its backoff runs out. Clear (any answer) resets the
// count; a track left alone for max past its backoff is forgotten. The zero
// value is ready to use.
type Backoff struct {
	mu sync.Mutex
	m  map[int64]backoffEntry
}

type backoffEntry struct {
	last  time.Time // latest failure
	until time.Time // prefetch skips the track before this
	n     int       // consecutive failures
}

// Fail records one more consecutive failure of trackID at now.
func (b *Backoff) Fail(trackID int64, now time.Time, base, max time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.m == nil {
		b.m = map[int64]backoffEntry{}
	}
	e := b.m[trackID]
	if !e.until.IsZero() && now.After(e.until.Add(max)) {
		e.n = 0 // quiet for long enough: start over
	}
	e.n++
	d := base
	for i := 1; i < e.n && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	e.last, e.until = now, now.Add(d)
	b.m[trackID] = e
}

// Active reports whether an on-demand request for trackID should still wait
// at now: its last failure is less than base ago.
func (b *Backoff) Active(trackID int64, now time.Time, base time.Duration) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.m[trackID]
	return ok && now.Before(e.last.Add(base))
}

// Clear forgets trackID's failures (it got an answer).
func (b *Backoff) Clear(trackID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.m, trackID)
}

// ActiveIDs lists the tracks prefetch must skip at now (the doubled
// backoff), dropping entries quiet for longer than max past their backoff.
func (b *Backoff) ActiveIDs(now time.Time, max time.Duration) []any {
	b.mu.Lock()
	defer b.mu.Unlock()
	var ids []any
	for id, e := range b.m {
		switch {
		case now.Before(e.until):
			ids = append(ids, id)
		case now.After(e.until.Add(max)):
			delete(b.m, id)
		}
	}
	return ids
}
