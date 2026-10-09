package artwork

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/lyrics"
)

// recProv records which tracks it was asked about, in order, and answers
// nothing (or err).
type recProv struct {
	local bool
	err   error
	only  int64 // when set, err is returned for this track alone
	mu    sync.Mutex
	ids   []int64
}

func (p *recProv) Name() string { return "rec" }
func (p *recProv) Local() bool  { return p.local }

func (p *recProv) Find(ctx context.Context, q lyrics.Query) ([]Found, error) {
	p.mu.Lock()
	p.ids = append(p.ids, q.TrackID)
	p.mu.Unlock()
	if p.only != 0 && q.TrackID != p.only {
		return nil, nil
	}
	return nil, p.err
}

func (p *recProv) seen() []int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.ids)
}

// addTrack inserts a track with the given status and added_at.
func (e artEnv) addTrack(t *testing.T, rel, status string, addedAt int64) int64 {
	t.Helper()
	res, err := e.db.Exec(`INSERT INTO tracks(library_id,rel_path,size,mtime,fingerprint,duration_ms,codec,bitrate,
		tag_title,tag_artist,status,added_at) VALUES (?,?,1,1,?,200000,'mp3',192,?,'a',?,?)`, e.libID, rel, rel, rel, status, addedAt)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (e artEnv) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := e.db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func runPrefetchFor(e artEnv, d, every time.Duration) {
	ctx, cancel := context.WithTimeout(e.ctx, d)
	defer cancel()
	e.svc.RunPrefetch(ctx, every)
}

func TestPrefetchOrderAndSkips(t *testing.T) {
	p := &recProv{local: true}
	e := newArtEnv(t, p)
	t1 := e.addTrack(t, "t1.mp3", "pending", 1)
	t2 := e.addTrack(t, "t2.mp3", "kept", 20)
	t3 := e.addTrack(t, "t3.mp3", "pending", 1)
	t4 := e.addTrack(t, "t4.mp3", "kept", 1)
	t5 := e.addTrack(t, "t5.mp3", "pending", 1)
	t6 := e.addTrack(t, "t6.mp3", "kept", 10)
	e.addTrack(t, "t7.mp3", "trashed", 1)
	now := e.svc.now().Unix()
	e.exec(t, `INSERT INTO users(username,password_hash,role,created_at) VALUES ('bob','x','member',1)`)
	e.exec(t, `INSERT INTO favorites(user_id,track_id,created_at) SELECT id, ?, 1 FROM users WHERE username='bob'`, t3)
	e.exec(t, `INSERT INTO artwork_lookup(track_id,attempted_at,found,source) VALUES (?,?,1,'folder')`, t4, now-86400)
	e.exec(t, `INSERT INTO artwork_lookup(track_id,attempted_at,found) VALUES (?,?,0)`, t5, now-86400)
	e.exec(t, `INSERT INTO artwork_lookup(track_id,attempted_at,found) VALUES (?,?,0)`, t6, now-31*86400)

	runPrefetchFor(e, 2*time.Second, 10*time.Millisecond)
	if got, want := p.seen(), []int64{t3, t2, t6, t1}; !slices.Equal(got, want) {
		t.Fatalf("prefetch order %v, want %v", got, want)
	}
}

func TestPrefetchOffReturnsAtOnce(t *testing.T) {
	p := &recProv{local: true}
	e := newArtEnv(t, p)
	e.addTrack(t, "t1.mp3", "pending", 1)
	done := make(chan struct{})
	go func() { defer close(done); e.svc.RunPrefetch(context.Background(), 0) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunPrefetch(ctx, 0) did not return")
	}
	if len(p.seen()) != 0 {
		t.Fatalf("looked up %v", p.seen())
	}
}

// During an outage (every online source errors) a track is asked about once
// and the worker then pauses FailRetry instead of walking the library at
// full rate; the backed-off track is not picked again.
func TestPrefetchPausesDuringOutage(t *testing.T) {
	p := &recProv{err: errors.New("HTTP 503")}
	e := newArtEnv(t, p)
	e.svc.FailRetry = time.Hour
	t1 := e.addTrack(t, "t1.mp3", "kept", 9)
	for _, rel := range []string{"t2.mp3", "t3.mp3", "t4.mp3", "t5.mp3"} { // a spinning worker would ask about all five
		e.addTrack(t, rel, "kept", 1)
	}
	runPrefetchFor(e, 300*time.Millisecond, 10*time.Millisecond)
	got := p.seen()
	if len(got) == 0 || len(got) > 2 || got[0] != t1 || slices.Contains(got[1:], t1) {
		t.Fatalf("outage: asked about %v, want t1 (%d) once and then a pause (<= 2 calls)", got, t1)
	}
	if _, _, ok := e.lookupRow(t, t1); ok {
		t.Fatal("an outage was recorded as a miss")
	}
	if id, ok, err := e.svc.nextPrefetch(e.ctx); err != nil || !ok || id == t1 {
		t.Fatalf("next = %d ok=%v err=%v; want another track (t1 is in failure backoff)", id, ok, err)
	}
}

// One track that keeps failing must not starve the rest (real clock): its
// backoff doubles per consecutive failure, so after the FailRetry pause it
// is still backed off and the tracks below it get their turn.
func TestPrefetchOneFailingTrackDoesNotStarveTheRest(t *testing.T) {
	e := newArtEnv(t)
	t1 := e.addTrack(t, "t1.mp3", "kept", 9)
	p := &recProv{err: errors.New("HTTP 503"), only: t1}
	e.svc.Providers = []Provider{p}
	e.svc.Now = nil // real clock
	e.svc.FailRetry = 20 * time.Millisecond
	var rest []int64
	for _, rel := range []string{"t2.mp3", "t3.mp3", "t4.mp3"} {
		rest = append(rest, e.addTrack(t, rel, "kept", 1))
	}
	runPrefetchFor(e, 600*time.Millisecond, 5*time.Millisecond)
	got := p.seen()
	for _, id := range rest {
		if !slices.Contains(got, id) {
			t.Fatalf("asked about %v; track %d below the failing t1 (%d) was never prefetched", got, id, t1)
		}
	}
}

func TestBackoffDoublesPerFailureCappedAndResetsOnSuccess(t *testing.T) {
	e := newArtEnv(t)
	e.svc.init()
	e.svc.FailRetry = time.Minute
	e.svc.MissRetry = 5 * time.Minute
	id := e.addTrack(t, "t1.mp3", "kept", 1)
	for i, want := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		e.svc.backoff(id)
		e.advance(want - time.Second)
		if !slices.Contains(e.svc.backedOff(), any(id)) {
			t.Fatalf("failure %d: backoff shorter than %v", i+1, want)
		}
		e.advance(time.Second)
		if slices.Contains(e.svc.backedOff(), any(id)) {
			t.Fatalf("failure %d: backoff longer than %v", i+1, want)
		}
	}
	if err := e.svc.record(e.ctx, id, false, ""); err != nil {
		t.Fatal(err)
	}
	e.svc.backoff(id)
	e.advance(time.Minute)
	if slices.Contains(e.svc.backedOff(), any(id)) {
		t.Fatal("a recorded answer did not reset the failure count")
	}
}

// The doubling only spaces out prefetch: someone opening the song still gets
// a fresh lookup once the plain 10-minute FailRetry has passed.
func TestOnDemandUsesPlainFailRetry(t *testing.T) {
	down := &countProv{err: errors.New("HTTP 503")}
	e := newArtEnv(t, down)
	id := e.track(t, "o/x.mp3", "甜蜜蜜", "邓丽君", "")
	for i := 1; i <= 6; i++ {
		e.svc.Get(e.ctx, id, 300)
		if n := down.n.Load(); n != int32(i) {
			t.Fatalf("on-demand lookup %d: provider asked %d times", i, n)
		}
		e.advance(11 * time.Minute)
	}
	if next, ok, err := e.svc.nextPrefetch(e.ctx); err != nil || (ok && next == id) {
		t.Fatalf("prefetch picked the six-times-failed track: %d %v %v", next, ok, err)
	}
	e.svc.Get(e.ctx, id, 300)
	if n := down.n.Load(); n != 7 {
		t.Fatalf("on-demand request 11 minutes after the 6th failure did not look up: %d", n)
	}
}
