package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronsuns/lark-server/internal/testutil"
)

func TestProbeFormats(t *testing.T) {
	dir := t.TempDir()
	p := FFprobe{Path: "ffprobe"}
	cases := []struct {
		name     string
		args     []string
		codec    string
		lossless bool
	}{
		{"a.mp3", []string{"-b:a", "192k", "-metadata", "title=Tian Mi Mi", "-metadata", "artist=邓丽君", "-metadata", "track=3/12"}, "mp3", false},
		{"b.flac", nil, "flac", true},
		{"c.wav", nil, "pcm_s16le", true},
		{"d.wma", []string{"-c:a", "wmav2"}, "wmav2", false},
		{"e.m4a", []string{"-c:a", "aac", "-b:a", "128k"}, "aac", false},
	}
	for _, c := range cases {
		path := testutil.Sample(t, dir, c.name, c.args...)
		info, err := p.Probe(context.Background(), path)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if info.Codec != c.codec || info.Lossless != c.lossless {
			t.Errorf("%s: codec=%q lossless=%v", c.name, info.Codec, info.Lossless)
		}
		if info.DurationMS < 1900 || info.DurationMS > 2200 {
			t.Errorf("%s: duration=%d", c.name, info.DurationMS)
		}
	}
	info, _ := p.Probe(context.Background(), filepath.Join(dir, "a.mp3"))
	if info.Title != "Tian Mi Mi" || info.Artist != "邓丽君" || info.TrackNo != 3 {
		t.Errorf("tags=%+v", info)
	}
	if info.BitrateKbps < 180 || info.BitrateKbps > 200 {
		t.Errorf("bitrate=%d", info.BitrateKbps)
	}
}

// A probe failure's error must carry ffprobe's own
// stderr (e.g. "Invalid data found when processing input"), not just the
// bare exit status, so broken_reason is actually useful for diagnosing why
// a file was marked broken.
func TestProbeBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.mp3")
	os.WriteFile(path, []byte("not audio at all"), 0o644)
	_, err := (FFprobe{Path: "ffprobe"}).Probe(context.Background(), path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "exit status") {
		t.Fatalf("expected the underlying exit status still present, got %q", err)
	}
	if len(err.Error()) <= len("ffprobe: exit status 1") {
		t.Fatalf("expected ffprobe's stderr folded into the error, got only %q", err)
	}
}

func TestFingerprintStableAcrossRename(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.bin")
	data := make([]byte, 300_000)
	for i := range data {
		data[i] = byte(i % 251)
	}
	os.WriteFile(a, data, 0o644)
	f1, err := Fingerprint(a, int64(len(data)))
	if err != nil || len(f1) != 32 {
		t.Fatalf("fp=%q err=%v", f1, err)
	}
	b := filepath.Join(dir, "moved.bin")
	os.Rename(a, b)
	f2, _ := Fingerprint(b, int64(len(data)))
	if f1 != f2 {
		t.Fatal("fingerprint changed on rename")
	}
	data[len(data)-1] ^= 0xff
	os.WriteFile(b, data, 0o644)
	f3, _ := Fingerprint(b, int64(len(data)))
	if f3 == f1 {
		t.Fatal("fingerprint ignored tail change")
	}
}

// TestFingerprintDetectsTailChangeUnder128KiB guards against the off-by-one
// gate size > 2*fpChunk, which used to skip the tail read entirely for files
// between 64 KiB and 128 KiB (only the first chunk was ever hashed).
func TestFingerprintDetectsTailChangeUnder128KiB(t *testing.T) {
	dir := t.TempDir()
	const size = 100_000 // between fpChunk (64 KiB) and 2*fpChunk (128 KiB)
	data1 := make([]byte, size)
	for i := range data1 {
		data1[i] = byte(i % 251)
	}
	data2 := append([]byte(nil), data1...)
	data2[len(data2)-1] ^= 0xff // differs only near the very end, well past the first 64 KiB

	a := filepath.Join(dir, "a.bin")
	b := filepath.Join(dir, "b.bin")
	if err := os.WriteFile(a, data1, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, data2, 0o644); err != nil {
		t.Fatal(err)
	}

	f1, err := Fingerprint(a, size)
	if err != nil {
		t.Fatal(err)
	}
	f2, err := Fingerprint(b, size)
	if err != nil {
		t.Fatal(err)
	}
	if f1 == f2 {
		t.Fatal("fingerprint identical for files with identical first 64 KiB but different tails")
	}
}

func TestTagsReadsLyrics(t *testing.T) {
	path := testutil.Sample(t, t.TempDir(), "l.mp3", "-metadata", "lyrics=hello")
	tags, err := FFprobe{Path: "ffprobe"}.Tags(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for k, v := range tags {
		if strings.HasPrefix(k, "lyrics") && v == "hello" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tags %v", tags)
	}
}

func TestAttachedPicture(t *testing.T) {
	dir := t.TempDir()
	plain := testutil.Sample(t, dir, "plain.mp3")
	pic := filepath.Join(dir, "pic.png")
	if out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=red:s=64x64", "-frames:v", "1", pic).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	with := filepath.Join(dir, "with.mp3")
	if out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y", "-i", plain, "-i", pic, "-map", "0:a", "-map", "1:v",
		"-c:a", "copy", "-c:v", "mjpeg", "-disposition:v:0", "attached_pic", "-id3v2_version", "3", with).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	p := FFprobe{Path: "ffprobe"}
	if idx, ok, err := p.AttachedPicture(context.Background(), with); err != nil || !ok || idx != 1 {
		t.Fatalf("with: %d %v %v", idx, ok, err)
	}
	if _, ok, err := p.AttachedPicture(context.Background(), plain); err != nil || ok {
		t.Fatalf("plain: %v %v", ok, err)
	}
}
