package library

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestTracksFiltersAndVisibility(t *testing.T) {
	e := newEnv(t, fakeProber{})
	for _, p := range []string{"A/1.mp3", "A/2.mp3", "B/3.mp3", "C/4.mp3"} {
		writeRandom(t, filepath.Join(e.root, p))
	}
	e.scan(t)
	ctx := context.Background()
	db := e.st.DB
	db.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'u','h','member',0)`)
	id := func(rel string) int64 {
		var i int64
		db.QueryRow(`SELECT id FROM tracks WHERE rel_path=?`, rel).Scan(&i)
		return i
	}
	db.Exec(`INSERT INTO favorites VALUES (1,?,0)`, id("A/1.mp3"))
	db.Exec(`INSERT INTO dislikes VALUES (1,?,0)`, id("A/2.mp3"))
	db.Exec(`UPDATE tracks SET status='trashed' WHERE rel_path='B/3.mp3'`)
	db.Exec(`UPDATE tracks SET missing_since=1700000000 WHERE rel_path='C/4.mp3'`)

	p, err := e.st.Tracks(ctx, 1, TrackFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].Path != "A/1.mp3" || !p.Items[0].Favorite || p.Items[0].Title != "1" {
		t.Fatalf("visible=%+v", p.Items)
	}
	p, _ = e.st.Tracks(ctx, 1, TrackFilter{DislikedOnly: true})
	if len(p.Items) != 1 || !p.Items[0].Disliked {
		t.Fatalf("disliked=%+v", p.Items)
	}
	p, _ = e.st.Tracks(ctx, 2, TrackFilter{}) // another user: their own (empty) dislikes
	if len(p.Items) != 2 {
		t.Fatalf("other user sees %d", len(p.Items))
	}
	if _, err := e.st.Track(ctx, 1, id("B/3.mp3")); err != ErrNotFound {
		t.Fatalf("trashed track err=%v", err)
	}
	if _, err := e.st.Track(ctx, 1, id("C/4.mp3")); err != ErrNotFound {
		t.Fatalf("missing track err=%v", err)
	}
	ts, _ := e.st.TracksByIDs(ctx, 2, []int64{id("A/2.mp3"), id("B/3.mp3"), id("A/1.mp3")})
	if len(ts) != 2 || ts[0].Path != "A/2.mp3" || ts[1].Path != "A/1.mp3" {
		t.Fatalf("byIDs=%+v", ts)
	}
}

func TestPaginationCursor(t *testing.T) {
	e := newEnv(t, fakeProber{})
	for i := 0; i < 5; i++ {
		writeRandom(t, filepath.Join(e.root, string(rune('a'+i))+".mp3"))
	}
	e.scan(t)
	seen := map[int64]bool{}
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		p, err := e.st.Tracks(context.Background(), 1, TrackFilter{Limit: 2, Cursor: cursor, Sort: "title"})
		if err != nil {
			t.Fatal(err)
		}
		for _, tr := range p.Items {
			seen[tr.ID] = true
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if len(seen) != 5 {
		t.Fatalf("paged through %d of 5", len(seen))
	}
}

func TestAlbumsArtistsSearch(t *testing.T) {
	e := newEnv(t, fakeProber{})
	writeRandom(t, filepath.Join(e.root, "邓丽君精选", "01 - 甜蜜蜜.mp3"))
	writeRandom(t, filepath.Join(e.root, "邓丽君精选", "02 - 月亮代表我的心.mp3"))
	e.scan(t)
	ctx := context.Background()
	e.st.DB.Exec(`UPDATE tracks SET tag_artist='邓丽君'`)
	var ids []int64
	rows, _ := e.st.DB.Query(`SELECT id FROM tracks`)
	for rows.Next() {
		var i int64
		rows.Scan(&i)
		ids = append(ids, i)
	}
	rows.Close()
	for _, i := range ids {
		e.st.Reindex(ctx, i)
	}

	al, _ := e.st.Albums(ctx, 0, 50, "")
	if len(al.Items) != 1 || al.Items[0].Name != "邓丽君精选" || al.Items[0].TrackCount != 2 || al.Items[0].Artist != "邓丽君" {
		t.Fatalf("albums=%+v", al.Items)
	}
	_, tracks, _ := e.st.Album(ctx, 1, al.Items[0].ID)
	if len(tracks) != 2 || tracks[0].Title != "甜蜜蜜" {
		t.Fatalf("album tracks=%+v", tracks)
	}
	ar, _ := e.st.Artists(ctx, 50, "")
	if len(ar.Items) != 1 || ar.Items[0].TrackCount != 2 {
		t.Fatalf("artists=%+v", ar.Items)
	}
	r, err := e.st.Search(ctx, 1, "dlj", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tracks) != 2 || len(r.Albums) != 1 || len(r.Artists) != 1 {
		t.Fatalf("search=%+v", r)
	}
	r, _ = e.st.Search(ctx, 1, "月亮", 20)
	if len(r.Tracks) != 1 {
		t.Fatalf("search 月亮=%+v", r.Tracks)
	}
}

func TestRandomTracksRespectsVisibilityDislikesAndExclude(t *testing.T) {
	e := newEnv(t, fakeProber{})
	for i := 1; i <= 8; i++ {
		writeRandom(t, filepath.Join(e.root, fmt.Sprintf("%d.mp3", i)))
	}
	e.scan(t)
	ctx := context.Background()
	d := e.st.DB
	d.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'u','h','member',0)`)
	id := func(rel string) int64 {
		var i int64
		d.QueryRow(`SELECT id FROM tracks WHERE rel_path=?`, rel).Scan(&i)
		return i
	}
	d.Exec(`UPDATE tracks SET status='trashed' WHERE rel_path='1.mp3'`)
	d.Exec(`UPDATE tracks SET broken=1 WHERE rel_path='2.mp3'`)
	d.Exec(`UPDATE tracks SET missing_since=1 WHERE rel_path='3.mp3'`)
	d.Exec(`INSERT INTO dislikes VALUES (1,?,0)`, id("4.mp3"))
	got, err := e.st.RandomTracks(ctx, 1, 50, []int64{id("5.mp3")})
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, tr := range got {
		paths[tr.Path] = true
	}
	if len(got) != 3 || !paths["6.mp3"] || !paths["7.mp3"] || !paths["8.mp3"] {
		t.Fatalf("got %v", paths)
	}
	few, _ := e.st.RandomTracks(ctx, 1, 2, nil)
	if len(few) != 2 {
		t.Fatalf("n not honoured: %d", len(few))
	}
}

func TestBrokenFilter(t *testing.T) {
	e := newEnv(t, fakeProber{broken: map[string]bool{"bad.wma": true}})
	writeRandom(t, filepath.Join(e.root, "bad.wma"))
	writeRandom(t, filepath.Join(e.root, "ok.mp3"))
	e.scan(t)
	p, _ := e.st.Tracks(context.Background(), 1, TrackFilter{Broken: true})
	if len(p.Items) != 1 || p.Items[0].Path != "bad.wma" {
		t.Fatalf("%+v", p.Items)
	}
	if p.Items[0].BrokenReason != "corrupt" {
		t.Fatalf("broken_reason=%q, want %q", p.Items[0].BrokenReason, "corrupt")
	}
}

func TestRandomFavoritesExcludeReshuffleAndFallback(t *testing.T) {
	e := newEnv(t, fakeProber{})
	for i := 1; i <= 4; i++ {
		writeRandom(t, filepath.Join(e.root, fmt.Sprintf("%d.mp3", i)))
	}
	e.scan(t)
	ctx := context.Background()
	d := e.st.DB
	d.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'u','h','member',0),(2,'v','h','member',0)`)
	id := func(rel string) int64 {
		var i int64
		d.QueryRow(`SELECT id FROM tracks WHERE rel_path=?`, rel).Scan(&i)
		return i
	}
	f1, f2 := id("1.mp3"), id("2.mp3")
	d.Exec(`INSERT INTO favorites VALUES (1,?,0),(1,?,0)`, f1, f2)
	ids := func(ts []Track) map[int64]bool {
		m := map[int64]bool{}
		for _, t := range ts {
			m[t.ID] = true
		}
		return m
	}

	ts, src, err := e.st.RandomFavorites(ctx, 1, 10, nil)
	if err != nil || src != "favorites" || len(ts) != 2 || !ids(ts)[f1] || !ids(ts)[f2] {
		t.Fatalf("all favorites: %v %s %v", ids(ts), src, err)
	}
	ts, src, _ = e.st.RandomFavorites(ctx, 1, 10, []int64{f1})
	if src != "favorites" || len(ts) != 1 || ts[0].ID != f2 {
		t.Fatalf("exclude played: %v", ids(ts))
	}
	ts, src, _ = e.st.RandomFavorites(ctx, 1, 10, []int64{f1, f2})
	if src != "favorites" || len(ts) != 2 {
		t.Fatalf("all heard → reshuffle favorites: %v %s", ids(ts), src)
	}
	d.Exec(`UPDATE tracks SET status='trashed' WHERE id=?`, f2)
	if ts, _, _ = e.st.RandomFavorites(ctx, 1, 10, nil); len(ts) != 1 || ts[0].ID != f1 {
		t.Fatalf("trashed favorite must drop out: %v", ids(ts))
	}
	ts, src, _ = e.st.RandomFavorites(ctx, 2, 10, nil)
	if src != "all" || len(ts) != 3 {
		t.Fatalf("no favorites → global shuffle of visible tracks: %v %s", ids(ts), src)
	}
	d.Exec(`INSERT INTO tags(id,name,kind) VALUES (1,'chill','mood')`)
	d.Exec(`INSERT INTO track_tags(track_id,tag_id,source) VALUES (?,1,'manual')`, id("3.mp3"))
	if ts, _ := e.st.Random(ctx, 2, RandomOpts{Tag: "chill"}); len(ts) != 1 || ts[0].Path != "3.mp3" {
		t.Fatalf("tag filter: %v", ids(ts))
	}
}

// sort=favorited lists a user's favorites newest-favorited first (the offline
// cache downloads them in that order), whatever order they were added in.
func TestFavoritesSortedByWhenFavorited(t *testing.T) {
	e := newEnv(t, fakeProber{})
	for i := 1; i <= 3; i++ {
		writeRandom(t, filepath.Join(e.root, fmt.Sprintf("%d.mp3", i)))
	}
	e.scan(t)
	d := e.st.DB
	d.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'u','h','member',0),(2,'v','h','member',0)`)
	id := func(rel string) int64 {
		var i int64
		d.QueryRow(`SELECT id FROM tracks WHERE rel_path=?`, rel).Scan(&i)
		return i
	}
	d.Exec(`INSERT INTO favorites VALUES (1,?,300),(1,?,100),(1,?,200),(2,?,999)`, id("1.mp3"), id("2.mp3"), id("3.mp3"), id("2.mp3"))
	p, err := e.st.Tracks(context.Background(), 1, TrackFilter{FavoritesOnly: true, Sort: "favorited"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tr := range p.Items {
		got = append(got, tr.Path)
	}
	if fmt.Sprint(got) != "[1.mp3 3.mp3 2.mp3]" {
		t.Fatalf("order %v", got)
	}
}
