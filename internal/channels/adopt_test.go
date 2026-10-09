package channels

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func TestAdoptEpisode(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("anna")
	tmp := t.TempDir()
	src, thumb := filepath.Join(tmp, vid(7)+".audio.m4a"), filepath.Join(tmp, vid(7)+".audio.jpg")
	os.WriteFile(src, []byte("0123456789"), 0o644)
	os.WriteFile(thumb, []byte("jpg"), 0o644)
	in := Adopt{VideoID: vid(7), Title: "Ep 7", ChannelID: chX, ChannelTitle: "X", DurationS: 600, Kind: ytdlp.MediaAudio, Src: src, Thumb: thumb}
	if err := e.svc.AdoptEpisode(ctx, u, in); err != nil {
		t.Fatal(err)
	}
	if !exists(e.path(chX, vid(7)+".m4a")) || !exists(e.path(chX, vid(7)+".jpg")) || exists(src) {
		t.Fatal("moved into the channels layout")
	}
	ep, err := e.svc.Episode(ctx, u, vid(7))
	if err != nil || !ep.Kept || ep.Audio == nil || ep.Audio.Status != "done" || ep.Audio.Bytes != 10 || ep.ChannelTitle != "X" || ep.Kind != "video" {
		t.Fatalf("%+v %v", ep, err)
	}
	// Again (someone else keeps it too): the copy already there wins, theirs is deleted.
	os.WriteFile(src, []byte("x"), 0o644)
	b := e.user("bo")
	if err := e.svc.AdoptEpisode(ctx, b, in); err != nil {
		t.Fatal(err)
	}
	if exists(src) || e.count(`SELECT bytes FROM episode_files WHERE video_id=?`, vid(7)) != 10 || e.count(`SELECT COUNT(*) FROM episode_keeps`) != 2 {
		t.Fatal("existing file kept, both users keep it")
	}
	if err := e.svc.AdoptEpisode(ctx, u, Adopt{VideoID: vid(8), ChannelID: "nope", Kind: ytdlp.MediaAudio, Src: src}); err != ErrBadID {
		t.Fatal(err)
	}
}

// 保留 at 360p, then again once 高清 is done (this visit or a later one): the
// episode becomes the 720p, not "kept" while the HD file is thrown away. A
// smaller file never replaces a bigger one.
func TestAdoptEpisodeUpgradesAKeptVideo(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("anna")
	tmp := t.TempDir()
	keep := func(name, body string) {
		src := filepath.Join(tmp, name)
		os.WriteFile(src, []byte(body), 0o644)
		in := Adopt{VideoID: vid(7), Title: "Ep 7", ChannelID: chX, ChannelTitle: "X", DurationS: 600, Kind: ytdlp.MediaVideo, Src: src}
		if err := e.svc.AdoptEpisode(ctx, u, in); err != nil {
			t.Fatal(err)
		}
		if exists(src) {
			t.Fatalf("%s left behind", name)
		}
	}
	keep("7.video.mp4", "360p")
	keep("7.hd.mp4", "the 720p file")
	dst := e.path(chX, vid(7)+".v.mp4")
	if b, _ := os.ReadFile(dst); string(b) != "the 720p file" || e.count(`SELECT bytes FROM episode_files WHERE video_id=? AND kind='video'`, vid(7)) != 13 {
		t.Fatalf("not upgraded: %q", b)
	}
	keep("7.video.mp4", "360p")
	if b, _ := os.ReadFile(dst); string(b) != "the 720p file" {
		t.Fatalf("downgraded: %q", b)
	}
}
