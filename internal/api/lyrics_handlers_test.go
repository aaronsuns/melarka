package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/aaronsuns/lark-server/internal/lyrics"
)

type apiLyricsProv struct{ cands []lyrics.Candidate }

func (apiLyricsProv) Name() string { return "lrclib" }
func (p apiLyricsProv) Search(context.Context, lyrics.Query) ([]lyrics.Candidate, error) {
	return p.cands, nil
}

func TestLyricsMemberGetWithoutProviders(t *testing.T) {
	s, ts := newTestServer(t)
	kid := loginAs(t, s, "kid", "member")
	id := seedTrack(t, s, "a.mp3", "甜蜜蜜", "邓丽君", "Y")
	r, b := do(t, ts, kid, "GET", fmt.Sprintf("/api/v1/tracks/%d/lyrics", id), nil)
	if r.StatusCode != 200 || strings.TrimSpace(string(b)) != `{"found":false,"synced":false,"offset_ms":0}` {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, kid, "GET", "/api/v1/tracks/99999/lyrics", nil); r.StatusCode != 404 || !strings.Contains(string(b), `"code":"not_found"`) {
		t.Fatalf("missing track %d %s", r.StatusCode, b)
	}
	s.Library.DB.Exec(`UPDATE tracks SET status='trashed' WHERE id=?`, id)
	if r, _ := do(t, ts, kid, "GET", fmt.Sprintf("/api/v1/tracks/%d/lyrics", id), nil); r.StatusCode != 404 {
		t.Fatalf("trashed track %d", r.StatusCode)
	}
}

func TestLyricsAdminCandidatesSelectRefresh(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	kid := loginAs(t, s, "kid", "member")
	id := seedTrack(t, s, "a.mp3", "甜蜜蜜", "邓丽君", "Y")
	other := seedTrack(t, s, "b.mp3", "B", "X", "Y")
	s.Lyrics.Providers = []lyrics.Provider{apiLyricsProv{cands: []lyrics.Candidate{
		{Source: "lrclib", ExternalID: "1", Title: "甜蜜蜜", Artist: "邓丽君", Text: "plain words"},
		{Source: "lrclib", ExternalID: "2", Title: "甜蜜蜜", Artist: "鄧麗君", Synced: true, Text: "[00:01.00]a\n[00:02.00]b\n[00:03.00]c"},
	}}}
	p := fmt.Sprintf("/api/v1/tracks/%d/lyrics", id)
	r, b := do(t, ts, kid, "GET", p, nil)
	if r.StatusCode != 200 || !strings.Contains(string(b), `"found":true`) || !strings.Contains(string(b), `"t_ms":1000`) {
		t.Fatalf("get %d %s", r.StatusCode, b)
	}
	r, b = do(t, ts, adm, "GET", p+"/candidates", nil)
	var cs []lyrics.Stored
	if r.StatusCode != 200 || json.Unmarshal(b, &cs) != nil || len(cs) != 2 {
		t.Fatalf("candidates %d %s", r.StatusCode, b)
	}
	var plain lyrics.Stored
	for _, c := range cs {
		if !c.Synced {
			plain = c
		}
	}
	if r, b := do(t, ts, adm, "PUT", p, map[string]int64{"candidate_id": plain.ID}); r.StatusCode != 204 {
		t.Fatalf("select %d %s", r.StatusCode, b)
	}
	if _, b := do(t, ts, kid, "GET", p, nil); !strings.Contains(string(b), `"text":"plain words"`) {
		t.Fatalf("after select %s", b)
	}
	if r, b := do(t, ts, adm, "POST", p+"/refresh", nil); r.StatusCode != 200 || !strings.Contains(string(b), `"text":"plain words"`) {
		t.Fatalf("refresh %d %s", r.StatusCode, b)
	}
	if r, _ := do(t, ts, adm, "PUT", fmt.Sprintf("/api/v1/tracks/%d/lyrics", other), map[string]int64{"candidate_id": plain.ID}); r.StatusCode != 404 {
		t.Fatalf("select another track's candidate %d", r.StatusCode)
	}
	if r, _ := do(t, ts, adm, "PUT", p, map[string]string{"candidate": "x"}); r.StatusCode != 400 {
		t.Fatalf("bad body %d", r.StatusCode)
	}
	if r, _ := do(t, ts, adm, "GET", "/api/v1/tracks/99999/lyrics/candidates", nil); r.StatusCode != 404 {
		t.Fatalf("candidates of a missing track %d", r.StatusCode)
	}
}

type recLyrics struct {
	mu   sync.Mutex
	seen [][2]string
}

func (r *recLyrics) Name() string { return "lrclib" }
func (r *recLyrics) Search(_ context.Context, q lyrics.Query) ([]lyrics.Candidate, error) {
	r.mu.Lock()
	r.seen = append(r.seen, [2]string{q.Title, q.Artist})
	r.mu.Unlock()
	return nil, nil
}

func TestRefreshLyricsWithTitleAndArtist(t *testing.T) {
	s, ts := newTestServer(t)
	rec := &recLyrics{}
	s.Lyrics.Providers = []lyrics.Provider{rec}
	adm := loginAs(t, s, "dad", "admin")
	id := seedTrack(t, s, "a.mp3", "《西游记》插曲 女儿情 吴静 高清", "Roy Hoo", "")
	p := fmt.Sprintf("/api/v1/tracks/%d/lyrics/refresh", id)
	if r, b := do(t, ts, adm, "POST", p, nil); r.StatusCode != 200 { // no body: as before
		t.Fatalf("no body: %d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, adm, "POST", p, map[string]string{"title": "女儿情", "artist": "吴静"}); r.StatusCode != 200 {
		t.Fatalf("override: %d %s", r.StatusCode, b)
	}
	rec.mu.Lock()
	got := rec.seen[len(rec.seen)-1]
	rec.mu.Unlock()
	if got != [2]string{"女儿情", "吴静"} {
		t.Fatalf("asked %v", got)
	}
	if r, _ := do(t, ts, adm, "POST", p, map[string]string{"title": "  "}); r.StatusCode != 400 {
		t.Fatalf("empty title: %d", r.StatusCode)
	}
	if r, _ := do(t, ts, adm, "POST", p, map[string]string{"album": "x"}); r.StatusCode != 400 {
		t.Fatalf("unknown field: %d", r.StatusCode)
	}
}

func TestLyricsBroadDeleteAndNone(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	kid := loginAs(t, s, "kid", "member")
	id := seedTrack(t, s, "a.mp3", "甜蜜蜜", "邓丽君", "Y")
	s.Lyrics.Providers = []lyrics.Provider{apiLyricsProv{cands: []lyrics.Candidate{
		{Source: "lrclib", ExternalID: "1", Title: "甜蜜蜜", Artist: "毛辣角", Text: "cover words"},
		{Source: "lrclib", ExternalID: "2", Title: "甜蜜蜜 (DJ版)", Artist: "某人", Text: "dj words"},
	}}}
	p := fmt.Sprintf("/api/v1/tracks/%d/lyrics", id)
	// The artist differs and there is no duration to vouch for it: only broad finds it.
	if r, b := do(t, ts, adm, "POST", p+"/refresh", nil); r.StatusCode != 200 || !strings.Contains(string(b), `"found":false`) {
		t.Fatalf("strict refresh %d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, adm, "POST", p+"/refresh", map[string]any{"broad": true, "title": "甜蜜蜜"}); r.StatusCode != 200 || !strings.Contains(string(b), `"found":true`) {
		t.Fatalf("broad refresh %d %s", r.StatusCode, b)
	}
	_, b := do(t, ts, adm, "GET", p+"/candidates", nil)
	var cs []lyrics.Stored
	if json.Unmarshal(b, &cs) != nil || len(cs) != 2 || !cs[0].Selected {
		t.Fatalf("candidates %s", b)
	}

	// Delete the selected one: the other takes over.
	if r, b := do(t, ts, adm, "DELETE", fmt.Sprintf("%s/candidates/%d", p, cs[0].ID), nil); r.StatusCode != 204 {
		t.Fatalf("delete %d %s", r.StatusCode, b)
	}
	if _, b := do(t, ts, kid, "GET", p, nil); !strings.Contains(string(b), cs[1].Preview) {
		t.Fatalf("after delete %s", b)
	}
	if r, b := do(t, ts, adm, "DELETE", fmt.Sprintf("%s/candidates/%d", p, cs[0].ID), nil); r.StatusCode != 404 || !strings.Contains(string(b), `"code":"not_found"`) {
		t.Fatalf("delete twice %d %s", r.StatusCode, b)
	}
	if r, _ := do(t, ts, adm, "DELETE", p+"/candidates/x", nil); r.StatusCode != 400 {
		t.Fatalf("bad candidate id %d", r.StatusCode)
	}

	// "No lyrics for this song".
	if r, b := do(t, ts, adm, "PUT", p, map[string]any{"none": true}); r.StatusCode != 204 {
		t.Fatalf("none %d %s", r.StatusCode, b)
	}
	if _, b := do(t, ts, kid, "GET", p, nil); !strings.Contains(string(b), `"found":false`) {
		t.Fatalf("after none %s", b)
	}
	if _, b := do(t, ts, adm, "GET", "/api/v1/admin/lyrics/missing/count?scope=all", nil); !strings.Contains(string(b), `"count":0`) {
		t.Fatalf("a no-lyrics track is still missing: %s", b)
	}
	for _, bad := range []map[string]any{{"none": true, "candidate_id": cs[1].ID}, {"none": true, "candidate_id": -1}, {"none": false}, {}} {
		if r, b := do(t, ts, adm, "PUT", p, bad); r.StatusCode != 400 || !strings.Contains(string(b), `"code":"bad_request"`) {
			t.Fatalf("%v → %d %s", bad, r.StatusCode, b)
		}
	}
	if r, _ := do(t, ts, adm, "PUT", "/api/v1/tracks/99999/lyrics", map[string]any{"none": true}); r.StatusCode != 404 {
		t.Fatalf("none on a missing track %d", r.StatusCode)
	}
	if r, _ := do(t, ts, adm, "POST", p+"/refresh", map[string]any{"broad": "yes"}); r.StatusCode != 400 {
		t.Fatalf("bad broad %d", r.StatusCode)
	}
}

// Any signed-in user may shift the lyrics (offset, clamped) and report them
// wrong; both name the lyrics they saw (409 lyrics_changed otherwise) and act
// for everyone. The reporter (or an admin) can undo a report; admins list
// rejections and lift them.
func TestLyricsOffsetAndWrongForMembers(t *testing.T) {
	s, ts := newTestServer(t)
	kid := loginAs(t, s, "kid", "member")
	bro := loginAs(t, s, "bro", "member")
	adm := loginAs(t, s, "dad", "admin")
	id := seedTrack(t, s, "a.mp3", "甜蜜蜜", "邓丽君", "Y")
	s.Lyrics.Providers = []lyrics.Provider{apiLyricsProv{cands: []lyrics.Candidate{
		{Source: "lrclib", ExternalID: "1", Title: "甜蜜蜜", Artist: "邓丽君", Synced: true, Text: "[00:01.00]first\n[00:02.00]f2\n[00:03.00]f3"},
		{Source: "lrclib", ExternalID: "2", Title: "甜蜜蜜", Artist: "鄧麗君", Text: "second words"},
	}}}
	p := fmt.Sprintf("/api/v1/tracks/%d/lyrics", id)
	if r, b := do(t, ts, kid, "PUT", p+"/offset", map[string]int{"offset_ms": 500}); r.StatusCode != 409 || !strings.Contains(string(b), `"code":"no_lyrics"`) {
		t.Fatalf("offset before lyrics: %d %s", r.StatusCode, b)
	}
	var shown struct {
		ID       int64 `json:"id"`
		Found    bool  `json:"found"`
		OffsetMS *int  `json:"offset_ms"`
		Text     string
		ReportID int64 `json:"report_id"`
	}
	get := func() {
		t.Helper()
		_, b := do(t, ts, kid, "GET", p, nil)
		shown.ID, shown.Found, shown.OffsetMS, shown.Text = 0, false, nil, ""
		if json.Unmarshal(b, &shown) != nil || shown.OffsetMS == nil {
			t.Fatalf("get %s", b)
		}
	}
	get()
	if !shown.Found || shown.ID == 0 || *shown.OffsetMS != 0 {
		t.Fatalf("%+v", shown)
	}
	first := shown.ID
	if r, b := do(t, ts, kid, "PUT", p+"/offset", map[string]int64{"offset_ms": 1500, "lyrics_id": first}); r.StatusCode != 204 {
		t.Fatalf("offset %d %s", r.StatusCode, b)
	}
	if _, b := do(t, ts, adm, "GET", p, nil); !strings.Contains(string(b), `"offset_ms":1500`) {
		t.Fatalf("offset is for everyone: %s", b)
	}
	if r, b := do(t, ts, kid, "PUT", p+"/offset", map[string]int64{"offset_ms": 1, "lyrics_id": first + 100}); r.StatusCode != 409 || !strings.Contains(string(b), `"code":"lyrics_changed"`) {
		t.Fatalf("offset for other lyrics: %d %s", r.StatusCode, b)
	}
	if r, _ := do(t, ts, kid, "PUT", p+"/offset", map[string]int{"offset_ms": -45000}); r.StatusCode != 204 {
		t.Fatal(r.StatusCode)
	}
	if _, b := do(t, ts, kid, "GET", p, nil); !strings.Contains(string(b), `"offset_ms":-30000`) {
		t.Fatalf("clamp: %s", b)
	}
	for _, body := range []any{map[string]any{}, map[string]any{"offset_ms": 1.5}, map[string]any{"offset_ms": "x"}, map[string]any{"offset": 1}} {
		if r, b := do(t, ts, kid, "PUT", p+"/offset", body); r.StatusCode != 400 || !strings.Contains(string(b), `"code":"bad_request"`) {
			t.Errorf("bad body %v: %d %s", body, r.StatusCode, b)
		}
	}
	if r, _ := do(t, ts, kid, "PUT", "/api/v1/tracks/99999/lyrics/offset", map[string]int{"offset_ms": 1}); r.StatusCode != 404 {
		t.Fatalf("missing track %d", r.StatusCode)
	}
	// Wrong needs the shown lyrics' id.
	for _, body := range []any{nil, map[string]any{}, map[string]any{"lyrics_id": "x"}} {
		if r, b := do(t, ts, kid, "POST", p+"/wrong", body); r.StatusCode != 400 {
			t.Errorf("wrong without lyrics_id %v: %d %s", body, r.StatusCode, b)
		}
	}
	if r, b := do(t, ts, kid, "POST", p+"/wrong", map[string]int64{"lyrics_id": first + 100}); r.StatusCode != 409 || !strings.Contains(string(b), `"code":"lyrics_changed"`) {
		t.Fatalf("stale wrong: %d %s", r.StatusCode, b)
	}
	r, b := do(t, ts, kid, "POST", p+"/wrong", map[string]int64{"lyrics_id": first})
	if r.StatusCode != 200 || json.Unmarshal(b, &shown) != nil || shown.Text != "second words" || *shown.OffsetMS != 0 || shown.ReportID == 0 || shown.ID == 0 {
		t.Fatalf("wrong %d %s", r.StatusCode, b)
	}
	report := shown.ReportID
	// Admin sees who rejected what.
	r, b = do(t, ts, adm, "GET", p+"/rejected", nil)
	var rej []lyrics.Rejected
	if r.StatusCode != 200 || json.Unmarshal(b, &rej) != nil || len(rej) != 1 || rej[0].ReportedBy == nil || *rej[0].ReportedBy != "kid" || !strings.HasPrefix(rej[0].Preview, "first") {
		t.Fatalf("rejected %d %s", r.StatusCode, b)
	}
	// Undo: someone else may not; the reporter may.
	undo := map[string]int64{"report_id": report}
	if r, b := do(t, ts, bro, "POST", p+"/wrong/undo", undo); r.StatusCode != 403 || !strings.Contains(string(b), `"code":"not_your_report"`) {
		t.Fatalf("other's undo %d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, kid, "POST", p+"/wrong/undo", undo); r.StatusCode != 200 || !strings.Contains(string(b), `"t_ms":1000`) || !strings.Contains(string(b), `"offset_ms":-30000`) {
		t.Fatalf("undo %d %s", r.StatusCode, b)
	}
	if r, _ := do(t, ts, kid, "POST", p+"/wrong/undo", undo); r.StatusCode != 404 {
		t.Fatalf("undo twice %d", r.StatusCode)
	}
	// Report both away: none left, the agent's list says so (also filtered).
	get()
	do(t, ts, kid, "POST", p+"/wrong", map[string]int64{"lyrics_id": shown.ID})
	get()
	if r, b := do(t, ts, kid, "POST", p+"/wrong", map[string]int64{"lyrics_id": shown.ID}); r.StatusCode != 200 || !strings.Contains(string(b), `"found":false`) {
		t.Fatalf("wrong again %d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, kid, "POST", p+"/wrong", map[string]int64{"lyrics_id": shown.ID}); r.StatusCode != 409 || !strings.Contains(string(b), `"code":"no_lyrics"`) {
		t.Fatalf("nothing to report %d %s", r.StatusCode, b)
	}
	if r, _ := do(t, ts, kid, "POST", "/api/v1/tracks/99999/lyrics/wrong", map[string]int64{"lyrics_id": 1}); r.StatusCode != 404 {
		t.Fatalf("missing track %d", r.StatusCode)
	}
	other := seedTrack(t, s, "b.mp3", "B", "X", "Y") // missing, not reported
	_, b = do(t, ts, adm, "GET", "/api/v1/admin/lyrics/missing", nil)
	if !strings.Contains(string(b), fmt.Sprintf(`"id":%d,`, id)) || !strings.Contains(string(b), `"reported_wrong":true`) || !strings.Contains(string(b), fmt.Sprintf(`"id":%d,`, other)) {
		t.Fatalf("missing list %s", b)
	}
	_, b = do(t, ts, adm, "GET", "/api/v1/admin/lyrics/missing?reported=1", nil)
	if !strings.Contains(string(b), fmt.Sprintf(`"id":%d,`, id)) || strings.Contains(string(b), fmt.Sprintf(`"id":%d,`, other)) {
		t.Fatalf("reported filter %s", b)
	}
	if _, b := do(t, ts, adm, "GET", "/api/v1/admin/lyrics/missing/count?reported=1", nil); strings.TrimSpace(string(b)) != `{"count":1}` {
		t.Fatalf("reported count %s", b)
	}
	if r, _ := do(t, ts, adm, "GET", "/api/v1/admin/lyrics/missing?reported=yes", nil); r.StatusCode != 400 {
		t.Fatalf("bad reported %d", r.StatusCode)
	}
	// Admin lifts a rejection: the words come back as a candidate.
	_, b = do(t, ts, adm, "GET", p+"/rejected", nil)
	json.Unmarshal(b, &rej)
	if r, b := do(t, ts, adm, "DELETE", fmt.Sprintf("%s/rejected/%d", p, rej[0].ID), nil); r.StatusCode != 204 {
		t.Fatalf("unreject %d %s", r.StatusCode, b)
	}
	if r, _ := do(t, ts, adm, "DELETE", fmt.Sprintf("%s/rejected/%d", p, rej[0].ID), nil); r.StatusCode != 404 {
		t.Fatalf("unreject twice %d", r.StatusCode)
	}
	if _, b := do(t, ts, adm, "GET", p+"/candidates", nil); !strings.Contains(string(b), `"preview"`) {
		t.Fatalf("restored candidate missing: %s", b)
	}
	for _, c := range [][2]string{{"PUT", p + "/offset"}, {"POST", p + "/wrong"}, {"POST", p + "/wrong/undo"}} {
		if r, _ := do(t, ts, "", c[0], c[1], map[string]int{"offset_ms": 1}); r.StatusCode != 401 {
			t.Errorf("%s %s signed out → %d", c[0], c[1], r.StatusCode)
		}
	}
}
