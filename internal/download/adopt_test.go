package download

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func TestAdoptKeptPreview(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	b, _ := os.ReadFile(e.fake.sample)
	src := filepath.Join(t.TempDir(), "kp_00000001.audio.m4a")
	os.WriteFile(src, b, 0o644)
	v := ytdlp.Video{ID: "kp_00000001", Title: "邓丽君 - 甜蜜蜜", Channel: "Teresa Teng", DurationS: 2}
	j, adopted, err := e.svc.Adopt(ctx, e.alice, v, src)
	if err != nil || !adopted || j.Status != StatusDone || j.TrackID == nil {
		t.Fatalf("%+v %v %v", j, adopted, err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("the preview file was moved, not copied")
	}
	if _, err := os.Stat(filepath.Join(e.root, "Teresa Teng", "甜蜜蜜 [kp_00000001].m4a")); err != nil {
		t.Fatalf("not where a download would be: %v", err)
	}
	var fav int
	e.svc.DB.QueryRow(`SELECT COUNT(*) FROM favorites WHERE user_id=? AND track_id=?`, e.alice, *j.TrackID).Scan(&fav)
	if fav != 1 {
		t.Fatal("kept previews are auto-favorited like downloads")
	}
	if len(e.fake.calls()) != 0 {
		t.Fatal("no yt-dlp download")
	}
	// Bob keeps the same video: nothing moves, he gets the existing job (and the favorite).
	src2 := filepath.Join(t.TempDir(), "again.m4a")
	os.WriteFile(src2, b, 0o644)
	j2, adopted, err := e.svc.Adopt(ctx, e.bob, v, src2)
	if err != nil || adopted || j2.ID != j.ID {
		t.Fatalf("%+v %v %v", j2, adopted, err)
	}
	if _, err := os.Stat(src2); err != nil {
		t.Fatal("src stays for the caller to delete")
	}
	e.svc.DB.QueryRow(`SELECT COUNT(*) FROM favorites WHERE user_id=? AND track_id=?`, e.bob, *j.TrackID).Scan(&fav)
	if fav != 1 {
		t.Fatal("bob's favorite")
	}
}

func TestAdoptWithoutTarget(t *testing.T) {
	e := newEnv(t, false)
	if _, _, err := e.svc.Adopt(context.Background(), e.alice, ytdlp.Video{ID: "kp_00000001"}, "/nope"); err != ErrNoTarget {
		t.Fatal(err)
	}
}

// A file the library cannot ingest goes back where it came from: the
// preview keeps its file (and can still be kept into Channels).
func TestAdoptIngestFailureMovesTheFileBack(t *testing.T) {
	e := newEnv(t, true)
	src := filepath.Join(t.TempDir(), "kp_00000002.audio.txt") // not audio: the scan ignores it
	os.WriteFile(src, []byte("not audio"), 0o644)
	if _, _, err := e.svc.Adopt(context.Background(), e.alice, ytdlp.Video{ID: "kp_00000002", Title: "x", Channel: "C"}, src); err == nil {
		t.Fatal("no track, no success")
	}
	if b, err := os.ReadFile(src); err != nil || string(b) != "not audio" {
		t.Fatalf("file moved back: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "C", "x [kp_00000002].txt")); !os.IsNotExist(err) {
		t.Fatal("nothing left in the library")
	}
}
