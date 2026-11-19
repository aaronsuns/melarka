package recommend

import (
	"testing"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// Recommendations are built from YouTube Mixes: a Bilibili id never qualifies.
func TestPlayableIgnoresBilibiliID(t *testing.T) {
	if playable(ytdlp.Video{ID: "BV1aaaaaaaa1", Title: "t", DurationS: 200}) {
		t.Fatal("a Bilibili video must not be recommended")
	}
	if !playable(ytdlp.Video{ID: "dQw4w9WgXcQ", Title: "t", DurationS: 200}) {
		t.Fatal("a YouTube video is")
	}
}
