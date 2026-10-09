package lyrics

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/testutil"
)

type fakeProv struct {
	name  string
	cands []Candidate
	err   error
	errOn func(n int) error // when set: the error for the n-th call (0-based); overrides err
	delay time.Duration
	local bool // reads only the track's own file, like Embedded
	calls atomic.Int32
	mu    sync.Mutex
	seen  []Query
}

// queries lists the {title, artist} of every query this provider was asked, in order.
func (f *fakeProv) queries() [][2]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][2]string
	for _, q := range f.seen {
		out = append(out, [2]string{q.Title, q.Artist})
	}
	return out
}

func (f *fakeProv) Name() string { return f.name }
func (f *fakeProv) Local() bool  { return f.local }

func (f *fakeProv) Search(ctx context.Context, q Query) ([]Candidate, error) {
	n := int(f.calls.Add(1)) - 1
	f.mu.Lock()
	f.seen = append(f.seen, q)
	f.mu.Unlock()
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.errOn != nil {
		return f.cands, f.errOn(n)
	}
	return f.cands, f.err
}

type lyricsEnv struct {
	svc     *Service
	db      *sql.DB
	ctx     context.Context
	track   int64
	provs   []*fakeProv
	advance func(d time.Duration)
	tag     func(trackID int64, name string)
}

func newLyricsEnv(t *testing.T, provs ...*fakeProv) lyricsEnv {
	t.Helper()
	ctx := context.Background()
	d := testutil.DB(t)
	lib := &library.Store{DB: d}
	l, err := lib.EnsureLibrary(ctx, "main", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	res, err := d.Exec(`INSERT INTO tracks(library_id,rel_path,size,mtime,fingerprint,duration_ms,codec,bitrate,
		tag_title,tag_artist,status,added_at) VALUES (?,'01 - tianmimi.mp3',1,1,'fp',211000,'mp3',192,'tianmimi','','kept',1)`, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := d.Exec(`INSERT INTO track_overrides(track_id,title,artist) VALUES (?,'甜蜜蜜','邓丽君')`, id); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	now := time.Unix(1_800_000_000, 0)
	ps := make([]Provider, len(provs))
	for i, p := range provs {
		ps[i] = p
	}
	svc := &Service{DB: d, Library: lib, Providers: ps, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now }}
	return lyricsEnv{
		svc: svc, db: d, ctx: ctx, track: id, provs: provs,
		advance: func(dur time.Duration) { mu.Lock(); now = now.Add(dur); mu.Unlock() },
		tag: func(trackID int64, name string) {
			if _, err := d.Exec(`INSERT INTO tags(name,kind) VALUES (?, 'other') ON CONFLICT(name) DO NOTHING`, name); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`INSERT INTO track_tags(track_id,tag_id,source) SELECT ?, id, 'manual' FROM tags WHERE name=?`, trackID, name); err != nil {
				t.Fatal(err)
			}
		},
	}
}

func TestLookupRanksStoresAndSelects(t *testing.T) {
	e := newLyricsEnv(t,
		&fakeProv{name: "lrclib", cands: []Candidate{{Source: "lrclib", ExternalID: "1", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211, Text: "plain words"}}},
		&fakeProv{name: "netease", cands: []Candidate{
			{Source: "netease", ExternalID: "7", Title: "甜蜜蜜", Artist: "鄧麗君", DurationS: 210, Synced: true, Text: "[00:01.00]a\n[00:02.00]b\n[00:03.00]c"},
			{Source: "netease", ExternalID: "8", Title: "甜蜜蜜", Artist: "毛辣角", DurationS: 228, Synced: true, Text: "[00:01.00]x\n[00:02.00]y\n[00:03.00]z"},
		}})
	r, err := e.svc.Get(e.ctx, e.track)
	if err != nil || !r.Found || r.Source != "netease" || !r.Synced || len(r.Lines) != 3 {
		t.Fatalf("%+v %v", r, err)
	}
	cs, _ := e.svc.Candidates(e.ctx, e.track)
	if len(cs) != 2 { // the wrong-artist, wrong-duration candidate was never stored
		t.Fatalf("candidates %+v", cs)
	}
	plain := cs[0]
	if plain.Source != "lrclib" {
		plain = cs[1]
	}
	if plain.Preview != "plain words" || plain.Selected {
		t.Fatalf("preview %+v", plain)
	}
	if err := e.svc.Select(e.ctx, e.track, plain.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.svc.Lookup(e.ctx, e.track); r.Source != "lrclib" || r.Text != "plain words" {
		t.Fatalf("refresh must keep the admin's pick: %+v", r)
	}
	if err := e.svc.Select(e.ctx, e.track+1, plain.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("selected another track's candidate")
	}
	if r, _ := e.svc.Get(e.ctx, e.track); r.Source != "lrclib" {
		t.Fatalf("get after select: %+v", r)
	}
}

// Review focus 2
func TestBudgetAndProviderFailures(t *testing.T) {
	slow := &fakeProv{name: "netease", delay: 10 * time.Second}
	broken := &fakeProv{name: "qq", err: errors.New("bad response: invalid character '<'")}
	good := &fakeProv{name: "lrclib", cands: []Candidate{{Source: "lrclib", ExternalID: "1", Title: "甜蜜蜜", Synced: true, Text: "[00:01.00]a\n[00:02.00]b\n[00:03.00]c"}}}
	e := newLyricsEnv(t, good, slow, broken)
	e.svc.Budget = 300 * time.Millisecond
	start := time.Now()
	r, _ := e.svc.Get(e.ctx, e.track)
	if !r.Found || time.Since(start) > time.Second {
		t.Fatalf("%+v after %v", r, time.Since(start))
	}

	e2 := newLyricsEnv(t, &fakeProv{name: "lrclib", err: errors.New("dial tcp: no route")}, &fakeProv{name: "netease", err: errors.New("HTTP 403")})
	if r, _ := e2.svc.Get(e2.ctx, e2.track); r.Found {
		t.Fatal("found from nothing")
	}
	var rows int
	e2.db.QueryRow(`SELECT COUNT(*) FROM lyrics_lookup`).Scan(&rows)
	if rows != 0 {
		t.Fatal("a total outage was remembered as a 7-day miss")
	}
	e2.svc.Get(e2.ctx, e2.track)
	if e2.provs[0].calls.Load() != 1 {
		t.Fatal("failure backoff not honoured")
	}
	e2.advance(11 * time.Minute)
	e2.svc.Get(e2.ctx, e2.track)
	if e2.provs[0].calls.Load() != 2 {
		t.Fatal("not retried after the backoff")
	}
}

// A provider that ignores ctx entirely (blocks forever) must not hold the
// answer back past the budget either.
type stuckProv struct{ release chan struct{} }

func (stuckProv) Name() string { return "kugou" }
func (s stuckProv) Search(context.Context, Query) ([]Candidate, error) {
	<-s.release
	return nil, nil
}

func TestProviderIgnoringContextCannotBreakBudget(t *testing.T) {
	good := &fakeProv{name: "lrclib", cands: []Candidate{{Source: "lrclib", ExternalID: "1", Title: "甜蜜蜜", Text: "words"}}}
	e := newLyricsEnv(t, good)
	stuck := stuckProv{release: make(chan struct{})}
	defer close(stuck.release)
	e.svc.Providers = append(e.svc.Providers, stuck)
	e.svc.Budget = 200 * time.Millisecond
	start := time.Now()
	r, err := e.svc.Get(e.ctx, e.track)
	if err != nil || !r.Found || r.Text != "words" || time.Since(start) > time.Second {
		t.Fatalf("%+v %v after %v", r, err, time.Since(start))
	}

	// Only the stuck provider: the budget runs out with no answer → not a miss.
	e2 := newLyricsEnv(t)
	e2.svc.Providers = []Provider{stuck}
	e2.svc.Budget = 100 * time.Millisecond
	if r, _ := e2.svc.Get(e2.ctx, e2.track); r.Found {
		t.Fatal("found from nothing")
	}
	var rows int
	e2.db.QueryRow(`SELECT COUNT(*) FROM lyrics_lookup`).Scan(&rows)
	if rows != 0 {
		t.Fatal("a timed-out lookup was remembered as a 7-day miss")
	}
}

func TestCleanMissIsRememberedForSevenDaysAndInstrumentalSkips(t *testing.T) {
	p := &fakeProv{name: "lrclib"}
	e := newLyricsEnv(t, p)
	e.svc.Get(e.ctx, e.track)
	e.advance(6 * 24 * time.Hour)
	e.svc.Get(e.ctx, e.track)
	if p.calls.Load() != 1 {
		t.Fatalf("calls %d within 7 days", p.calls.Load())
	}
	e.advance(2 * 24 * time.Hour)
	e.svc.Get(e.ctx, e.track)
	if p.calls.Load() != 2 {
		t.Fatal("not retried after 7 days")
	}
	e.tag(e.track, "instrumental")
	e.advance(8 * 24 * time.Hour)
	if r, _ := e.svc.Get(e.ctx, e.track); !r.Instrumental || p.calls.Load() != 2 {
		t.Fatalf("instrumental track looked up: %+v", r)
	}
	if r, _ := e.svc.Lookup(e.ctx, e.track); !r.Instrumental || p.calls.Load() != 2 {
		t.Fatalf("instrumental track refreshed: %+v", r)
	}
}

// A partial outage (one provider errored, another answered "nothing") is
// still a clean miss: someone answered.
func TestPartialOutageWithCleanAnswerIsAMiss(t *testing.T) {
	e := newLyricsEnv(t, &fakeProv{name: "lrclib"}, &fakeProv{name: "netease", err: errors.New(`{"code":-462}`)})
	e.svc.Get(e.ctx, e.track)
	var found int
	if err := e.db.QueryRow(`SELECT found FROM lyrics_lookup WHERE track_id=?`, e.track).Scan(&found); err != nil || found != 0 {
		t.Fatalf("found=%d err=%v", found, err)
	}
}

func TestConcurrentGetsShareOneLookup(t *testing.T) {
	p := &fakeProv{name: "lrclib", delay: 50 * time.Millisecond, cands: []Candidate{{Source: "lrclib", ExternalID: "1", Title: "甜蜜蜜", Text: "words"}}}
	q := &fakeProv{name: "netease", delay: 50 * time.Millisecond}
	e := newLyricsEnv(t, p, q)
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, err := e.svc.Get(e.ctx, e.track); err != nil || !r.Found {
				t.Errorf("%+v %v", r, err)
			}
		}()
	}
	wg.Wait()
	if p.calls.Load() != 1 || q.calls.Load() != 1 {
		t.Fatalf("calls %d %d", p.calls.Load(), q.calls.Load())
	}
}

func TestTrashedOrMissingTrackIsNotFound(t *testing.T) {
	p := &fakeProv{name: "lrclib"}
	e := newLyricsEnv(t, p)
	if _, err := e.svc.Get(e.ctx, e.track+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	e.db.Exec(`UPDATE tracks SET status='trashed' WHERE id=?`, e.track)
	if _, err := e.svc.Get(e.ctx, e.track); !errors.Is(err, ErrNotFound) {
		t.Fatalf("trashed: %v", err)
	}
	if _, err := e.svc.Lookup(e.ctx, e.track); !errors.Is(err, ErrNotFound) {
		t.Fatalf("trashed refresh: %v", err)
	}
	if _, err := e.svc.Candidates(e.ctx, e.track); !errors.Is(err, ErrNotFound) {
		t.Fatalf("trashed candidates: %v", err)
	}
	if p.calls.Load() != 0 {
		t.Fatal("looked up a trashed track")
	}
}

func lookupRows(t *testing.T, e lyricsEnv) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM lyrics_lookup`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Ruling P20: the file's own (embedded) clean "nothing here" is no evidence
// that nobody has the lyrics — an LRCLIB outage behind it is still an outage.
func TestLocalCleanMissDoesNotHideRemoteOutage(t *testing.T) {
	remote := &fakeProv{name: "lrclib", err: errors.New("lrclib.net: HTTP 403")}
	e := newLyricsEnv(t, &fakeProv{name: "embedded", local: true}, remote)
	if r, _ := e.svc.Get(e.ctx, e.track); r.Found {
		t.Fatal("found from nothing")
	}
	if lookupRows(t, e) != 0 {
		t.Fatal("an LRCLIB outage was remembered as a 7-day miss")
	}
	e.svc.Get(e.ctx, e.track)
	if remote.calls.Load() != 1 {
		t.Fatal("failure backoff not honoured")
	}
	e.advance(11 * time.Minute)
	e.svc.Get(e.ctx, e.track)
	if remote.calls.Load() != 2 {
		t.Fatal("not retried after the backoff")
	}

	// The real Embedded provider (no sidecar, no tag reader) + a remote that
	// hangs past the budget: still an outage.
	slow := &fakeProv{name: "lrclib", delay: 10 * time.Second}
	e2 := newLyricsEnv(t)
	e2.svc.Providers = []Provider{&Embedded{}, slow}
	e2.svc.Budget = 100 * time.Millisecond
	e2.svc.Get(e2.ctx, e2.track)
	if lookupRows(t, e2) != 0 {
		t.Fatal("a timed-out LRCLIB was remembered as a 7-day miss")
	}
}

// ...but with only local providers enabled, their clean miss IS the answer.
func TestEmbeddedOnlyCleanMissIsRemembered(t *testing.T) {
	e := newLyricsEnv(t)
	e.svc.Providers = []Provider{&Embedded{}}
	if r, err := e.svc.Get(e.ctx, e.track); err != nil || r.Found {
		t.Fatalf("%+v %v", r, err)
	}
	var found int
	if err := e.db.QueryRow(`SELECT found FROM lyrics_lookup WHERE track_id=?`, e.track).Scan(&found); err != nil || found != 0 {
		t.Fatalf("found=%d err=%v", found, err)
	}
}

// store (nothing matched) and Select used to open with a read, so under
// concurrent writers SQLite answered SQLITE_BUSY_SNAPSHOT, which busy_timeout
// doesn't retry. They must write first and so only ever wait for the lock.
func TestConcurrentStoreAndSelectNeverBusy(t *testing.T) {
	e := newLyricsEnv(t)
	var cand int64
	if err := e.db.QueryRow(`INSERT INTO lyrics(track_id,source,external_id,synced,text,created_at)
		VALUES (?,'lrclib','1',0,'la la',1) RETURNING id`, e.track).Scan(&cand); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 400)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				var err error
				if g%2 == 0 {
					err = e.svc.store(e.ctx, e.track, nil, 0, true, e.svc.now())
				} else {
					err = e.svc.Select(e.ctx, e.track, cand)
				}
				if err != nil {
					errs <- err
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	n := 0
	for err := range errs {
		if n == 0 {
			t.Errorf("concurrent lyrics write: %v", err)
		}
		n++
	}
	if n > 0 {
		t.Errorf("%d of 200 writes failed", n)
	}
}

func TestLookupCleansChannelDownloadsAndKeepsTheDisplayTitle(t *testing.T) {
	hit := &fakeProv{name: "netease", cands: []Candidate{{ExternalID: "1", Title: "女儿情", Artist: "吴静", DurationS: 211, Text: "[00:01.00]鸳鸯双栖蝶双飞\n[00:05.00]满园春色惹人醉\n[00:09.00]悄悄问圣僧"}}} // ≥3 timed lines = synced
	miss := &fakeProv{name: "qq"}
	e := newLyricsEnv(t, hit, miss)
	e.db.Exec(`UPDATE track_overrides SET title='《西游记》插曲 女儿情 吴静 高清', artist='Roy Hoo' WHERE track_id=?`, e.track)
	e.db.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'a','h','member',0)`)
	e.db.Exec(`INSERT INTO downloads(user_id,url,video_id,channel,status,track_id,created_at,updated_at)
		VALUES (1,'u','abcdefghijk','Roy Hoo','done',?,0,0)`, e.track)
	r, err := e.svc.Get(e.ctx, e.track)
	if err != nil || !r.Found || !r.Synced {
		t.Fatalf("%+v %v", r, err)
	}
	if got := hit.queries(); fmt.Sprint(got) != fmt.Sprint([][2]string{{"女儿情", "吴静"}}) {
		t.Fatalf("first variant matched, no more asked: %v", got)
	}
	if got := miss.queries(); fmt.Sprint(got) != fmt.Sprint([][2]string{{"女儿情", "吴静"}, {"女儿情", ""}, {"女儿情", "Roy Hoo"}}) {
		t.Fatalf("every variant tried on a miss: %v", got)
	}
	tr, _ := e.svc.Library.Track(e.ctx, 0, e.track)
	if tr.Title != "《西游记》插曲 女儿情 吴静 高清" || tr.Artist != "Roy Hoo" {
		t.Fatalf("display changed: %q / %q", tr.Title, tr.Artist)
	}
}

func TestLookupWithOverrideAsksExactlyThat(t *testing.T) {
	p := &fakeProv{name: "lrclib", cands: []Candidate{{ExternalID: "9", Title: "女儿情", Artist: "吴静", DurationS: 211, Text: "[00:01.00]x"}}}
	e := newLyricsEnv(t, p)
	title, artist := "女儿情", "吴静"
	if r, err := e.svc.LookupWith(e.ctx, e.track, Override{Title: &title, Artist: &artist}); err != nil || !r.Found {
		t.Fatalf("%+v %v", r, err)
	}
	empty := ""
	e.svc.LookupWith(e.ctx, e.track, Override{Artist: &empty}) // title kept (甜蜜蜜, cleaned), artist dropped
	if got := p.queries(); fmt.Sprint(got) != fmt.Sprint([][2]string{{"女儿情", "吴静"}, {"甜蜜蜜", ""}}) {
		t.Fatalf("override queries: %v", got)
	}
}

// channelEnv makes the env track a YouTube download whose artist is the channel.
func channelEnv(t *testing.T, provs ...*fakeProv) lyricsEnv {
	t.Helper()
	e := newLyricsEnv(t, provs...)
	for _, q := range []string{
		`UPDATE track_overrides SET title='《西游记》插曲 女儿情 吴静 高清', artist='Roy Hoo' WHERE track_id=?`,
		`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'a','h','member',0)`,
		`INSERT INTO downloads(user_id,url,video_id,channel,status,track_id,created_at,updated_at) VALUES (1,'u','abcdefghijk','Roy Hoo','done',?,0,0)`,
	} {
		var err error
		if strings.Contains(q, "?") {
			_, err = e.db.Exec(q, e.track)
		} else {
			_, err = e.db.Exec(q)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// The B2 miss contract with fallback queries: every variant answered cleanly
// is a 7-day miss; a provider that errored on some variants but answered
// another cleanly still answered; one that errored on all of them did not.
func TestFallbackQueriesKeepTheMissContract(t *testing.T) {
	e := channelEnv(t, &fakeProv{name: "lrclib"})
	e.svc.Get(e.ctx, e.track)
	if lookupRows(t, e) != 1 || len(e.provs[0].queries()) != 3 {
		t.Fatalf("clean answers to every variant must be a miss: rows=%d queries=%v", lookupRows(t, e), e.provs[0].queries())
	}

	e2 := channelEnv(t, &fakeProv{name: "lrclib", err: errors.New("HTTP 503")})
	e2.svc.Get(e2.ctx, e2.track)
	if lookupRows(t, e2) != 0 || len(e2.provs[0].queries()) != 3 {
		t.Fatalf("an outage on every variant is not a miss: rows=%d queries=%v", lookupRows(t, e2), e2.provs[0].queries())
	}

	// The local provider ignores titles: asked once, whatever the variants.
	local := &fakeProv{name: "embedded", local: true}
	e3 := channelEnv(t, local, &fakeProv{name: "lrclib"})
	e3.svc.Get(e3.ctx, e3.track)
	if len(local.queries()) != 1 {
		t.Fatalf("local provider asked %v", local.queries())
	}
}

// Fallback queries run one after another inside the same budget: a slow
// provider can't stretch the lookup past it, and a lookup the budget cut
// short is not remembered as a miss.
func TestFallbackQueriesStayWithinTheBudget(t *testing.T) {
	slow := &fakeProv{name: "lrclib", delay: 150 * time.Millisecond}
	e := channelEnv(t, slow)
	e.svc.Budget = 250 * time.Millisecond
	start := time.Now()
	if r, _ := e.svc.Get(e.ctx, e.track); r.Found {
		t.Fatal("found from nothing")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("lookup took %v with a 250ms budget", d)
	}
	if n := len(slow.queries()); n < 1 || n > 2 { // 150ms each: the budget allows at most two to start
		t.Fatalf("queries before the budget ran out: %d", n)
	}
	if lookupRows(t, e) != 0 {
		t.Fatal("a lookup cut short by the budget was remembered as a 7-day miss")
	}
}

// Ruling Q14: a provider answered cleanly only if every variant it was asked
// answered without error (or an earlier one matched). NetEase answering the
// first variant with nothing, then hitting its -462 wall on the fallbacks,
// is an outage, not a 7-day miss.
func TestPartlyErroredFallbacksAreNotAMiss(t *testing.T) {
	wall := errors.New(`{"code":-462}`)
	netease := &fakeProv{name: "netease", errOn: func(n int) error {
		if n == 0 {
			return nil
		}
		return wall
	}}
	e := channelEnv(t, netease)
	e.svc.Get(e.ctx, e.track)
	if got := netease.queries(); fmt.Sprint(got) != fmt.Sprint([][2]string{{"女儿情", "吴静"}, {"女儿情", ""}, {"女儿情", "Roy Hoo"}}) {
		t.Fatalf("queries %v", got)
	}
	if lookupRows(t, e) != 0 {
		t.Fatal("a provider that errored on its fallbacks was remembered as a 7-day miss")
	}
	e.svc.Get(e.ctx, e.track)
	if netease.calls.Load() != 3 {
		t.Fatal("failure backoff not honoured")
	}

	// An earlier variant matched: later errors don't matter (they're never asked).
	hit := &fakeProv{name: "netease", cands: []Candidate{{ExternalID: "1", Title: "女儿情", Artist: "吴静", DurationS: 211, Text: "words"}},
		errOn: func(n int) error {
			if n == 0 {
				return nil
			}
			return wall
		}}
	e2 := channelEnv(t, hit)
	if r, _ := e2.svc.Get(e2.ctx, e2.track); !r.Found {
		t.Fatal("match on the first variant lost")
	}
}

// The video's own title feeds extra searches: the shown names are what the
// conservative cleaner stored (the work as title), the video knows the song.
func TestLookupSearchesTheVideoTitleBracket(t *testing.T) {
	hit := &fakeProv{name: "netease", cands: []Candidate{{ExternalID: "1", Title: "一生所爱", Artist: "卢冠廷", DurationS: 211, Text: "[00:01.00]从前现在过去了再不来\n[00:05.00]红红落叶长埋尘土内\n[00:09.00]开始终结总是没变改"}}}
	e := newLyricsEnv(t, hit)
	e.db.Exec(`UPDATE track_overrides SET title='大话西游', artist='盧冠廷 莫文蔚【一生所愛 Love In A Life Time】電影' WHERE track_id=?`, e.track)
	e.db.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'a','h','member',0)`)
	e.db.Exec(`INSERT INTO downloads(user_id,url,video_id,title,channel,status,track_id,created_at,updated_at)
		VALUES (1,'u','abcdefghijk','盧冠廷 莫文蔚【一生所愛 Love In A Life Time】電影「大话西游」插曲','華音殿Music Channel','done',?,0,0)`, e.track)
	r, err := e.svc.Get(e.ctx, e.track)
	if err != nil || !r.Found {
		t.Fatalf("%+v %v (asked %v)", r, err, hit.queries())
	}
	if got := hit.queries(); fmt.Sprint(got) != fmt.Sprint([][2]string{{"大话西游", "盧冠廷 莫文蔚 電影"}, {"一生所愛", "盧冠廷"}}) {
		t.Fatalf("asked %v", got)
	}
	if tr, _ := e.svc.Library.Track(e.ctx, 0, e.track); tr.Title != "大话西游" {
		t.Fatalf("display changed: %q", tr.Title)
	}
}
