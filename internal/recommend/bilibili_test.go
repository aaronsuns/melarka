package recommend

import (
	"context"
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

type offSources struct{}

func (offSources) Enabled(context.Context, ytdlp.Source) bool { return false }

func TestRecommendStepIdlesWhenYouTubeOff(t *testing.T) {
	s := &Service{Sources: offSources{}} // no DB: the step must stop before touching it
	if w, err := s.step(context.Background()); w != sourceOffWait || err != nil {
		t.Fatalf("%s %v", w, err)
	}
}
