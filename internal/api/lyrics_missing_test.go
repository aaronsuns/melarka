package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aaronsuns/lark-server/internal/tags"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// seedTrackIn is seedTrack in another library (e.g. the download target).
func seedTrackIn(t *testing.T, s *Server, libID int64, rel, title, artist string) int64 {
	t.Helper()
	ctx := context.Background()
	res, err := s.Library.DB.ExecContext(ctx, `INSERT INTO tracks(library_id,rel_path,size,mtime,fingerprint,duration_ms,codec,bitrate,
		tag_title,tag_artist,tag_album,status,added_at) VALUES (?,?,1,1,?,241600,'m4a',128,?,?,'','kept',1)`, libID, rel, rel, title, artist)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if err := s.Library.Reindex(ctx, id); err != nil {
		t.Fatal(err)
	}
	return id
}

type missingItem struct {
	ID             int64   `json:"id"`
	Title          string  `json:"title"`
	Artist         string  `json:"artist"`
	Album          string  `json:"album"`
	Folder         string  `json:"folder"`
	Path           string  `json:"path"`
	DurationS      int     `json:"duration_s"`
	YouTubeTitle   *string `json:"youtube_title"`
	YouTubeChannel *string `json:"youtube_channel"`
	LastLookupAt   *int64  `json:"last_lookup_at"`
	Found          *bool   `json:"found"`
	TitleEdited    *bool   `json:"title_edited"`
	ArtistEdited   *bool   `json:"artist_edited"`
}

// The lyrics agent's work list: visible tracks with no selected lyrics and
// no instrumental tag, anyone's favorites first, then downloads, then the
// rest, each by id; scope narrows to one group; after pages.
func TestLyricsMissingList(t *testing.T) {
	s, ts := newTestServer(t)
	ctx := context.Background()
	adm := loginAs(t, s, "dad", "admin")
	kid := loginAs(t, s, "kid", "member")
	var kidID int64
	if err := s.Library.DB.QueryRow(`SELECT id FROM users WHERE username='kid'`).Scan(&kidID); err != nil {
		t.Fatal(err)
	}
	dl, err := s.Library.EnsureLibrary(ctx, "youtube", t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	t1 := seedTrack(t, s, "a/one.mp3", "One", "A", "Alb")
	t2 := seedTrack(t, s, "a/two.mp3", "Two", "A", "Alb") // kid's favorite
	t3 := seedTrack(t, s, "a/three.mp3", "Three", "A", "Alb")
	t4 := seedTrack(t, s, "a/four.mp3", "Four", "A", "Alb")
	t5 := seedTrack(t, s, "a/five.mp3", "Five", "A", "Alb")
	t6 := seedTrack(t, s, "a/six.mp3", "Six", "A", "Alb")
	t7 := seedTrack(t, s, "a/seven.mp3", "Seven", "A", "Alb")
	const raw = "盧冠廷 莫文蔚【一生所愛 Love In A Life Time】電影「大话西游」插曲"
	d1 := seedTrackIn(t, s, dl.ID, "华音殿/一生所愛.m4a", "一生所愛", "盧冠廷, 莫文蔚")
	d2 := seedTrackIn(t, s, dl.ID, "x/fav.m4a", "Fav", "B") // favorite and download: favorites group
	db := s.Library.DB
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO favorites(user_id,track_id,created_at) VALUES (?,?,1),(?,?,1)`, kidID, t2, kidID, d2)
	exec(`INSERT INTO lyrics(track_id,source,external_id,synced,text,selected,created_at) VALUES (?,'lrclib','1',0,'x',1,1)`, t3)
	exec(`UPDATE tracks SET status='trashed' WHERE id=?`, t4)
	exec(`UPDATE tracks SET missing_since=1 WHERE id=?`, t5)
	if err := s.Tags.Replace(ctx, t6, "lastfm", []tags.Tag{{Name: "instrumental", Kind: "genre"}}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO lyrics(track_id,source,external_id,synced,text,selected,created_at) VALUES (?,'lrclib','2',0,'y',0,1)`, t7)
	exec(`INSERT INTO lyrics_lookup(track_id,attempted_at,found,manual) VALUES (?,100,0,0)`, t7)
	exec(`INSERT INTO downloads(user_id,url,video_id,title,channel,status,track_id,created_at,updated_at)
		VALUES (?,'u','old','older title','old chan','failed',?,1,1), (?,'u','v1',?,'華音殿Music Channel','done',?,2,2)`,
		kidID, d1, kidID, raw, d1)

	// Overrides: automatic (equal to any cleaner version of a done job's title, or empty) vs typed by hand.
	autoTitle, _ := ytdlp.CleanTitle(raw, "華音殿Music Channel")
	_, v1Artist := ytdlp.CleanTitleV1("邓丽君演唱《甜蜜蜜》", "Chan")
	exec(`INSERT INTO downloads(user_id,url,video_id,title,channel,status,track_id,created_at,updated_at)
		VALUES (?,'u','v2','邓丽君演唱《甜蜜蜜》','Chan','done',?,3,3)`, kidID, d2)
	exec(`INSERT INTO track_overrides(track_id,title,artist) VALUES (?,?,'卢冠廷, 莫文蔚'), (?,'',?), (?,'Two (edit)',NULL)`,
		d1, autoTitle, d2, v1Artist, t2)
	for _, id := range []int64{d1, d2, t2} {
		if err := s.Library.Reindex(ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	list := func(q string) []missingItem {
		t.Helper()
		r, b := do(t, ts, adm, "GET", "/api/v1/admin/lyrics/missing"+q, nil)
		var out []missingItem
		if r.StatusCode != 200 || json.Unmarshal(b, &out) != nil {
			t.Fatalf("%s → %d %s", q, r.StatusCode, b)
		}
		return out
	}
	ids := func(items []missingItem) string {
		var s []string
		for _, it := range items {
			s = append(s, fmt.Sprint(it.ID))
		}
		return strings.Join(s, ",")
	}
	want := func(ids ...int64) string {
		var s []string
		for _, id := range ids {
			s = append(s, fmt.Sprint(id))
		}
		return strings.Join(s, ",")
	}
	all := list("")
	if got := ids(all); got != want(t2, d2, d1, t1, t7) {
		t.Fatalf("all = %s, want %s", got, want(t2, d2, d1, t1, t7))
	}
	if got := ids(list("?scope=all&limit=2")); got != want(t2, d2) {
		t.Errorf("page 1 = %s", got)
	}
	if got := ids(list(fmt.Sprintf("?scope=all&limit=2&after=%d", d2))); got != want(d1, t1) {
		t.Errorf("page 2 = %s", got)
	}
	if got := ids(list(fmt.Sprintf("?limit=2&after=%d", t1))); got != want(t7) {
		t.Errorf("page 3 = %s", got)
	}
	if got := ids(list(fmt.Sprintf("?after=%d", t7))); got != "" {
		t.Errorf("past the end = %s", got)
	}
	if got := ids(list("?scope=favorites")); got != want(t2, d2) {
		t.Errorf("favorites = %s", got)
	}
	if got := ids(list(fmt.Sprintf("?scope=favorites&after=%d", t2))); got != want(d2) {
		t.Errorf("favorites after = %s", got)
	}
	if got := ids(list("?scope=downloads")); got != want(d1) {
		t.Errorf("downloads = %s", got)
	}

	byID := map[int64]missingItem{}
	for _, it := range all {
		byID[it.ID] = it
	}
	d := byID[d1]
	if d.Title != autoTitle || d.Artist != "卢冠廷, 莫文蔚" || d.Folder != "华音殿" || d.Path != "华音殿/一生所愛.m4a" || d.DurationS != 242 {
		t.Errorf("download item %+v", d)
	}
	if d.YouTubeTitle == nil || *d.YouTubeTitle != raw || d.YouTubeChannel == nil || *d.YouTubeChannel != "華音殿Music Channel" {
		t.Errorf("youtube fields from the done job: %+v", d)
	}
	if d.LastLookupAt != nil || d.Found != nil {
		t.Errorf("never looked up, yet %+v", d)
	}
	edited := func(id int64, title, artist bool) {
		t.Helper()
		it := byID[id]
		if it.TitleEdited == nil || it.ArtistEdited == nil || *it.TitleEdited != title || *it.ArtistEdited != artist {
			t.Errorf("track %d edited = %v/%v, want %v/%v", id, it.TitleEdited, it.ArtistEdited, title, artist)
		}
	}
	edited(d1, false, true)  // title as cleaned, artist typed by hand
	edited(d2, false, false) // empty title override, artist as the V1 cleaner made it
	edited(t2, true, false)  // not a download: any override is a person's
	edited(t1, false, false)
	if o := byID[t1]; o.YouTubeTitle != nil || o.Album != "Alb" || o.Folder != "a" {
		t.Errorf("plain item %+v", o)
	}
	if o := byID[t7]; o.LastLookupAt == nil || *o.LastLookupAt != 100 || o.Found == nil || *o.Found {
		t.Errorf("looked-up item %+v", o)
	}
	_, b := do(t, ts, adm, "GET", "/api/v1/admin/lyrics/missing", nil)
	if strings.Contains(string(b), `"youtube_title":null`) {
		t.Errorf("absent youtube fields must be omitted: %s", b)
	}

	for q, n := range map[string]int{"": 5, "?scope=all": 5, "?scope=favorites": 2, "?scope=downloads": 1} {
		r, b := do(t, ts, adm, "GET", "/api/v1/admin/lyrics/missing/count"+q, nil)
		if r.StatusCode != 200 || strings.TrimSpace(string(b)) != fmt.Sprintf(`{"count":%d}`, n) {
			t.Errorf("count%s → %d %s, want %d", q, r.StatusCode, b, n)
		}
	}
	for _, p := range []string{"/api/v1/admin/lyrics/missing?scope=nope", "/api/v1/admin/lyrics/missing/count?scope=nope",
		"/api/v1/admin/lyrics/missing?after=x", "/api/v1/admin/lyrics/missing?limit=0", "/api/v1/admin/lyrics/missing?limit=501"} {
		if r, b := do(t, ts, adm, "GET", p, nil); r.StatusCode != 400 || !strings.Contains(string(b), `"code":"bad_request"`) {
			t.Errorf("%s → %d %s", p, r.StatusCode, b)
		}
	}
	if r, _ := do(t, ts, kid, "GET", "/api/v1/admin/lyrics/missing", nil); r.StatusCode != 403 {
		t.Errorf("member → %d", r.StatusCode)
	}
}
