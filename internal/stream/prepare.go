package stream

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// PrepJob is one track to have ready at a quality tier.
type PrepJob struct {
	ID   int64
	Tier Tier
}

// Preparer pre-transcodes the next few queued tracks while the current one
// plays, so a track change never waits 10–20 s for ffmpeg (a locked iPhone
// suspends the page long before that). Strictly one job at a time, niced,
// through the playback cache's semaphore and singleflight; passthrough
// tracks are skipped, and a job only starts when a transcode slot stays
// free for playback (else it waits for the next request). The pending list is bounded: a newer request goes
// first and the stale tail of older ones falls off.
type Preparer struct {
	Service *Service
	Runner  Runner // runs its transcodes (niced)
	Max     int    // pending jobs kept
	Log     *slog.Logger

	mu      sync.Mutex
	pending []PrepJob
	running *PrepJob
	wake    chan struct{}
}

// NewPreparer gives a Preparer that transcodes with a niced ffmpeg.
func NewPreparer(svc *Service, ffmpeg string) *Preparer {
	return &Preparer{Service: svc, Runner: ExecRunner{FFmpeg: ffmpeg, Nice: true}, Max: 8, Log: svc.Log}
}

func (p *Preparer) signal() chan struct{} {
	if p.wake == nil {
		p.wake = make(chan struct{}, 1)
	}
	return p.wake
}

// Enqueue puts ids (soonest first) at the front of the pending list and
// returns how many of them are pending or running afterwards.
func (p *Preparer) Enqueue(ids []int64, t Tier) int {
	p.mu.Lock()
	seen := map[PrepJob]bool{}
	if p.running != nil {
		seen[*p.running] = true
	}
	var next []PrepJob
	n := 0
	for _, id := range ids {
		j := PrepJob{id, t}
		if seen[j] {
			if p.running != nil && *p.running == j {
				n++
			}
			continue
		}
		seen[j] = true
		next = append(next, j)
		n++
	}
	for _, j := range p.pending {
		if !seen[j] {
			seen[j] = true
			next = append(next, j)
		}
	}
	max := p.max()
	if len(next) > max {
		next = next[:max]
	}
	p.pending = next
	wake := p.signal()
	p.mu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
	return n
}

func (p *Preparer) max() int {
	if p.Max < 1 {
		return 1
	}
	return p.Max
}

func (p *Preparer) has(j PrepJob) bool {
	for _, x := range p.pending {
		if x == j {
			return true
		}
	}
	return false
}

// Idle reports that nothing is pending or running.
func (p *Preparer) Idle() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running == nil && len(p.pending) == 0
}

func (p *Preparer) pendingIDs() []int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]int64, len(p.pending))
	for i, j := range p.pending {
		out[i] = j.ID
	}
	return out
}

// Run works through the pending list until ctx ends.
func (p *Preparer) Run(ctx context.Context) {
	p.mu.Lock()
	wake := p.signal()
	p.mu.Unlock()
	for {
		p.mu.Lock()
		if len(p.pending) == 0 {
			p.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-wake:
				continue
			}
		}
		j := p.pending[0]
		p.pending = p.pending[1:]
		p.running = &j
		p.mu.Unlock()

		did, err := p.Service.Warm(ctx, j.ID, j.Tier, p.Runner)
		if errors.Is(err, ErrBusy) {
			// Playback needs the slots: keep it (unless a newer request
			// already did) and try again at the next Enqueue — no polling.
			p.mu.Lock()
			p.running = nil
			if !p.has(j) {
				p.pending = append([]PrepJob{j}, p.pending...)
				if max := p.max(); len(p.pending) > max {
					p.pending = p.pending[:max]
				}
			}
			p.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-wake:
			}
			continue
		}
		if err != nil && ctx.Err() == nil && p.Log != nil {
			p.Log.Warn("pre-transcode failed", "track", j.ID, "tier", j.Tier, "err", err)
		} else if did && p.Log != nil {
			p.Log.Debug("pre-transcoded", "track", j.ID, "tier", j.Tier)
		}

		p.mu.Lock()
		p.running = nil
		p.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
	}
}
