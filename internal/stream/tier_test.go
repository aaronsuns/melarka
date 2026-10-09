package stream

import (
	"strings"
	"testing"
)

func TestDecideTable(t *testing.T) {
	cases := []struct {
		codec string
		br    int
		tier  Tier
		pass  bool
		out   string
		outBR int
	}{
		{"mp3", 320, Lossless, true, "", 0},
		{"mp3", 320, High, true, "", 0},
		{"mp3", 320, Saver, false, "aac", 128},
		{"mp3", 128, Saver, true, "", 0},
		{"aac", 0, High, true, "", 0},
		{"flac", 900, Lossless, true, "", 0},
		{"flac", 900, High, false, "aac", 256},
		{"alac", 900, Saver, false, "aac", 128},
		{"pcm_s16le", 1411, Lossless, false, "flac", 0},
		{"pcm_s16le", 1411, High, false, "aac", 256},
		{"wmalossless", 900, Lossless, false, "alac", 0},
		{"wmav2", 192, Lossless, false, "aac", 256},
		{"wmav2", 192, Saver, false, "aac", 128},
		{"opus", 160, Lossless, false, "aac", 256},
		{"ape", 800, Lossless, false, "flac", 0},
	}
	for _, c := range cases {
		p := Decide(Source{Codec: c.codec, BitrateKbps: c.br}, c.tier)
		if p.Passthrough != c.pass || p.Codec != c.out || p.BitrateKbps != c.outBR {
			t.Errorf("%s@%d %s → %+v", c.codec, c.br, c.tier, p)
		}
		if !p.Passthrough && p.ContentType == "" {
			t.Errorf("%s: no content type", c.codec)
		}
	}
	if _, ok := ParseTier("ultra"); ok {
		t.Error("bad tier accepted")
	}
	if tier, ok := ParseTier(""); !ok || tier != Lossless {
		t.Error("empty tier must be lossless")
	}
}

func TestFFmpegArgs(t *testing.T) {
	a := FFmpegArgs("in.wav", "out.m4a.tmp", Plan{Codec: "aac", BitrateKbps: 256, Ext: ".m4a"})
	joined := ""
	for _, s := range a {
		joined += s + " "
	}
	for _, want := range []string{"-i in.wav", "-c:a aac", "-b:a 256k", "-movflags +faststart", "-f ipod", "out.m4a.tmp"} {
		if !contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
	f := FFmpegArgs("in.wav", "o.tmp", Plan{Codec: "flac", Ext: ".flac"})
	if !contains(fmtArgs(f), "-c:a flac") || !contains(fmtArgs(f), "-f flac") {
		t.Errorf("flac args %v", f)
	}
}

func fmtArgs(a []string) string   { return strings.Join(a, " ") + " " }
func contains(s, sub string) bool { return strings.Contains(s, sub) }
