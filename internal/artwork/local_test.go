package artwork

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aaronsuns/lark-server/internal/lyrics"
)

func TestBuild(t *testing.T) {
	ps, err := Build([]string{"embedded", "folder"}, Deps{})
	if err != nil || len(ps) != 2 || ps[0].Name() != "embedded" || ps[1].Name() != "folder" {
		t.Fatalf("%v %v", ps, err)
	}
	for _, p := range ps {
		if !isLocal(p) {
			t.Fatalf("%s is not local", p.Name())
		}
	}
	if _, err := Build([]string{"spotify"}, Deps{}); err == nil {
		t.Fatal("unknown provider accepted")
	}
	if _, err := Build([]string{"folder", "folder"}, Deps{}); err == nil {
		t.Fatal("duplicate provider accepted")
	}
}

func TestFolderWithoutPathOrDirectoryIsNothing(t *testing.T) {
	ctx := context.Background()
	if fs, err := (Folder{}).Find(ctx, lyrics.Query{}); fs != nil || err != nil {
		t.Fatalf("no path: %v %v", fs, err)
	}
	q := lyrics.Query{Path: filepath.Join(t.TempDir(), "gone", "x.mp3")}
	if fs, err := (Folder{}).Find(ctx, q); fs != nil || err != nil {
		t.Fatalf("missing dir: %v %v", fs, err)
	}
	if fs, err := (&Embedded{}).Find(ctx, q); fs != nil || err != nil {
		t.Fatalf("embedded without a prober: %v %v", fs, err)
	}
}

// The NUC shares its CPU with playback: cover work runs niced, like transcodes.
func TestSquareNiced(t *testing.T) {
	if _, err := exec.LookPath("nice"); err != nil {
		t.Skip("nice not installed")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.png")
	image(t, src, "red", 200, 100)
	dst := filepath.Join(dir, "out.jpg")
	if err := (FFmpeg{Path: "ffmpeg", Nice: true}).Square(context.Background(), src, -1, dst, 300); err != nil {
		t.Fatal(err)
	}
	if d := dims(t, dst); d != "100,100" {
		t.Fatalf("dims %s", d)
	}
	if args := (FFmpeg{Path: "/x/ffmpeg", Nice: true}).command(src, -1, dst, 300); args[0] != "nice" || args[3] != "/x/ffmpeg" {
		t.Fatalf("not niced: %v", args)
	}
}
