package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

type reviewItem struct {
	ID             int64   `json:"id"`
	Title          string  `json:"title"`
	Artist         string  `json:"artist"`
	Album          string  `json:"album"`
	Year           *int64  `json:"year"`
	DurationS      int     `json:"duration_s"`
	Folder         string  `json:"folder"`
	Path           string  `json:"path"`
	YouTubeTitle   *string `json:"youtube_title"`
	YouTubeChannel *string `json:"youtube_channel"`
	TitleEdited    *bool   `json:"title_edited"`
	ArtistEdited   *bool   `json:"artist_edited"`
	AlbumEdited    *bool   `json:"album_edited"`
	YearEdited     *bool   `json:"year_edited"`
	HasLyrics      *bool   `json:"has_lyrics"`
	Source         string  `json:"source"`
}

type reviewFixture struct {
	s                      *Server
	ts                     *httptest.Server
	adm, kid               string
	t1, t2, d1, d2, d3, d4 int64
	raw, channel           string
}

// seedReview: t1 a plain library track (never reviewed), t2 a kid's favorite
// in the main library, d1 a YouTube download as ingest left it (cleaned
// title/artist overrides, the channel folder as album, the upload date as
// year), d2 a download that is also a favorite (favorites group), d3 a file
// in the download library with no job, d4 a trashed download.
func seedReview(t *testing.T) (*reviewFixture, func(q string) []reviewItem) {
	t.Helper()
	s, ts := newTestServer(t)
	ctx := context.Background()
	f := &reviewFixture{s: s, ts: ts, adm: loginAs(t, s, "dad", "admin"), kid: loginAs(t, s, "kid", "member")}
	var kidID int64
	if err := s.Library.DB.QueryRow(`SELECT id FROM users WHERE username='kid'`).Scan(&kidID); err != nil {
		t.Fatal(err)
	}
	dl, err := s.Library.EnsureLibrary(ctx, "youtube", t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	f.raw, f.channel = "陳瑞 - 『超高无损音質』", "POP MusicChannel"
	f.t1 = seedTrack(t, s, "a/one.mp3", "One", "A", "Alb")
	f.t2 = seedTrack(t, s, "a/two.mp3", "Two", "B", "Alb")
	f.d1 = seedTrackIn(t, s, dl.ID, "POP MusicChannel/陳瑞 [v1].m4a", "", "白狐")
	f.d2 = seedTrackIn(t, s, dl.ID, "Chan/fav [v2].m4a", "Fav", "C")
	f.d3 = seedTrackIn(t, s, dl.ID, "loose/file.m4a", "Loose", "D")
	f.d4 = seedTrackIn(t, s, dl.ID, "Chan/gone [v4].m4a", "Gone", "E")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.Library.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO favorites(user_id,track_id,created_at) VALUES (?,?,1),(?,?,1)`, kidID, f.t2, kidID, f.d2)
	exec(`UPDATE tracks SET tag_year=20190826 WHERE id=?`, f.d1)
	exec(`UPDATE tracks SET status='trashed' WHERE id=?`, f.d4)
	exec(`INSERT INTO downloads(user_id,url,video_id,title,channel,status,track_id,created_at,updated_at)
		VALUES (?,'u','v1',?,?,'done',?,1,1), (?,'u','v2','Fav video','Chan','done',?,2,2)`,
		kidID, f.raw, f.channel, f.d1, kidID, f.d2)
	exec(`INSERT INTO lyrics(track_id,source,external_id,synced,text,selected,created_at) VALUES (?,'lrclib','1',0,'x',1,1)`, f.d2)
	title, artist := ytdlp.CleanTitle(f.raw, f.channel)
	exec(`INSERT INTO track_overrides(track_id,title,artist) VALUES (?,?,?)`, f.d1, title, artist)
	for _, id := range []int64{f.d1} {
		if err := s.Library.Reindex(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	list := func(q string) []reviewItem {
		t.Helper()
		r, b := do(t, ts, f.adm, "GET", "/api/v1/admin/metadata/review"+q, nil)
		var out []reviewItem
		if r.StatusCode != 200 || json.Unmarshal(b, &out) != nil {
			t.Fatalf("%s → %d %s", q, r.StatusCode, b)
		}
		return out
	}
	return f, list
}

func reviewIDs(items []reviewItem) string {
	var s []string
	for _, it := range items {
		s = append(s, fmt.Sprint(it.ID))
	}
	return strings.Join(s, ",")
}

func idList(ids ...int64) string {
	var s []string
	for _, id := range ids {
		s = append(s, fmt.Sprint(id))
	}
	return strings.Join(s, ",")
}

// The names agent's work list: favorites (anyone's) first, then the download
// library, each by id; never plain library tracks or trashed ones.
func TestMetadataReviewList(t *testing.T) {
	f, list := seedReview(t)
	all := list("")
	if got := reviewIDs(all); got != idList(f.t2, f.d2, f.d1, f.d3) {
		t.Fatalf("all = %s, want %s", got, idList(f.t2, f.d2, f.d1, f.d3))
	}
	if got := reviewIDs(list("?scope=favorites")); got != idList(f.t2, f.d2) {
		t.Errorf("favorites = %s", got)
	}
	if got := reviewIDs(list("?scope=downloads")); got != idList(f.d1, f.d3) {
		t.Errorf("downloads = %s", got)
	}
	if got := reviewIDs(list("?limit=2")); got != idList(f.t2, f.d2) {
		t.Errorf("page 1 = %s", got)
	}
	if got := reviewIDs(list(fmt.Sprintf("?limit=2&after=%d", f.d2))); got != idList(f.d1, f.d3) {
		t.Errorf("page 2 (crosses into downloads) = %s", got)
	}
	if got := reviewIDs(list(fmt.Sprintf("?scope=downloads&after=%d", f.d1))); got != idList(f.d3) {
		t.Errorf("downloads after = %s", got)
	}

	byID := map[int64]reviewItem{}
	for _, it := range all {
		byID[it.ID] = it
	}
	d := byID[f.d1]
	title, artist := ytdlp.CleanTitle(f.raw, f.channel)
	if d.Title != title || d.Artist != artist || d.Album != f.channel || d.Year == nil || *d.Year != 20190826 ||
		d.Folder != "POP MusicChannel" || d.DurationS != 242 {
		t.Errorf("download item %+v", d)
	}
	if d.YouTubeTitle == nil || *d.YouTubeTitle != f.raw || d.YouTubeChannel == nil || *d.YouTubeChannel != f.channel {
		t.Errorf("youtube fields: %+v", d)
	}
	flags := func(id int64, want [5]bool) {
		t.Helper()
		it := byID[id]
		if it.TitleEdited == nil || it.ArtistEdited == nil || it.AlbumEdited == nil || it.YearEdited == nil || it.HasLyrics == nil {
			t.Fatalf("track %d lacks flags: %+v", id, it)
		}
		got := [5]bool{*it.TitleEdited, *it.ArtistEdited, *it.AlbumEdited, *it.YearEdited, *it.HasLyrics}
		if got != want {
			t.Errorf("track %d title/artist/album/year edited, has_lyrics = %v, want %v", id, got, want)
		}
	}
	flags(f.d1, [5]bool{false, false, false, false, false}) // all as ingest left them
	flags(f.d2, [5]bool{false, false, false, false, true})
	flags(f.t2, [5]bool{false, false, false, false, false})

	count := func(q string) string {
		t.Helper()
		r, b := do(t, f.ts, f.adm, "GET", "/api/v1/admin/metadata/review/count"+q, nil)
		if r.StatusCode != 200 {
			t.Fatalf("count%s → %d %s", q, r.StatusCode, b)
		}
		return strings.TrimSpace(string(b))
	}
	for q, n := range map[string]int{"": 4, "?scope=all": 4, "?scope=favorites": 2, "?scope=downloads": 2} {
		if got := count(q); got != fmt.Sprintf(`{"count":%d}`, n) {
			t.Errorf("count%s = %s, want %d", q, got, n)
		}
	}
	for _, p := range []string{"/api/v1/admin/metadata/review?scope=nope", "/api/v1/admin/metadata/review/count?scope=nope",
		"/api/v1/admin/metadata/review?after=x", "/api/v1/admin/metadata/review?limit=0", "/api/v1/admin/metadata/review?limit=501"} {
		if r, b := do(t, f.ts, f.adm, "GET", p, nil); r.StatusCode != 400 || !strings.Contains(string(b), `"code":"bad_request"`) {
			t.Errorf("%s → %d %s", p, r.StatusCode, b)
		}
	}
}

// review-done takes a track off the list until its displayed title, artist,
// album or year changes; outcomes are fixed|ok|skipped.
func TestMetadataReviewDoneAndRequeue(t *testing.T) {
	f, list := seedReview(t)
	put := func(id int64, body any) (int, string) {
		t.Helper()
		r, b := do(t, f.ts, f.adm, "PUT", fmt.Sprintf("/api/v1/admin/metadata/review/%d", id), body)
		return r.StatusCode, string(b)
	}
	if c, b := put(f.t2, map[string]string{"outcome": "ok"}); c != 204 {
		t.Fatalf("ok → %d %s", c, b)
	}
	if c, b := put(f.d1, map[string]string{"outcome": "skipped"}); c != 204 {
		t.Fatalf("skipped → %d %s", c, b)
	}
	if got := reviewIDs(list("")); got != idList(f.d2, f.d3) {
		t.Fatalf("after review = %s", got)
	}
	var outcome string
	var at int64
	if err := f.s.Library.DB.QueryRow(`SELECT outcome, reviewed_at FROM metadata_review WHERE track_id=?`, f.d1).Scan(&outcome, &at); err != nil || outcome != "skipped" || at <= 0 {
		t.Errorf("stored %q %d %v", outcome, at, err)
	}
	if r, b := do(t, f.ts, f.adm, "GET", "/api/v1/admin/metadata/review/count", nil); strings.TrimSpace(string(b)) != `{"count":2}` {
		t.Errorf("count after review %d %s", r.StatusCode, b)
	}
	// A later change (by anyone) re-queues it; each field counts.
	for _, patch := range []map[string]any{{"title": "Two!"}, {"artist": "B2"}, {"album": "Album 2"}, {"year": 1999}} {
		if r, b := do(t, f.ts, f.adm, "PATCH", fmt.Sprintf("/api/v1/tracks/%d", f.t2), patch); r.StatusCode != 204 {
			t.Fatalf("patch %v → %d %s", patch, r.StatusCode, b)
		}
		if got := reviewIDs(list("?scope=favorites")); got != idList(f.t2, f.d2) {
			t.Errorf("after %v favorites = %s, want t2 re-queued", patch, got)
		}
		if c, b := put(f.t2, map[string]string{"outcome": "fixed"}); c != 204 {
			t.Fatalf("fixed → %d %s", c, b)
		}
		if got := reviewIDs(list("?scope=favorites")); got != idList(f.d2) {
			t.Errorf("after re-review favorites = %s", got)
		}
	}
	for _, bad := range []any{map[string]string{"outcome": "great"}, map[string]string{}, map[string]any{"outcome": "ok", "x": 1}} {
		if c, b := put(f.d2, bad); c != 400 || !strings.Contains(b, `"code":"bad_request"`) {
			t.Errorf("%v → %d %s", bad, c, b)
		}
	}
	if c, b := put(f.d4, map[string]string{"outcome": "ok"}); c != 404 {
		t.Errorf("trashed → %d %s", c, b)
	}
	if c, b := put(999999, map[string]string{"outcome": "ok"}); c != 404 || !strings.Contains(b, `"code":"not_found"`) {
		t.Errorf("unknown → %d %s", c, b)
	}
	if r, _ := do(t, f.ts, f.kid, "PUT", fmt.Sprintf("/api/v1/admin/metadata/review/%d", f.d2), map[string]string{"outcome": "ok"}); r.StatusCode != 403 {
		t.Errorf("member → %d", r.StatusCode)
	}
}

// album_edited / year_edited: automatic iff no override, or (album) the
// override is the video's channel, which is what ingest's folder makes the
// album; anything else set on the track was typed.
func TestMetadataReviewEditedFlags(t *testing.T) {
	f, list := seedReview(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.s.Library.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE track_overrides SET album=? WHERE track_id=?`, f.channel, f.d1)
	exec(`INSERT INTO track_overrides(track_id,title,artist,album,year) VALUES (?, 'Fav (mine)', 'C', 'Real Album', 2001)`, f.d2)
	exec(`INSERT INTO track_overrides(track_id,year) VALUES (?, 1988)`, f.t2)
	for _, id := range []int64{f.d1, f.d2, f.t2} {
		if err := f.s.Library.Reindex(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	byID := map[int64]reviewItem{}
	for _, it := range list("") {
		byID[it.ID] = it
	}
	check := func(id int64, title, artist, album, year bool) {
		t.Helper()
		it := byID[id]
		got := [4]bool{*it.TitleEdited, *it.ArtistEdited, *it.AlbumEdited, *it.YearEdited}
		if want := [4]bool{title, artist, album, year}; got != want {
			t.Errorf("track %d edited = %v, want %v", id, got, want)
		}
	}
	check(f.d1, false, false, false, false) // album override = the channel: automatic
	check(f.d2, true, true, true, true)     // artist "C" is no cleaner's output of "Fav video"/"Chan"
	check(f.t2, false, false, false, true)
}

// no_album / no_year set an explicit "none", so a channel-name folder or an
// upload date stops showing; "" and 0 still clear the override as before.
func TestPatchTrackNoAlbumNoYear(t *testing.T) {
	f, list := seedReview(t)
	p := fmt.Sprintf("/api/v1/tracks/%d", f.d1)
	for _, body := range []map[string]any{{"no_album": true}, {"no_year": true}} {
		if r, b := do(t, f.ts, f.adm, "PATCH", p, body); r.StatusCode != 204 {
			t.Fatalf("%v → %d %s", body, r.StatusCode, b)
		}
	}
	var it reviewItem
	for _, x := range list("?scope=downloads") {
		if x.ID == f.d1 {
			it = x
		}
	}
	if it.Album != "" || it.Year != nil || !*it.AlbumEdited || !*it.YearEdited {
		t.Errorf("after no_album/no_year: %+v", it)
	}
	_, b := do(t, f.ts, f.adm, "GET", p, nil)
	if !strings.Contains(string(b), `"album":""`) || !strings.Contains(string(b), `"year":null`) {
		t.Errorf("track after none: %s", b)
	}
	for _, body := range []map[string]any{{"album": ""}, {"year": 0}} {
		if r, b := do(t, f.ts, f.adm, "PATCH", p, body); r.StatusCode != 204 {
			t.Fatalf("%v → %d %s", body, r.StatusCode, b)
		}
	}
	_, b = do(t, f.ts, f.adm, "GET", p, nil)
	if !strings.Contains(string(b), `"album":"POP MusicChannel"`) || !strings.Contains(string(b), `"year":20190826`) {
		t.Errorf("clearing the override brings the tagged values back: %s", b)
	}
	for _, bad := range []map[string]any{{"no_album": true, "album": "X"}, {"no_year": true, "year": 2001}} {
		if r, b := do(t, f.ts, f.adm, "PATCH", p, bad); r.StatusCode != 400 || !strings.Contains(string(b), `"code":"bad_request"`) {
			t.Errorf("%v → %d %s", bad, r.StatusCode, b)
		}
	}
}

// A changed title or artist clears the track's lyrics and artwork misses so
// lookups retry with the new names; selected/manual lyrics, a "wrong" report
// and found rows stay; other fields leave the misses alone.
func TestPatchTitleClearsLookupMisses(t *testing.T) {
	f, _ := seedReview(t)
	db := f.s.Library.DB
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO lyrics_lookup(track_id,attempted_at,found,manual) VALUES (?,100,0,0), (?,100,0,1), (?,100,1,0)`, f.d1, f.t2, f.d2)
	exec(`INSERT INTO lyrics_lookup(track_id,attempted_at,found,manual,wrong_at) VALUES (?,100,0,0,50)`, f.d3)
	exec(`INSERT INTO artwork_lookup(track_id,attempted_at,found) VALUES (?,100,0), (?,100,1), (?,100,0)`, f.d1, f.d2, f.t1)
	rows := func(table string, id int64) int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE track_id=?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	patch := func(id int64, body map[string]any) {
		t.Helper()
		if r, b := do(t, f.ts, f.adm, "PATCH", fmt.Sprintf("/api/v1/tracks/%d", id), body); r.StatusCode != 204 {
			t.Fatalf("patch %d %v → %d %s", id, body, r.StatusCode, b)
		}
	}
	patch(f.t1, map[string]any{"album": "Other", "year": 2000})
	if rows("artwork_lookup", f.t1) != 1 {
		t.Error("album/year change cleared the artwork miss")
	}
	patch(f.d1, map[string]any{"title": "白狐"})
	if rows("lyrics_lookup", f.d1) != 0 || rows("artwork_lookup", f.d1) != 0 {
		t.Error("title change kept the misses")
	}
	patch(f.t2, map[string]any{"artist": "B2"})
	patch(f.d2, map[string]any{"artist": "C2"})
	patch(f.d3, map[string]any{"title": "Loose 2"})
	if rows("lyrics_lookup", f.t2) != 1 || rows("lyrics_lookup", f.d2) != 1 || rows("artwork_lookup", f.d2) != 1 || rows("lyrics_lookup", f.d3) != 1 {
		t.Error("manual, found or reported rows were cleared")
	}
	var sel int
	if err := db.QueryRow(`SELECT COUNT(*) FROM lyrics WHERE track_id=? AND selected=1`, f.d2).Scan(&sel); err != nil || sel != 1 {
		t.Errorf("selected lyrics touched: %d %v", sel, err)
	}
	// Same value again: nothing changes, nothing is cleared.
	exec(`INSERT INTO artwork_lookup(track_id,attempted_at,found) VALUES (?,200,0)`, f.d1)
	patch(f.d1, map[string]any{"title": "白狐"})
	if rows("artwork_lookup", f.d1) != 1 {
		t.Error("an unchanged title cleared the miss")
	}
}

// The list skips reviewed tracks across more than one internal chunk (200).
func TestMetadataReviewSkipsReviewedAcrossChunks(t *testing.T) {
	s, ts := newTestServer(t)
	ctx := context.Background()
	adm := loginAs(t, s, "dad", "admin")
	dl, err := s.Library.EnsureLibrary(ctx, "youtube", t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := 0; i < 450; i++ {
		ids = append(ids, seedTrackIn(t, s, dl.ID, fmt.Sprintf("c/%03d.m4a", i), fmt.Sprintf("S%d", i), "A"))
	}
	for _, id := range ids[:420] {
		if r, b := do(t, ts, adm, "PUT", fmt.Sprintf("/api/v1/admin/metadata/review/%d", id), map[string]string{"outcome": "ok"}); r.StatusCode != 204 {
			t.Fatalf("%d → %d %s", id, r.StatusCode, b)
		}
	}
	r, b := do(t, ts, adm, "GET", "/api/v1/admin/metadata/review?scope=downloads&limit=5", nil)
	var out []reviewItem
	if r.StatusCode != 200 || json.Unmarshal(b, &out) != nil || reviewIDs(out) != idList(ids[420:425]...) {
		t.Errorf("list → %d %s", r.StatusCode, b)
	}
	if _, b := do(t, ts, adm, "GET", "/api/v1/admin/metadata/review/count?scope=downloads", nil); strings.TrimSpace(string(b)) != `{"count":30}` {
		t.Errorf("count %s", b)
	}
}

// source tells a YouTube download (download library or a done job) from a
// plain library file; GET /admin/metadata/review/{id} serves one item
// whether or not it was reviewed.
func TestMetadataReviewSourceAndOne(t *testing.T) {
	f, list := seedReview(t)
	src := map[int64]string{}
	for _, it := range list("") {
		src[it.ID] = it.Source
	}
	if src[f.t2] != "library" || src[f.d1] != "download" || src[f.d2] != "download" || src[f.d3] != "download" {
		t.Errorf("sources %v", src)
	}
	one := func(id int64) (int, reviewItem, string) {
		t.Helper()
		r, b := do(t, f.ts, f.adm, "GET", fmt.Sprintf("/api/v1/admin/metadata/review/%d", id), nil)
		var it reviewItem
		_ = json.Unmarshal(b, &it)
		return r.StatusCode, it, string(b)
	}
	if c, it, b := one(f.d1); c != 200 || it.ID != f.d1 || it.Source != "download" || it.Album != f.channel || it.AlbumEdited == nil {
		t.Errorf("one d1 → %d %s", c, b)
	}
	if r, b := do(t, f.ts, f.adm, "PUT", fmt.Sprintf("/api/v1/admin/metadata/review/%d", f.t1), map[string]string{"outcome": "ok"}); r.StatusCode != 204 {
		t.Fatalf("review t1 %d %s", r.StatusCode, b)
	}
	if c, it, b := one(f.t1); c != 200 || it.Source != "library" { // reviewed, and not even on the list: still served
		t.Errorf("one t1 → %d %s", c, b)
	}
	for _, id := range []int64{f.d4, 999999} {
		if c, _, b := one(id); c != 404 || !strings.Contains(b, `"code":"not_found"`) {
			t.Errorf("one %d → %d %s", id, c, b)
		}
	}
	if r, _ := do(t, f.ts, f.kid, "GET", fmt.Sprintf("/api/v1/admin/metadata/review/%d", f.d1), nil); r.StatusCode != 403 {
		t.Errorf("member → %d", r.StatusCode)
	}
}
