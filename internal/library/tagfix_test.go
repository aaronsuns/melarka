package library

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/aaronsuns/lark-server/internal/media"
)

func TestRepairTagDecodesGBKMojibake(t *testing.T) {
	cases := map[string]string{
		"ÄÇÓ¢-Ïà¼û²»Èç»³Äî":             "那英-相见不如怀念",
		"ÌØ±ðµÄ°®¸øÌØ±ðµÄÄã":            "特别的爱给特别的你",
		"±¦±´DJÍøÌá¹©Ãâ·ÑDJÎèÇúMP3ÏÂÔØ": "宝贝DJ网提供免费DJ舞曲MP3下载",
	}
	for in, want := range cases {
		if got := repairTag(in); got != want {
			t.Errorf("repairTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRepairTagLeavesReadableTextAlone(t *testing.T) {
	for _, s := range []string{
		"Teresa Teng", "Café", "Björk", "Zi Zuo Duo Qing", "邓丽君", "",
		// Latin-1 that is not GBK mojibake: a high byte followed by an ASCII
		// byte (a valid GBK trail byte, but never in real Chinese mojibake).
		"Mötley Crüe", "Sigur Rós", "Beyoncé", "Hélène",
		// Already CJK, mixed with Latin-1-range characters.
		"邓丽君 Café",
		// Nordic names: adjacent accented letters form high-high GBK pairs
		// but carry no Latin-1 symbol (U+0080–U+00BF, ×, ÷).
		"Sääksjärvi", "Sjöö", "Hööks", "Lööv", "BÖÖ", "Öö", "Åå Ää", "Ståhl", "Måns Zelmerlöw",
		// CP1251 Cyrillic mojibake is not GBK.
		"Àíçè",
	} {
		if got := repairTag(s); got != s {
			t.Errorf("repairTag(%q) = %q, want unchanged", s, got)
		}
	}
}

func TestUnusableTag(t *testing.T) {
	for _, s := range []string{"���", "���˵�С��", "??", " ? ", "Track 7", "track07", "音轨 3", "曲目12",
		"Unknown Artist", "unknown", "<Unknown>", "未知", "未知艺术家"} {
		if !unusableTag(s) {
			t.Errorf("unusableTag(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"七里香", "Track of Love", "7 Years", "Teresa Teng", "What?"} {
		if unusableTag(s) {
			t.Errorf("unusableTag(%q) = true, want false", s)
		}
	}
}

func TestCleanTag(t *testing.T) {
	cases := map[string]string{
		"ÄÇÓ¢-Ïà¼û²»Èç»³Äî": "那英-相见不如怀念",
		"���ܾ�":             "",
		"Track 7":           "",
		"甜蜜蜜":               "甜蜜蜜",
	}
	for in, want := range cases {
		if got := cleanTag(in); got != want {
			t.Errorf("cleanTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestArtistAndTitleFromPath(t *testing.T) {
	cases := []struct{ rel, artist, title string }{
		{"经典怀旧(1)/陈星 - 离家的孩子.mp3", "陈星", "离家的孩子"},
		{"邓丽君精选/01 - 甜蜜蜜.mp3", "", "甜蜜蜜"},
		{"a/Song.mp3", "", "Song"},
		{"misc/track01.mp3", "", "track01"},
		{"x/伊能静-流浪的小孩.mp3", "伊能静", "流浪的小孩"},
		{"x/a-b-c.mp3", "", "a-b-c"},                                                             // more than one '-': not split
		{"x/2001 - Space.mp3", "", "2001 - Space"},                                               // numeric left side is not an artist
		{"x/03 - 陈星 - 离家的孩子.flac", "陈星", "离家的孩子"},                                                // track number, then artist
		{"x/This Is A Very Long Left Side-Title.mp3", "", "This Is A Very Long Left Side-Title"}, // > 20 runes
		// A single unspaced '-' splits only when the left side has CJK.
		{"x/Re-Born.mp3", "", "Re-Born"},
		{"x/Anti-Hero.mp3", "", "Anti-Hero"},
		{"x/Spider-Man.mp3", "", "Spider-Man"},
		{"x/Jay-Z.mp3", "", "Jay-Z"},
	}
	for _, c := range cases {
		if got := artistFromPath(c.rel); got != c.artist {
			t.Errorf("artistFromPath(%q) = %q, want %q", c.rel, got, c.artist)
		}
		if got := titleFromPath(c.rel, ""); got != c.title {
			t.Errorf("titleFromPath(%q) = %q, want %q", c.rel, got, c.title)
		}
	}
}

// With an artist tag the file name is not split for the title: the part
// before " - " is likely part of the title, not the artist — unless it is
// that very artist.
func TestTitleFromPathWithKnownArtist(t *testing.T) {
	cases := []struct{ rel, artist, title string }{
		{"x/甜蜜蜜 - 现场版.mp3", "邓丽君", "甜蜜蜜 - 现场版"},
		{"x/01 - 甜蜜蜜 - 现场版.mp3", "邓丽君", "甜蜜蜜 - 现场版"},
		{"x/伊能静-流浪的小孩.mp3", "Someone", "伊能静-流浪的小孩"},
		{"邓丽君精选/01 - 甜蜜蜜.mp3", "邓丽君", "甜蜜蜜"},
		{"x/陈星 - 离家的孩子.mp3", "陈星", "离家的孩子"},
		{"x/甜蜜蜜 - 现场版.mp3", "", "现场版"}, // no artist tag: the name is "Artist - Title"
	}
	for _, c := range cases {
		if got := titleFromPath(c.rel, c.artist); got != c.title {
			t.Errorf("titleFromPath(%q, %q) = %q, want %q", c.rel, c.artist, got, c.title)
		}
	}
}

func TestArtistTagKeepsTheWholeFileNameAsTitle(t *testing.T) {
	e := newEnv(t, tagProber{info: media.Info{Artist: "邓丽君", DurationMS: 1000, Codec: "mp3"}})
	rel := "现场/甜蜜蜜 - 现场版.mp3"
	writeRandom(t, filepath.Join(e.root, filepath.FromSlash(rel)))
	e.scan(t)
	var id int64
	if err := e.st.DB.QueryRow(`SELECT id FROM tracks WHERE rel_path=?`, rel).Scan(&id); err != nil {
		t.Fatal(err)
	}
	tr, err := e.st.Track(context.Background(), 1, id)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Title != "甜蜜蜜 - 现场版" || tr.Artist != "邓丽君" {
		t.Fatalf("title %q artist %q", tr.Title, tr.Artist)
	}
	if r, _ := e.st.Search(context.Background(), 1, "现场版", 10); len(r.Tracks) != 1 {
		t.Fatalf("not searchable by its file-name title: %+v", r)
	}
}

// tagProber returns fixed tags for every file.
type tagProber struct{ info media.Info }

func (p tagProber) Probe(context.Context, string) (media.Info, error) { return p.info, nil }

func TestScanCleansGarbledTagsAndFallsBackToFileName(t *testing.T) {
	e := newEnv(t, tagProber{media.Info{DurationMS: 200_000, Codec: "mp3",
		Title: "���˵�С��", Artist: "���ܾ�", Album: "Track 7", AlbumArtist: "??"}})
	rel := "经典怀旧(10)/伊能静 - 流浪的小孩(1).mp3"
	writeRandom(t, filepath.Join(e.root, filepath.FromSlash(rel)))
	e.scan(t)
	for _, col := range []string{"tag_title", "tag_artist", "tag_album", "tag_album_artist"} {
		if v := e.col(t, rel, col); v != "" {
			t.Errorf("%s = %q, want cleaned to empty", col, v)
		}
	}
	ctx := context.Background()
	p, err := e.st.Tracks(ctx, 1, TrackFilter{})
	if err != nil || len(p.Items) != 1 {
		t.Fatalf("tracks: %v %+v", err, p)
	}
	tr := p.Items[0]
	if tr.Title != "流浪的小孩(1)" || tr.Artist != "伊能静" || tr.ArtistID == nil || tr.Album != "经典怀旧(10)" {
		t.Fatalf("display: %+v", tr)
	}
	r, err := e.st.Search(ctx, 1, "伊能静", 10)
	if err != nil || len(r.Tracks) != 1 || len(r.Artists) != 1 || r.Artists[0].Name != "伊能静" {
		t.Fatalf("search: %v %+v", err, r)
	}
}

func TestScanRepairsGBKMojibakeTags(t *testing.T) {
	e := newEnv(t, tagProber{media.Info{Codec: "mp3", Title: "ÌØ±ðµÄ°®¸øÌØ±ðµÄÄã", Artist: "ÄÇÓ¢"}})
	rel := "经典怀旧(2)/x.mp3"
	writeRandom(t, filepath.Join(e.root, filepath.FromSlash(rel)))
	e.scan(t)
	if v := e.col(t, rel, "tag_title"); v != "特别的爱给特别的你" {
		t.Errorf("tag_title = %q", v)
	}
	if v := e.col(t, rel, "tag_artist"); v != "那英" {
		t.Errorf("tag_artist = %q", v)
	}
}

func TestRepairStoredTagsBackfill(t *testing.T) {
	e := newEnv(t, fakeProber{})
	ctx := context.Background()
	rels := map[string]string{
		"garbled":  "经典怀旧(10)/伊能静 - 流浪的小孩(1).mp3",
		"gbk":      "经典怀旧(1)/a.mp3",
		"override": "经典怀旧(3)/陈星 - 离家的孩子.mp3",
		"clean":    "邓丽君精选/01 - 甜蜜蜜.mp3",
		"trashed":  "经典怀旧(4)/b.mp3",
		"linkless": "经典怀旧(5)/陈星 - 星星.mp3",
	}
	for _, rel := range rels {
		writeRandom(t, filepath.Join(e.root, filepath.FromSlash(rel)))
	}
	e.scan(t)
	id := func(k string) int64 {
		var id int64
		if err := e.st.DB.QueryRow(`SELECT id FROM tracks WHERE rel_path=?`, rels[k]).Scan(&id); err != nil {
			t.Fatal(k, err)
		}
		return id
	}
	setTags := func(k, title, artist, album string) {
		if _, err := e.st.DB.Exec(`UPDATE tracks SET tag_title=?, tag_artist=?, tag_album=? WHERE id=?`, title, artist, album, id(k)); err != nil {
			t.Fatal(err)
		}
		if err := e.st.Reindex(ctx, id(k)); err != nil {
			t.Fatal(err)
		}
	}
	// Rows stored before cleaning existed.
	setTags("garbled", "���˵�С��", "���ܾ�", "")
	setTags("gbk", "ÌØ±ðµÄ°®¸øÌØ±ðµÄÄã", "ÄÇÓ¢", "")
	setTags("override", "Track 7", "???", "")
	setTags("clean", "甜蜜蜜", "邓丽君", "")
	setTags("trashed", "���", "", "")
	if _, err := e.st.DB.Exec(`UPDATE tracks SET status='trashed' WHERE id=?`, id("trashed")); err != nil {
		t.Fatal(err)
	}
	// Indexed before the file-name artist fallback existed: clean (empty)
	// tags, but no artist link.
	setTags("linkless", "", "", "")
	if _, err := e.st.DB.Exec(`UPDATE tracks SET artist_id=NULL WHERE id=?`, id("linkless")); err != nil {
		t.Fatal(err)
	}
	title := "离家的孩子(手动)"
	if err := e.st.SetOverrides(ctx, id("override"), Overrides{Title: &title}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"garbled", "gbk", "override", "clean", "trashed", "linkless"} {
		if _, err := e.st.DB.Exec(`INSERT INTO tagging_state(track_id, agent_at, lastfm_at) VALUES (?,1,1)`, id(k)); err != nil {
			t.Fatal(err)
		}
	}
	// The garbled track's album was linked to the garbled artist (the
	// album upsert keeps an existing artist link).
	if _, err := e.st.DB.Exec(`UPDATE albums SET artist_id=(SELECT artist_id FROM tracks WHERE id=?)
		WHERE id=(SELECT album_id FROM tracks WHERE id=?)`, id("garbled"), id("garbled")); err != nil {
		t.Fatal(err)
	}
	// Lookup answers built from the garbled tags: misses on repaired tracks
	// go (they were searched with garbage), hits and admin picks stay.
	for _, q := range []struct {
		sql string
		k   string
	}{
		{`INSERT INTO lyrics_lookup(track_id,attempted_at,found,manual) VALUES (?,1,0,0)`, "gbk"},
		{`INSERT INTO artwork_lookup(track_id,attempted_at,found) VALUES (?,1,0)`, "gbk"},
		{`INSERT INTO lyrics_lookup(track_id,attempted_at,found,manual) VALUES (?,1,0,1)`, "garbled"},
		{`INSERT INTO artwork_lookup(track_id,attempted_at,found,source) VALUES (?,1,1,'embedded')`, "garbled"},
		{`INSERT INTO lyrics_lookup(track_id,attempted_at,found,manual) VALUES (?,1,0,0)`, "clean"},
		{`INSERT INTO artwork_lookup(track_id,attempted_at,found) VALUES (?,1,0)`, "clean"},
	} {
		if _, err := e.st.DB.Exec(q.sql, id(q.k)); err != nil {
			t.Fatal(err)
		}
	}

	n, err := e.st.RepairStoredTags(ctx)
	if err != nil || n != 4 {
		t.Fatalf("first run: n=%d err=%v, want 3 repaired + 1 relinked", n, err)
	}
	get := func(k string) Track {
		tr, err := e.st.Track(ctx, 1, id(k))
		if err != nil {
			t.Fatal(k, err)
		}
		return tr
	}
	if tr := get("garbled"); tr.Title != "流浪的小孩(1)" || tr.Artist != "伊能静" || tr.ArtistID == nil {
		t.Errorf("garbled: %+v", tr)
	}
	if tr := get("gbk"); tr.Title != "特别的爱给特别的你" || tr.Artist != "那英" {
		t.Errorf("gbk: %+v", tr)
	}
	if tr := get("override"); tr.Title != "离家的孩子(手动)" || tr.Artist != "陈星" {
		t.Errorf("override: %+v", tr)
	}
	if tr := get("clean"); tr.Title != "甜蜜蜜" || tr.Artist != "邓丽君" {
		t.Errorf("clean: %+v", tr)
	}
	if v := e.col(t, rels["trashed"], "tag_title"); v != "���" {
		t.Errorf("trashed row touched: %q", v)
	}
	agentAt := func(k string) sql.NullInt64 {
		var v sql.NullInt64
		if err := e.st.DB.QueryRow(`SELECT agent_at FROM tagging_state WHERE track_id=?`, id(k)).Scan(&v); err != nil {
			t.Fatal(k, err)
		}
		return v
	}
	for _, k := range []string{"garbled", "gbk", "override"} {
		if agentAt(k).Valid {
			t.Errorf("%s: agent mark not cleared", k)
		}
	}
	for _, k := range []string{"clean", "trashed", "linkless"} {
		if !agentAt(k).Valid {
			t.Errorf("%s: agent mark cleared for an unchanged track", k)
		}
	}
	// Last.fm retries only where a real artist name replaced the stored one.
	lastfmAt := func(k string) sql.NullInt64 {
		var v sql.NullInt64
		if err := e.st.DB.QueryRow(`SELECT lastfm_at FROM tagging_state WHERE track_id=?`, id(k)).Scan(&v); err != nil {
			t.Fatal(k, err)
		}
		return v
	}
	if lastfmAt("gbk").Valid {
		t.Error("gbk: lastfm mark not cleared after the artist was repaired")
	}
	for _, k := range []string{"garbled", "override", "clean", "trashed", "linkless"} {
		if !lastfmAt(k).Valid {
			t.Errorf("%s: lastfm mark cleared", k)
		}
	}
	if tr := get("linkless"); tr.Artist != "陈星" || tr.ArtistID == nil {
		t.Errorf("linkless: artist link not rebuilt: %+v", tr)
	}
	if r, _ := e.st.Search(ctx, 1, "伊能静", 10); len(r.Tracks) != 1 {
		t.Errorf("repaired artist not searchable: %+v", r)
	}
	var garbageArtists int
	if err := e.st.DB.QueryRow(`SELECT COUNT(*) FROM artists WHERE name LIKE '%�%' OR name='???'`).Scan(&garbageArtists); err != nil {
		t.Fatal(err)
	}
	if garbageArtists != 0 {
		t.Errorf("%d garbled artists left behind", garbageArtists)
	}
	var mojibakeArtists int
	if err := e.st.DB.QueryRow(`SELECT COUNT(*) FROM artists WHERE name IN ('ÄÇÓ¢','���ܾ�')`).Scan(&mojibakeArtists); err != nil {
		t.Fatal(err)
	}
	if mojibakeArtists != 0 {
		t.Errorf("%d mojibake artists left behind (still linked from albums)", mojibakeArtists)
	}
	albumArtist := func(k string) string {
		var name sql.NullString
		if err := e.st.DB.QueryRow(`SELECT ar.name FROM tracks t JOIN albums al ON al.id=t.album_id
			LEFT JOIN artists ar ON ar.id=al.artist_id WHERE t.id=?`, id(k)).Scan(&name); err != nil {
			t.Fatal(k, err)
		}
		return name.String
	}
	if a := albumArtist("garbled"); a != "伊能静" {
		t.Errorf("garbled: album artist %q, want 伊能静", a)
	}
	if a := albumArtist("gbk"); a != "那英" {
		t.Errorf("gbk: album artist %q, want 那英", a)
	}
	rowCount := func(table, k string) int {
		var n int
		if err := e.st.DB.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE track_id=?`, id(k)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, c := range []struct {
		table, k string
		want     int
	}{
		{"lyrics_lookup", "gbk", 0}, {"artwork_lookup", "gbk", 0}, // stale misses
		{"lyrics_lookup", "garbled", 1}, {"artwork_lookup", "garbled", 1}, // admin pick, a hit
		{"lyrics_lookup", "clean", 1}, {"artwork_lookup", "clean", 1}, // not repaired
	} {
		if got := rowCount(c.table, c.k); got != c.want {
			t.Errorf("%s rows for %s: %d, want %d", c.table, c.k, got, c.want)
		}
	}

	// Same key: a second run does nothing, even if garbage reappears.
	setTags("clean", "���", "邓丽君", "")
	if n, err := e.st.RepairStoredTags(ctx); err != nil || n != 0 {
		t.Fatalf("second run: n=%d err=%v, want 0", n, err)
	}
	if v := e.col(t, rels["clean"], "tag_title"); v != "���" {
		t.Errorf("second run touched a row: %q", v)
	}
}

// A run cut short (SIGTERM, "database is locked" against the startup scan)
// writes no tagFixKey, and the next start must find and finish every track
// it left half done: tags already cleaned but still linked to the garbled
// artist.
func TestRepairStoredTagsInterruptedRunSelfHeals(t *testing.T) {
	e := newEnv(t, fakeProber{})
	ctx := context.Background()
	rels := []string{"甲/x.mp3", "乙/y.mp3"}
	for _, rel := range rels {
		writeRandom(t, filepath.Join(e.root, filepath.FromSlash(rel)))
	}
	e.scan(t)
	ids := make([]int64, len(rels))
	for i, rel := range rels {
		if err := e.st.DB.QueryRow(`SELECT id FROM tracks WHERE rel_path=?`, rel).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := e.st.DB.Exec(`UPDATE tracks SET tag_title='ÌØ±ðµÄ°®', tag_artist='ÄÇÓ¢' WHERE id=?`, ids[i]); err != nil {
			t.Fatal(err)
		}
		if err := e.st.Reindex(ctx, ids[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := e.st.DB.Exec(`UPDATE albums SET artist_id=(SELECT artist_id FROM tracks WHERE id=?)
			WHERE id=(SELECT album_id FROM tracks WHERE id=?)`, ids[i], ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	// The first track's reindex (runs go in id order) fails after its tags
	// were repaired; the second is never reached.
	if _, err := e.st.DB.Exec(fmt.Sprintf(`CREATE TRIGGER fail_reindex BEFORE UPDATE OF artist_id ON tracks WHEN NEW.id=%d
		BEGIN SELECT RAISE(ABORT, 'database is locked'); END`, min(ids[0], ids[1]))); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.RepairStoredTags(ctx); err == nil {
		t.Fatal("first run: want the reindex error")
	}
	if _, err := e.st.DB.Exec(`DROP TRIGGER fail_reindex`); err != nil {
		t.Fatal(err)
	}
	if n, err := e.st.RepairStoredTags(ctx); err != nil || n != 2 {
		t.Fatalf("second run: n=%d err=%v, want both tracks (the stranded one and the untouched one)", n, err)
	}
	for _, id := range ids {
		tr, err := e.st.Track(ctx, 1, id)
		if err != nil {
			t.Fatal(err)
		}
		var albumArtist sql.NullString
		if err := e.st.DB.QueryRow(`SELECT ar.name FROM tracks t JOIN albums al ON al.id=t.album_id
			LEFT JOIN artists ar ON ar.id=al.artist_id WHERE t.id=?`, id).Scan(&albumArtist); err != nil {
			t.Fatal(err)
		}
		if tr.Artist != "那英" || albumArtist.String != "那英" {
			t.Errorf("track %d: artist %q, album artist %q", id, tr.Artist, albumArtist.String)
		}
	}
	var left int
	if err := e.st.DB.QueryRow(`SELECT COUNT(*) FROM artists WHERE name='ÄÇÓ¢'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("garbled artist left: %d %v", left, err)
	}
	if r, _ := e.st.Search(ctx, 1, "那英", 10); len(r.Tracks) != 2 {
		t.Fatalf("search: %+v", r)
	}
}
