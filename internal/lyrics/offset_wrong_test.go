package lyrics

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// lrcAt: three synced lines w, w2, w3 starting at second at.
func lrcAt(at int, w string) string {
	return fmt.Sprintf("[00:%02d.00]%s\n[00:%02d.00]%s2\n[00:%02d.00]%s3", at, w, at+1, w, at+2, w)
}

func lineText(r Result) string {
	if len(r.Lines) > 0 {
		return r.Lines[0].Text
	}
	return r.Text
}

// The offset belongs to the selected lyrics: kept while they stay selected
// (a refresh that picks them again included), clamped to ±30 s, and back to
// 0 whenever another candidate — or nothing — becomes selected.
func TestOffsetClampAndResetOnReselect(t *testing.T) {
	e := newLyricsEnv(t, &fakeProv{name: "lrclib", cands: []Candidate{
		{Source: "lrclib", ExternalID: "a", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211, Synced: true, Text: lrcAt(1, "a")},
		{Source: "lrclib", ExternalID: "b", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 213, Synced: true, Text: lrcAt(1, "b")},
	}})
	if err := e.svc.SetOffset(e.ctx, e.track, 0, 500); !errors.Is(err, ErrNoLyrics) {
		t.Fatalf("offset with nothing selected: %v", err)
	}
	r, err := e.svc.Get(e.ctx, e.track)
	if err != nil || lineText(r) != "a" || r.OffsetMS != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if err := e.svc.SetOffset(e.ctx, e.track+1, 0, 500); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing track: %v", err)
	}
	for _, c := range []struct{ in, want int }{{1500, 1500}, {99999, 30000}, {-99999, -30000}, {-1200, -1200}} {
		if err := e.svc.SetOffset(e.ctx, e.track, 0, c.in); err != nil {
			t.Fatal(err)
		}
		if r, _ := e.svc.Get(e.ctx, e.track); r.OffsetMS != c.want {
			t.Fatalf("set %d: got %d want %d", c.in, r.OffsetMS, c.want)
		}
	}
	if r, _ := e.svc.Lookup(e.ctx, e.track); lineText(r) != "a" || r.OffsetMS != -1200 {
		t.Fatalf("a refresh keeping the same lyrics must keep the offset: %+v", r)
	}
	cs, _ := e.svc.Candidates(e.ctx, e.track)
	var a, b int64
	for _, c := range cs {
		if strings.HasPrefix(c.Preview, "a") {
			a = c.ID
		} else {
			b = c.ID
		}
	}
	if err := e.svc.SetOffset(e.ctx, e.track, b, 100); !errors.Is(err, ErrLyricsChanged) {
		t.Fatalf("offset for lyrics no longer shown: %v", err)
	}
	if err := e.svc.Select(e.ctx, e.track, b); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.svc.Get(e.ctx, e.track); lineText(r) != "b" || r.OffsetMS != 0 {
		t.Fatalf("other lyrics selected: %+v", r)
	}
	if err := e.svc.Select(e.ctx, e.track, a); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.svc.Get(e.ctx, e.track); r.OffsetMS != 0 {
		t.Fatalf("selecting the first again starts from 0: %+v", r)
	}
	e.svc.SetOffset(e.ctx, e.track, 0, 700)
	if err := e.svc.MarkNone(e.ctx, e.track); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Select(e.ctx, e.track, a); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.svc.Get(e.ctx, e.track); r.OffsetMS != 0 {
		t.Fatalf("after no lyrics: %+v", r)
	}
}

func missingIDs(t *testing.T, e lyricsEnv) map[int64]Missing {
	t.Helper()
	ms, err := e.svc.MissingList(e.ctx, MissingAll, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int64]Missing{}
	for _, m := range ms {
		out[m.ID] = m
	}
	return out
}

// "Wrong lyrics": the selected words are tombstoned (from every source),
// the next best remaining candidate is selected (synced first, then the
// closest duration); with none left the track is flagged for the agent and
// lookups never bring the rejected words back.
func TestReportWrong(t *testing.T) {
	p := &fakeProv{name: "lrclib", cands: []Candidate{
		{Source: "lrclib", ExternalID: "a", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211, Synced: true, Text: lrcAt(1, "a")},
		{Source: "lrclib", ExternalID: "a2", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 212, Synced: true, Text: lrcAt(5, "a")}, // same words
		{Source: "lrclib", ExternalID: "far", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 214, Synced: true, Text: lrcAt(1, "far")},
		{Source: "lrclib", ExternalID: "near", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 210, Synced: true, Text: lrcAt(1, "near")},
		{Source: "lrclib", ExternalID: "plain", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211, Text: "plain"},
	}}
	e := newLyricsEnv(t, p)
	u := e.user("kid")
	if _, _, err := e.svc.ReportWrong(e.ctx, e.track, 1, u); !errors.Is(err, ErrNoLyrics) {
		t.Fatalf("nothing selected yet: %v", err)
	}
	r, _ := e.svc.Get(e.ctx, e.track)
	if lineText(r) != "a" || r.ID == 0 {
		t.Fatalf("closest synced first, with its id: %+v", r)
	}
	if _, _, err := e.svc.ReportWrong(e.ctx, e.track+1, r.ID, u); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing track: %v", err)
	}
	if _, _, err := e.svc.ReportWrong(e.ctx, e.track, r.ID+1000, u); !errors.Is(err, ErrLyricsChanged) {
		t.Fatalf("other lyrics than shown: %v", err)
	}
	e.svc.SetOffset(e.ctx, e.track, 0, 900)
	want := []string{"near", "far", "plain"}
	for _, w := range want {
		next, rep, err := e.svc.ReportWrong(e.ctx, e.track, r.ID, u)
		if err != nil || !next.Found || lineText(next) != w || next.OffsetMS != 0 || rep == 0 {
			t.Fatalf("want %q: %+v %d %v", w, next, rep, err)
		}
		r = next
		if _, ok := missingIDs(t, e)[e.track]; ok {
			t.Fatal("a track with lyrics is in the missing list")
		}
	}
	cs, _ := e.svc.Candidates(e.ctx, e.track)
	if len(cs) != 1 {
		t.Fatalf("the rejected words (both copies of a) are gone: %v", sources(cs))
	}
	r, _, err := e.svc.ReportWrong(e.ctx, e.track, r.ID, u)
	if err != nil || r.Found {
		t.Fatalf("nothing left: %+v %v", r, err)
	}
	if f, m := lookupState(t, e); f != 0 || m != 0 {
		t.Fatalf("found=%d manual=%d", f, m)
	}
	m, ok := missingIDs(t, e)[e.track]
	if !ok || !m.ReportedWrong {
		t.Fatalf("flagged for the agent: %+v %v", m, ok)
	}
	if ms, _ := e.svc.MissingListFiltered(e.ctx, MissingAll, true, 0, 100); len(ms) != 1 || ms[0].ID != e.track {
		t.Fatalf("reported filter: %+v", ms)
	}
	if n, _ := e.svc.MissingCountFiltered(e.ctx, MissingAll, true); n != 1 {
		t.Fatalf("reported count %d", n)
	}
	// Lookups (explicit or not) never bring the rejected words back.
	if r, _ := e.svc.Lookup(e.ctx, e.track); r.Found {
		t.Fatalf("rejected words came back: %+v", r)
	}
	if cs, _ := e.svc.Candidates(e.ctx, e.track); len(cs) != 0 {
		t.Fatalf("%v", sources(cs))
	}
	if m := missingIDs(t, e)[e.track]; !m.ReportedWrong {
		t.Fatal("flag lost by a lookup that found nothing")
	}
	// New words found later: selected, and the flag clears.
	p.cands = append(p.cands, Candidate{Source: "lrclib", ExternalID: "new", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211, Synced: true, Text: lrcAt(1, "new")})
	if r, _ := e.svc.Lookup(e.ctx, e.track); lineText(r) != "new" {
		t.Fatalf("%+v", r)
	}
	var wrongAt *int64
	e.db.QueryRow(`SELECT wrong_at FROM lyrics_lookup WHERE track_id=?`, e.track).Scan(&wrongAt)
	if wrongAt != nil {
		t.Fatal("wrong_at not cleared once lyrics were found")
	}
	if !strings.Contains(strings.Join(sources(mustCands(t, e)), ","), "*lrclib:new") {
		t.Fatal("new lyrics not selected")
	}
}

func (e lyricsEnv) user(name string) int64 {
	res, err := e.db.Exec(`INSERT INTO users(username,password_hash,role,created_at) VALUES (?,'x','member',1)`, name)
	if err != nil {
		panic(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// The reporter can undo a report (the words come back, selected, with their
// offset); someone else cannot, an admin can. Admins see every rejection
// (who, when, a preview) and can lift one: the words come back as a
// candidate and lookups may store them again.
func TestUndoWrongAndAdminRecovery(t *testing.T) {
	p := &fakeProv{name: "lrclib", cands: []Candidate{
		{Source: "lrclib", ExternalID: "a", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211, Synced: true, Text: lrcAt(1, "a")},
		{Source: "lrclib", ExternalID: "a2", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 212, Synced: true, Text: lrcAt(5, "a")},
		{Source: "lrclib", ExternalID: "b", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 214, Synced: true, Text: lrcAt(1, "b")},
	}}
	e := newLyricsEnv(t, p)
	kid, other := e.user("kid"), e.user("other")
	r, _ := e.svc.Get(e.ctx, e.track)
	e.svc.SetOffset(e.ctx, e.track, r.ID, 1200)
	next, rep, err := e.svc.ReportWrong(e.ctx, e.track, r.ID, kid)
	if err != nil || lineText(next) != "b" {
		t.Fatalf("%+v %v", next, err)
	}
	rej, err := e.svc.Rejected(e.ctx, e.track)
	if err != nil || len(rej) != 2 || rej[0].ReportedBy == nil || *rej[0].ReportedBy != "kid" || !strings.HasPrefix(rej[0].Preview, "a\na2") || !rej[0].Restorable || rej[0].RejectedAt == 0 {
		t.Fatalf("rejected list %+v %v", rej, err)
	}
	if _, err := e.svc.UndoWrong(e.ctx, e.track, rep, other, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("someone else's undo: %v", err)
	}
	if _, err := e.svc.UndoWrong(e.ctx, e.track, rep+999, kid, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown report: %v", err)
	}
	back, err := e.svc.UndoWrong(e.ctx, e.track, rep, kid, false)
	if err != nil || lineText(back) != "a" || back.OffsetMS != 1200 {
		t.Fatalf("undo: %+v %v", back, err)
	}
	if rej, _ := e.svc.Rejected(e.ctx, e.track); len(rej) != 0 {
		t.Fatalf("tombstones left after undo: %+v", rej)
	}
	if cs := mustCands(t, e); len(cs) != 3 {
		t.Fatalf("both copies back: %v", sources(cs))
	}
	if _, err := e.svc.UndoWrong(e.ctx, e.track, rep, kid, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("undo twice: %v", err)
	}
	// Admin undo of someone's report works too.
	r, _ = e.svc.Get(e.ctx, e.track)
	_, rep, _ = e.svc.ReportWrong(e.ctx, e.track, r.ID, kid)
	if back, err := e.svc.UndoWrong(e.ctx, e.track, rep, other, true); err != nil || lineText(back) != "a" {
		t.Fatalf("admin undo: %+v %v", back, err)
	}
	// Admin recovery: lifting a rejection brings the words back as a candidate
	// (not selected), and lifts every rejection of the same words.
	r, _ = e.svc.Get(e.ctx, e.track)
	e.svc.ReportWrong(e.ctx, e.track, r.ID, kid)
	rej, _ = e.svc.Rejected(e.ctx, e.track)
	if err := e.svc.Unreject(e.ctx, e.track+1, rej[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another track: %v", err)
	}
	if err := e.svc.Unreject(e.ctx, e.track, rej[0].ID); err != nil {
		t.Fatal(err)
	}
	if rej, _ := e.svc.Rejected(e.ctx, e.track); len(rej) != 0 {
		t.Fatalf("same words still rejected: %+v", rej)
	}
	cs := mustCands(t, e)
	if len(cs) != 3 || !strings.HasPrefix(cs[0].Preview, "b") || !cs[0].Selected {
		t.Fatalf("restored as a candidate, selection kept: %v", sources(cs))
	}
	// Old tombstones (before previews were stored) list without a preview.
	e.db.Exec(`INSERT INTO lyrics_rejected(track_id,source,external_id,text_hash,rejected_at) VALUES (?,'qq','9','h',5)`, e.track)
	if rej, _ := e.svc.Rejected(e.ctx, e.track); len(rej) != 1 || rej[0].Restorable || rej[0].ReportedBy != nil {
		t.Fatalf("%+v", rej)
	}
}

func mustCands(t *testing.T, e lyricsEnv) []Stored {
	t.Helper()
	cs, err := e.svc.Candidates(e.ctx, e.track)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}
