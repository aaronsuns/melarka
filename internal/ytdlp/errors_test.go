package ytdlp

import "testing"

func TestLastLine(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			"strips wrapper, prefers text after ERROR, drops multi-line WARNING",
			"yt-dlp: exit status 1: WARNING: something\nERROR: [youtube] p2: Video unavailable",
			"[youtube] p2: Video unavailable",
		},
		{
			"single-line ERROR",
			"yt-dlp: exit status 1: ERROR: network unreachable",
			"network unreachable",
		},
		{
			"no ERROR marker falls back to last non-empty line",
			"yt-dlp: exit status 1: some stderr\nmore stderr",
			"more stderr",
		},
		{
			"no wrapper at all",
			"context deadline exceeded",
			"context deadline exceeded",
		},
		{
			"strips a path-shaped token",
			"yt-dlp: exit status 1: ERROR: /data/music/youtube/Chan/Song [id].m4a: no space left",
			"[id].m4a: no space left",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LastLine(c.in); got != c.want {
				t.Errorf("LastLine(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestLastLineCapsAt200Runes(t *testing.T) {
	long := ""
	for range 50 {
		long += "0123456789"
	}
	got := LastLine("yt-dlp: exit status 1: ERROR: " + long)
	if r := []rune(got); len(r) != 200 {
		t.Fatalf("len = %d, want 200", len(r))
	}
}
