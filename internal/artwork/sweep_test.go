package artwork

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// The cache keeps covers of tracks that still exist (trashed ones too, for a restore) and recent
// temp files; covers of tracks gone from the library and temp files left by a crash go.
func TestSweepRemovesOrphansAndOldTempFiles(t *testing.T) {
	e := newArtEnv(t, local()...)
	keep := e.track(t, "a.mp3", "a", "x", "")
	trashed := e.track(t, "b.mp3", "b", "x", "")
	if _, err := e.db.Exec(`UPDATE tracks SET status='trashed', trashed_at=1 WHERE id=?`, trashed); err != nil {
		t.Fatal(err)
	}
	gone := e.track(t, "c.mp3", "c", "x", "")
	if _, err := e.db.Exec(`DELETE FROM tracks WHERE id=?`, gone); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(e.svc.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := e.svc.now()
	put := func(name string, age time.Duration) {
		p := filepath.Join(e.svc.Dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	for _, size := range Sizes {
		put(filepath.Base(e.svc.File(keep, size)), 48*time.Hour)
		put(filepath.Base(e.svc.File(trashed, size)), 48*time.Hour)
		put(filepath.Base(e.svc.File(gone, size)), 48*time.Hour)
	}
	put(".dl-123", 2*time.Hour)
	put(".7-300-99.tmp", 2*time.Hour)
	put(".dl-456", time.Minute) // a fetch in flight
	put("notes.txt", 48*time.Hour)

	n, err := e.svc.Sweep(e.ctx)
	if err != nil || n != 4 {
		t.Fatalf("removed %d, %v", n, err)
	}
	var left []string
	entries, _ := os.ReadDir(e.svc.Dir)
	for _, de := range entries {
		left = append(left, de.Name())
	}
	want := []string{".dl-456", filepath.Base(e.svc.File(keep, 1000)), filepath.Base(e.svc.File(keep, 300)),
		filepath.Base(e.svc.File(trashed, 1000)), filepath.Base(e.svc.File(trashed, 300)), "notes.txt"}
	slices.Sort(left)
	slices.Sort(want)
	if !slices.Equal(left, want) {
		t.Fatalf("left %v, want %v", left, want)
	}
}

func TestSweepWithoutCacheDir(t *testing.T) {
	e := newArtEnv(t)
	if n, err := e.svc.Sweep(e.ctx); n != 0 || err != nil {
		t.Fatalf("%d %v", n, err)
	}
}
