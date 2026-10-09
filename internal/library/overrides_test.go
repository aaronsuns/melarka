package library

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOverridesChangeDisplayAndSearch(t *testing.T) {
	e := newEnv(t, fakeProber{})
	writeRandom(t, filepath.Join(e.root, "misc", "track01.mp3"))
	e.scan(t)
	ctx := context.Background()
	var id int64
	e.st.DB.QueryRow(`SELECT id FROM tracks`).Scan(&id)
	title, artist := "甜蜜蜜", "邓丽君"
	if err := e.st.SetOverrides(ctx, id, Overrides{Title: &title, Artist: &artist}); err != nil {
		t.Fatal(err)
	}
	tr, _ := e.st.Track(ctx, 1, id)
	if tr.Title != "甜蜜蜜" || tr.Artist != "邓丽君" || tr.ArtistID == nil {
		t.Fatalf("%+v", tr)
	}
	if r, _ := e.st.Search(ctx, 1, "tmm", 10); len(r.Tracks) != 1 {
		t.Fatal("override not searchable")
	}
	empty := ""
	e.st.SetOverrides(ctx, id, Overrides{Title: &empty})
	tr, _ = e.st.Track(ctx, 1, id)
	if tr.Title != "track01" || tr.Artist != "邓丽君" {
		t.Fatalf("clear title: %+v", tr)
	}
	if err := e.st.SetStatus(ctx, id, "trashed"); err == nil {
		t.Fatal("status trashed must go through trash service")
	}
}

func TestSetOverridesRefusesTrashedTrack(t *testing.T) {
	e := newEnv(t, fakeProber{})
	writeRandom(t, filepath.Join(e.root, "misc", "track02.mp3"))
	e.scan(t)
	ctx := context.Background()
	var id int64
	e.st.DB.QueryRow(`SELECT id FROM tracks`).Scan(&id)
	if _, err := e.st.DB.Exec(`UPDATE tracks SET status='trashed' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	title := "should not apply"
	if err := e.st.SetOverrides(ctx, id, Overrides{Title: &title}); err != ErrNotFound {
		t.Fatalf("SetOverrides on trashed track: got %v, want ErrNotFound", err)
	}
}
