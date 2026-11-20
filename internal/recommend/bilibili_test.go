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

// A track downloaded from Bilibili seeds its Mix from a YouTube search, not
// from its BV id (which has no Mix).
func TestSeedVideoSkipsBilibiliDownload(t *testing.T) {
	e := newEnv(t)
	u := e.user("alice")
	tr := e.track("晴天", "周杰伦", 269)
	e.download(u, "BV1aaaaaaaa1", "done", tr)
	id, err := e.svc.seedVideo(context.Background(), u, Seed{TrackID: tr, Title: "晴天", Artist: "周杰伦"})
	if err != nil || id == "BV1aaaaaaaa1" {
		t.Fatalf("seed %q, %v: a Bilibili id must not seed a Mix", id, err)
	}
}
