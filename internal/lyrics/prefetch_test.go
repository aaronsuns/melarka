package lyrics

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// recProv records which tracks it was asked about, in order.
type recProv struct {
	err  error
	only int64 // when set, err is returned for this track alone
	mu   sync.Mutex
	ids  []int64
}

func (p *recProv) Name() string { return "lrclib" }

func (p *recProv) Search(ctx context.Context, q Query) ([]Candidate, error) {
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

// prefetchEnv: the env's own track is trashed so only the tracks a test adds
// are candidates.
func prefetchEnv(t *testing.T, p *recProv) (lyricsEnv, func(rel, status string, addedAt int64) int64) {
	t.Helper()
	e := newLyricsEnv(t)
	e.svc.Providers = []Provider{p}
	if _, err := e.db.Exec(`UPDATE tracks SET status='trashed' WHERE id=?`, e.track); err != nil {
		t.Fatal(err)
	}
	add := func(rel, status string, addedAt int64) int64 {
		res, err := e.db.Exec(`INSERT INTO tracks(library_id,rel_path,size,mtime,fingerprint,duration_ms,codec,bitrate,
			tag_title,tag_artist,status,added_at) SELECT library_id,?,1,1,'fp',200000,'mp3',192,?,'a',?,? FROM tracks WHERE id=?`,
			rel, rel, status, addedAt, e.track)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	return e, add
}

func runFor(e lyricsEnv, d, every time.Duration) {
	ctx, cancel := context.WithTimeout(e.ctx, d)
	defer cancel()
	e.svc.RunPrefetch(ctx, every)
}

func TestPrefetchOrderAndSkips(t *testing.T) {
	p := &recProv{}
	e, add := prefetchEnv(t, p)
	t1 := add("t1.mp3", "pending", 1)
	t2 := add("t2.mp3", "kept", 20)
	t3 := add("t3.mp3", "pending", 1)
	t4 := add("t4.mp3", "kept", 1)
	t5 := add("t5.mp3", "pending", 1)
	t6 := add("t6.mp3", "pending", 1)
	t7 := add("t7.mp3", "kept", 10)
	now := e.svc.now().Unix()
	if _, err := e.db.Exec(`INSERT INTO users(username,password_hash,role,created_at) VALUES ('bob','x','member',1)`); err != nil {
		t.Fatal(err)
	}
	mustExec(t, e, `INSERT INTO favorites(user_id,track_id,created_at) SELECT id, ?, 1 FROM users WHERE username='bob'`, t3)
	e.tag(t4, "instrumental")
	mustExec(t, e, `INSERT INTO lyrics(track_id,source,external_id,synced,text,selected,created_at) VALUES (?,'lrclib','1',0,'words',1,1)`, t5)
	mustExec(t, e, `INSERT INTO lyrics_lookup(track_id,attempted_at,found) VALUES (?,?,0)`, t6, now-86400)
	mustExec(t, e, `INSERT INTO lyrics_lookup(track_id,attempted_at,found) VALUES (?,?,0)`, t7, now-8*86400)

	runFor(e, 2*time.Second, 10*time.Millisecond)
	if got, want := p.seen(), []int64{t3, t2, t7, t1}; !slices.Equal(got, want) {
		t.Fatalf("prefetch order %v, want %v", got, want)
	}
}

func TestPrefetchOffReturnsAtOnce(t *testing.T) {
	p := &recProv{}
	e, add := prefetchEnv(t, p)
	add("t1.mp3", "pending", 1)
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

// During an outage (every provider errors) the worker must not walk the
// whole library at full rate: tracks in failure backoff are skipped and the
// worker pauses instead of moving on to the next track.
func TestPrefetchPausesDuringOutage(t *testing.T) {
	p := &recProv{err: errors.New("503")}
	e, add := prefetchEnv(t, p)
	t1 := add("t1.mp3", "kept", 2)
	add("t2.mp3", "kept", 1)
	runFor(e, 300*time.Millisecond, 10*time.Millisecond)
	if got := p.seen(); !slices.Equal(got, []int64{t1}) {
		t.Fatalf("outage: asked about %v, want only [%d]", got, t1)
	}
	if id, ok, err := e.svc.nextPrefetch(e.ctx); err != nil || !ok || id == t1 {
		t.Fatalf("next = %d ok=%v err=%v; want t2 (t1 is in failure backoff)", id, ok, err)
	}
}

func mustExec(t *testing.T, e lyricsEnv, q string, args ...any) {
	t.Helper()
	if _, err := e.db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

// One track that keeps failing must not starve the rest (real clock): its
// backoff doubles per consecutive failure, so after the FailRetry pause it
// is still backed off and the tracks below it get their turn.
func TestPrefetchOneFailingTrackDoesNotStarveTheRest(t *testing.T) {
	p := &recProv{err: errors.New("503")}
	e, add := prefetchEnv(t, p)
	t1 := add("t1.mp3", "kept", 9)
	p.only = t1
	e.svc.Now = nil // real clock
	e.svc.FailRetry = 20 * time.Millisecond
	var rest []int64
	for _, rel := range []string{"t2.mp3", "t3.mp3", "t4.mp3"} {
		rest = append(rest, add(rel, "kept", 1))
	}
	runFor(e, 600*time.Millisecond, 5*time.Millisecond)
	got := p.seen()
	for _, id := range rest {
		if !slices.Contains(got, id) {
			t.Fatalf("asked about %v; track %d below the failing t1 (%d) was never prefetched", got, id, t1)
		}
	}
}

func TestBackoffDoublesPerFailureCappedAndResetsOnSuccess(t *testing.T) {
	p := &recProv{}
	e, add := prefetchEnv(t, p)
	e.svc.FailRetry = time.Minute
	e.svc.MissRetry = 5 * time.Minute
	id := add("t1.mp3", "kept", 1)
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
	if _, err := e.svc.Lookup(e.ctx, id); err != nil { // a clean miss is an answer
		t.Fatal(err)
	}
	e.svc.backoff(id)
	e.advance(time.Minute)
	if slices.Contains(e.svc.backedOff(), any(id)) {
		t.Fatal("an answered lookup did not reset the failure count")
	}
}

// The doubling only spaces out prefetch: someone opening the song still gets
// a fresh lookup once the plain 10-minute FailRetry has passed.
func TestOnDemandUsesPlainFailRetry(t *testing.T) {
	p := &recProv{err: errors.New("503")}
	e, add := prefetchEnv(t, p)
	id := add("t1.mp3", "kept", 1)
	for i := 1; i <= 6; i++ {
		e.svc.Get(e.ctx, id)
		if n := len(p.seen()); n != i {
			t.Fatalf("on-demand lookup %d: provider asked %d times", i, n)
		}
		e.advance(11 * time.Minute)
	}
	if next, ok, err := e.svc.nextPrefetch(e.ctx); err != nil || (ok && next == id) {
		t.Fatalf("prefetch picked the six-times-failed track: %d %v %v", next, ok, err)
	}
	e.svc.Get(e.ctx, id)
	if n := len(p.seen()); n != 7 {
		t.Fatalf("on-demand request 11 minutes after the 6th failure did not look up: %d", n)
	}
}
