package download

import (
	"context"
	"database/sql"
	"testing"

	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// Downloads tagged before CleanArtist existed get the cleaned artist once —
// unless an admin edited the name since, or the track is in the trash — and
// their lyrics/cover misses are forgotten so they are looked up again.
func TestFixArtistNames(t *testing.T) {
	e := newEnv(t, true)
	const tianbian = "布仁巴雅尔原唱的歌曲《天边》，深沉悠远，具有浓浓的草原风味！"
	e.fake.entries = []entry{
		{"a1_0000000x", tianbian, "草原音乐"},        // untouched since download → fixed
		{"a2_0000000x", "周深翻唱《大鱼》", "Chan"},      // an admin renamed the artist → kept
		{"a3_0000000x", "邓丽君演唱《甜蜜蜜》", "Chan"},    // trashed → kept
		{"a4_0000000x", "邓丽君 - 甜蜜蜜【MV】", "Chan"}, // nothing to clean → untouched
	}
	ctx := context.Background()
	e.start(t)
	jobs, err := e.svc.Enqueue(ctx, e.admin, watchURL("a1_0000000x")) // the fake answers with all four
	if err != nil || len(jobs) != len(e.fake.entries) {
		t.Fatalf("enqueue: %d jobs, %v", len(jobs), err)
	}
	tracks := map[string]int64{}
	for _, j := range jobs {
		tracks[j.VideoID] = *e.waitStatus(t, j.ID, StatusDone).TrackID
	}
	e.stop(t)()
	d := e.svc.DB

	// The state a download from before the fix left behind: the V1 names as
	// overrides, and lookups that missed with them.
	for _, x := range e.fake.entries {
		title, artist := ytdlp.CleanTitleV1(x.Title, x.Channel)
		id := tracks[x.ID]
		if err := e.store.SetOverrides(ctx, id, library.Overrides{Title: &title, Artist: &artist}); err != nil {
			t.Fatal(err)
		}
	}
	edited := "周深 (live)"
	if err := e.store.SetOverrides(ctx, tracks["a2_0000000x"], library.Overrides{Artist: &edited}); err != nil {
		t.Fatal(err)
	}
	// (after the renames above: a rename itself forgets a track's misses)
	for _, x := range e.fake.entries {
		id := tracks[x.ID]
		mustExec(t, e, `INSERT OR REPLACE INTO lyrics_lookup(track_id, attempted_at, found, manual) VALUES (?,1,0,0)`, id)
		mustExec(t, e, `INSERT OR REPLACE INTO artwork_lookup(track_id, attempted_at, found) VALUES (?,1,0)`, id)
	}
	mustExec(t, e, `UPDATE tracks SET status='trashed' WHERE id=?`, tracks["a3_0000000x"])
	if tr, err := e.store.Track(ctx, e.admin, tracks["a1_0000000x"]); err != nil || tr.Artist != "布仁巴雅尔原唱的歌曲" {
		t.Fatalf("setup: %v %+v %v", tracks, tr, err)
	}

	n, err := e.svc.FixArtistNames(ctx)
	if err != nil || n != 1 {
		t.Fatalf("FixArtistNames = %d, %v; want 1 track", n, err)
	}
	tr, err := e.store.Track(ctx, e.admin, tracks["a1_0000000x"])
	if err != nil || tr.Artist != "布仁巴雅尔" || tr.Title != "天边" {
		t.Fatalf("fixed track: %+v %v", tr, err)
	}
	if got := overrideArtist(t, d, tracks["a1_0000000x"]); got != "布仁巴雅尔" {
		t.Errorf("override artist = %q", got)
	}
	if got := countRows(t, d, `SELECT COUNT(*) FROM lyrics_lookup WHERE track_id=?`, tracks["a1_0000000x"]); got != 0 {
		t.Errorf("lyrics miss kept for the fixed track")
	}
	if got := countRows(t, d, `SELECT COUNT(*) FROM artwork_lookup WHERE track_id=?`, tracks["a1_0000000x"]); got != 0 {
		t.Errorf("artwork miss kept for the fixed track")
	}
	// The old artist is gone from the library once nothing links it.
	if got := countRows(t, d, `SELECT COUNT(*) FROM artists WHERE name='布仁巴雅尔原唱的歌曲'`); got != 0 {
		t.Errorf("orphaned old artist left behind")
	}
	if got := overrideArtist(t, d, tracks["a2_0000000x"]); got != edited {
		t.Errorf("edited artist changed to %q", got)
	}
	if got := overrideArtist(t, d, tracks["a3_0000000x"]); got != "邓丽君演唱" {
		t.Errorf("trashed track's artist changed to %q", got)
	}
	for _, id := range []string{"a2_0000000x", "a3_0000000x", "a4_0000000x"} {
		if got := countRows(t, d, `SELECT COUNT(*) FROM lyrics_lookup WHERE track_id=? AND found=0`, tracks[id]); got != 1 {
			t.Errorf("%s: lyrics miss forgotten though nothing changed", id)
		}
	}

	// Once only: a later run changes nothing, even if a V1 name is back.
	old := "布仁巴雅尔原唱的歌曲"
	if err := e.store.SetOverrides(ctx, tracks["a1_0000000x"], library.Overrides{Artist: &old}); err != nil {
		t.Fatal(err)
	}
	if n, err := e.svc.FixArtistNames(ctx); err != nil || n != 0 {
		t.Fatalf("second run = %d, %v; want 0", n, err)
	}
	if got := overrideArtist(t, d, tracks["a1_0000000x"]); got != old {
		t.Errorf("second run rewrote the artist to %q", got)
	}
}

// An admin's lyrics pick survives the fix; only misses are forgotten.
func TestFixArtistNamesKeepsManualLyrics(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"b1_0000000x", "布仁巴雅尔原唱的歌曲《天边》", "Chan"}}
	ctx := context.Background()
	e.start(t)
	jobs, err := e.svc.Enqueue(ctx, e.admin, watchURL("b1_0000000x"))
	if err != nil {
		t.Fatal(err)
	}
	id := *e.waitStatus(t, jobs[0].ID, StatusDone).TrackID
	e.stop(t)()
	old := "布仁巴雅尔原唱的歌曲"
	if err := e.store.SetOverrides(ctx, id, library.Overrides{Artist: &old}); err != nil {
		t.Fatal(err)
	}
	mustExec(t, e, `INSERT OR REPLACE INTO lyrics_lookup(track_id, attempted_at, found, manual) VALUES (?,1,0,1)`, id)
	if n, err := e.svc.FixArtistNames(ctx); err != nil || n != 1 {
		t.Fatalf("FixArtistNames = %d, %v", n, err)
	}
	if got := countRows(t, e.svc.DB, `SELECT COUNT(*) FROM lyrics_lookup WHERE track_id=? AND manual=1`, id); got != 1 {
		t.Errorf("manual lyrics pick forgotten")
	}
}

func countRows(t *testing.T, d *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := d.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func overrideArtist(t *testing.T, d *sql.DB, id int64) string {
	t.Helper()
	var a sql.NullString
	if err := d.QueryRow(`SELECT artist FROM track_overrides WHERE track_id=?`, id).Scan(&a); err != nil {
		t.Fatal(err)
	}
	return a.String
}
