package lyrics

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func lookupState(t *testing.T, e lyricsEnv) (found, manual int) {
	t.Helper()
	if err := e.db.QueryRow(`SELECT found, manual FROM lyrics_lookup WHERE track_id=?`, e.track).Scan(&found, &manual); err != nil {
		t.Fatal(err)
	}
	return found, manual
}

func sources(cs []Stored) []string {
	var out []string
	for _, c := range cs {
		s := c.Source + ":" + c.Preview
		if c.Selected {
			s = "*" + s
		}
		out = append(out, s)
	}
	return out
}

// The track is 甜蜜蜜 / 邓丽君 / 211 s (newLyricsEnv).
var broadCands = []Candidate{
	{Source: "lrclib", ExternalID: "cover", Title: "甜蜜蜜", Artist: "毛辣角", DurationS: 226, Synced: true, Text: "[00:01.00]cover"},
	{Source: "lrclib", ExternalID: "strict", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211, Text: "strict"},
	{Source: "lrclib", ExternalID: "nodur", Title: "甜蜜蜜", Artist: "某人", Text: "nodur"},
	{Source: "lrclib", ExternalID: "other", Title: "月亮代表我的心", Artist: "邓丽君", DurationS: 211, Text: "other song"},
}

func TestBroadLookupKeepsLooseMatchesAndRanksStrictFirst(t *testing.T) {
	strict := newLyricsEnv(t, &fakeProv{name: "lrclib", cands: broadCands})
	if _, err := strict.svc.Lookup(strict.ctx, strict.track); err != nil {
		t.Fatal(err)
	}
	if cs, _ := strict.svc.Candidates(strict.ctx, strict.track); len(cs) != 1 {
		t.Fatalf("a normal lookup must stay strict: %v", sources(cs))
	}

	e := newLyricsEnv(t, &fakeProv{name: "lrclib", cands: broadCands})
	r, err := e.svc.LookupWith(e.ctx, e.track, Override{Broad: true})
	if err != nil || !r.Found || r.Text != "strict" {
		t.Fatalf("the strict match must win: %+v %v", r, err)
	}
	cs, _ := e.svc.Candidates(e.ctx, e.track)
	if len(cs) != 3 {
		t.Fatalf("broad keeps every loose match, never another song: %v", sources(cs))
	}

	// Without a strict match the best loose one is selected.
	e2 := newLyricsEnv(t, &fakeProv{name: "lrclib", cands: []Candidate{broadCands[2], broadCands[0]}})
	if r, _ := e2.svc.LookupWith(e2.ctx, e2.track, Override{Broad: true}); !r.Found || (r.Text != "cover" && (len(r.Lines) == 0 || r.Lines[0].Text != "cover")) {
		t.Fatalf("closest duration first: %+v", r)
	}
}

func TestBroadLookupCapsCandidates(t *testing.T) {
	var many []Candidate
	for i := range 15 {
		many = append(many, Candidate{Source: "lrclib", ExternalID: fmt.Sprint(i), Title: "甜蜜蜜", Artist: fmt.Sprint("singer", i), Text: fmt.Sprint("words ", i)})
	}
	e := newLyricsEnv(t, &fakeProv{name: "lrclib", cands: many})
	if _, err := e.svc.LookupWith(e.ctx, e.track, Override{Broad: true}); err != nil {
		t.Fatal(err)
	}
	if cs, _ := e.svc.Candidates(e.ctx, e.track); len(cs) != 10 {
		t.Fatalf("stored %d", len(cs))
	}
}

func TestBroadLookupKeepsTheMissContract(t *testing.T) {
	down := newLyricsEnv(t, &fakeProv{name: "lrclib", err: errors.New("HTTP 503")})
	if r, _ := down.svc.LookupWith(down.ctx, down.track, Override{Broad: true}); r.Found {
		t.Fatal("found from nothing")
	}
	if lookupRows(t, down) != 0 {
		t.Fatal("a broad lookup during an outage was remembered as a miss")
	}
	clean := newLyricsEnv(t, &fakeProv{name: "lrclib"})
	clean.svc.LookupWith(clean.ctx, clean.track, Override{Broad: true})
	if f, m := lookupState(t, clean); f != 0 || m != 0 {
		t.Fatalf("clean broad miss: found=%d manual=%d", f, m)
	}
}

func TestBroadAndNormalLookupsDoNotShareACall(t *testing.T) {
	if (Override{Broad: true}).key() == (Override{}).key() {
		t.Fatal("a broad search must not join a normal one in flight")
	}
	title := "甜蜜蜜"
	if (Override{Title: &title, Broad: true}).key() == (Override{Title: &title}).key() {
		t.Fatal("same with an override")
	}
}

func TestDeleteCandidate(t *testing.T) {
	p := &fakeProv{name: "lrclib", cands: broadCands[:3]}
	e := newLyricsEnv(t, p)
	e.svc.LookupWith(e.ctx, e.track, Override{Broad: true})
	cs, _ := e.svc.Candidates(e.ctx, e.track)
	if len(cs) != 3 || !cs[0].Selected || cs[0].Preview != "strict" {
		t.Fatalf("%v", sources(cs))
	}
	if err := e.svc.DeleteCandidate(e.ctx, e.track+1, cs[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another track: %v", err)
	}
	if err := e.svc.DeleteCandidate(e.ctx, e.track, 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing candidate: %v", err)
	}
	// Deleting the selected one selects the next one the picker lists.
	if err := e.svc.DeleteCandidate(e.ctx, e.track, cs[0].ID); err != nil {
		t.Fatal(err)
	}
	after, _ := e.svc.Candidates(e.ctx, e.track)
	if len(after) != 2 || !after[0].Selected || after[0].ID != cs[1].ID {
		t.Fatalf("next best: %v", sources(after))
	}
	if r, _ := e.svc.Get(e.ctx, e.track); !r.Found || r.Source != "lrclib" {
		t.Fatalf("%+v", r)
	}
	// Deleting one that is not selected leaves the selection alone.
	if err := e.svc.DeleteCandidate(e.ctx, e.track, after[1].ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := e.svc.Candidates(e.ctx, e.track); len(left) != 1 || !left[0].Selected {
		t.Fatalf("%v", sources(left))
	}
	// Deleting the last: no lyrics, and automatic lookups don't bring them back.
	if err := e.svc.DeleteCandidate(e.ctx, e.track, after[0].ID); err != nil {
		t.Fatal(err)
	}
	if f, m := lookupState(t, e); f != 0 || m != 1 {
		t.Fatalf("found=%d manual=%d", f, m)
	}
	calls := p.calls.Load()
	e.advance(30 * 24 * time.Hour)
	if r, _ := e.svc.Get(e.ctx, e.track); r.Found || p.calls.Load() != calls {
		t.Fatalf("looked up again: %+v calls %d→%d", r, calls, p.calls.Load())
	}
	if _, ok, _ := e.svc.nextPrefetch(e.ctx); ok {
		t.Fatal("prefetch picks a track the admin emptied")
	}
	// An explicit refresh still searches, but never re-adds a deleted candidate,
	// and finding nothing new keeps the admin's "no lyrics".
	if r, _ := e.svc.LookupWith(e.ctx, e.track, Override{Broad: true}); r.Found || p.calls.Load() == calls {
		t.Fatalf("refresh: %+v", r)
	}
	if cs, _ := e.svc.Candidates(e.ctx, e.track); len(cs) != 0 {
		t.Fatalf("deleted candidates came back: %v", sources(cs))
	}
	if f, m := lookupState(t, e); f != 0 || m != 1 {
		t.Fatalf("after an empty refresh found=%d manual=%d", f, m)
	}
	// Something new found by an explicit refresh is selected and clears the mark.
	p.cands = []Candidate{{Source: "lrclib", ExternalID: "new", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211, Text: "new words"}}
	if r, _ := e.svc.Lookup(e.ctx, e.track); !r.Found || r.Text != "new words" {
		t.Fatalf("%+v", r)
	}
	if f, m := lookupState(t, e); f != 1 || m != 0 {
		t.Fatalf("found=%d manual=%d", f, m)
	}
}

func TestMarkNone(t *testing.T) {
	p := &fakeProv{name: "lrclib", cands: broadCands[:3]}
	e := newLyricsEnv(t, p)
	if r, _ := e.svc.LookupWith(e.ctx, e.track, Override{Broad: true}); !r.Found {
		t.Fatal("setup")
	}
	if err := e.svc.MarkNone(e.ctx, e.track+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing track: %v", err)
	}
	if err := e.svc.MarkNone(e.ctx, e.track); err != nil {
		t.Fatal(err)
	}
	cs, _ := e.svc.Candidates(e.ctx, e.track)
	if len(cs) != 3 || cs[0].Selected || cs[1].Selected || cs[2].Selected {
		t.Fatalf("candidates stay, none selected: %v", sources(cs))
	}
	if f, m := lookupState(t, e); f != 0 || m != 1 {
		t.Fatalf("found=%d manual=%d", f, m)
	}
	calls := p.calls.Load()
	e.advance(30 * 24 * time.Hour)
	if r, _ := e.svc.Get(e.ctx, e.track); r.Found || p.calls.Load() != calls {
		t.Fatalf("automatic lookup after none: %+v", r)
	}
	if _, ok, _ := e.svc.nextPrefetch(e.ctx); ok {
		t.Fatal("prefetch picks a no-lyrics track")
	}
	if n, _ := e.svc.MissingCount(e.ctx, MissingAll); n != 0 {
		t.Fatalf("missing count %d", n)
	}
	if l, _ := e.svc.MissingList(e.ctx, MissingAll, 0, 50); len(l) != 0 {
		t.Fatalf("missing list %v", l)
	}
	// The admin picks one after all: it shows again.
	if err := e.svc.Select(e.ctx, e.track, cs[2].ID); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.svc.Get(e.ctx, e.track); !r.Found {
		t.Fatalf("%+v", r)
	}
	if f, m := lookupState(t, e); f != 1 || m != 1 {
		t.Fatalf("found=%d manual=%d", f, m)
	}
}

// A clean miss without the admin's mark still is on the missing list.
func TestMissingListKeepsPlainMisses(t *testing.T) {
	e := newLyricsEnv(t, &fakeProv{name: "lrclib"})
	e.svc.Get(e.ctx, e.track)
	if n, _ := e.svc.MissingCount(e.ctx, MissingAll); n != 1 {
		t.Fatalf("missing count %d", n)
	}
}

// Review fix 1: the same wrong words must not come back under another
// provider or id, synced or plain.
func TestDeletedTextNeverComesBackFromAnotherSource(t *testing.T) {
	p := &fakeProv{name: "netease", cands: []Candidate{{Source: "netease", ExternalID: "1", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211, Text: "[00:01.00]wrong\n[00:02.00]words"}}}
	e := newLyricsEnv(t, p)
	e.svc.Lookup(e.ctx, e.track)
	cs, _ := e.svc.Candidates(e.ctx, e.track)
	if len(cs) != 1 {
		t.Fatalf("%v", sources(cs))
	}
	if err := e.svc.DeleteCandidate(e.ctx, e.track, cs[0].ID); err != nil {
		t.Fatal(err)
	}
	p.cands = []Candidate{
		{Source: "netease", ExternalID: "2", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211, Text: "[00:03.00]wrong\n[00:04.00]words"},
		{Source: "lrclib", ExternalID: "9", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211, Text: " wrong\nwords \n"},
		{Source: "netease", ExternalID: "1", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211, Text: "edited upstream"}, // same id: still deleted
	}
	if r, _ := e.svc.Lookup(e.ctx, e.track); r.Found {
		t.Fatalf("deleted words came back: %+v", r)
	}
	if cs, _ := e.svc.Candidates(e.ctx, e.track); len(cs) != 0 {
		t.Fatalf("%v", sources(cs))
	}
	p.cands = append(p.cands, Candidate{Source: "lrclib", ExternalID: "10", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211, Text: "right words"})
	if r, _ := e.svc.Lookup(e.ctx, e.track); !r.Found || r.Text != "right words" {
		t.Fatalf("%+v", r)
	}
}

// Review fix 4: a deleted file's own lyrics are remembered by their words
// only, so a corrected .lrc is picked up.
func TestDeletedEmbeddedLyricsAreRememberedByTextOnly(t *testing.T) {
	p := &fakeProv{name: "embedded", local: true, cands: []Candidate{{Source: "embedded", ExternalID: "sidecar", Text: "old words"}}}
	e := newLyricsEnv(t, p)
	e.svc.Lookup(e.ctx, e.track)
	cs, _ := e.svc.Candidates(e.ctx, e.track)
	if len(cs) != 1 {
		t.Fatalf("%v", sources(cs))
	}
	e.svc.DeleteCandidate(e.ctx, e.track, cs[0].ID)
	if r, _ := e.svc.Lookup(e.ctx, e.track); r.Found {
		t.Fatalf("the same old .lrc came back: %+v", r)
	}
	p.cands = []Candidate{{Source: "embedded", ExternalID: "sidecar", Text: "fixed words"}}
	if r, _ := e.svc.Lookup(e.ctx, e.track); !r.Found || r.Text != "fixed words" {
		t.Fatalf("corrected .lrc: %+v", r)
	}
}

// Review fix 2: refreshing a "no lyrics" track doesn't re-select the
// candidates the admin already declined; only something new does.
func TestRefreshAfterNoneSelectsOnlySomethingNew(t *testing.T) {
	p := &fakeProv{name: "lrclib", cands: broadCands[:3]}
	e := newLyricsEnv(t, p)
	e.svc.LookupWith(e.ctx, e.track, Override{Broad: true})
	if err := e.svc.MarkNone(e.ctx, e.track); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.svc.LookupWith(e.ctx, e.track, Override{Broad: true}); r.Found {
		t.Fatalf("declined candidates re-selected: %+v", r)
	}
	if f, m := lookupState(t, e); f != 0 || m != 1 {
		t.Fatalf("found=%d manual=%d", f, m)
	}
	p.cands = append([]Candidate{}, broadCands[:3]...)
	p.cands = append(p.cands, Candidate{Source: "lrclib", ExternalID: "fresh", Title: "甜蜜蜜", Artist: "某人", Text: "fresh words"})
	if r, _ := e.svc.LookupWith(e.ctx, e.track, Override{Broad: true}); !r.Found || r.Text != "fresh words" {
		t.Fatalf("something new: %+v", r)
	}
	if f, m := lookupState(t, e); f != 1 || m != 0 {
		t.Fatalf("found=%d manual=%d", f, m)
	}
}

// Review fix 3: an automatic lookup (prefetch, Get) racing the admin's
// "no lyrics" never overwrites it.
func TestAutomaticLookupNeverOverwritesNone(t *testing.T) {
	p := &fakeProv{name: "lrclib", cands: []Candidate{{Source: "lrclib", ExternalID: "1", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211, Text: "words"}}}
	e := newLyricsEnv(t, p)
	if err := e.svc.MarkNone(e.ctx, e.track); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.store(e.ctx, e.track, p.cands, 0, false, e.svc.now()); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.svc.Get(e.ctx, e.track); r.Found {
		t.Fatalf("%+v", r)
	}
	if f, m := lookupState(t, e); f != 0 || m != 1 {
		t.Fatalf("found=%d manual=%d", f, m)
	}
	// Prefetch's lookup is an automatic one.
	if _, err := e.svc.autoLookup(e.ctx, e.track); err != nil {
		t.Fatal(err)
	}
	if f, m := lookupState(t, e); f != 0 || m != 1 {
		t.Fatalf("after prefetch found=%d manual=%d", f, m)
	}
	// An explicit refresh still finds them.
	if r, _ := e.svc.Lookup(e.ctx, e.track); !r.Found {
		t.Fatalf("%+v", r)
	}
}
