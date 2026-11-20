package download

import (
	"context"
	"sync"
	"testing"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// switchSources is a ytdlp.SourceGate a test flips.
type switchSources struct {
	mu sync.Mutex
	on map[ytdlp.Source]bool
}

func (s *switchSources) Enabled(_ context.Context, src ytdlp.Source) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.on[src]
}

func (s *switchSources) set(src ytdlp.Source, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.on[src] = on
}

func TestDownloadWorkerFailsJobWhenSourceOff(t *testing.T) {
	e := newEnv(t, true)
	src := &switchSources{on: map[ytdlp.Source]bool{}}
	e.svc.Sources = src
	ctx := context.Background()
	e.start(t)
	j, err := e.svc.EnqueueVideo(ctx, e.alice, ytdlp.Video{ID: "s1_0000000x", Title: "T", URL: watchURL("s1_0000000x")})
	if err != nil {
		t.Fatal(err)
	}
	e.waitStatus(t, j.ID, StatusFailed)
	if got := e.job(t, j.ID); got.Error != "lark:source_disabled" {
		t.Fatalf("error %q", got.Error)
	}
	if calls := e.fake.calls(); len(calls) != 0 {
		t.Fatalf("yt-dlp ran for a disabled source: %v", calls)
	}
	// Turned back on, a retry downloads it.
	src.set(ytdlp.SourceYouTube, true)
	if err := e.svc.Retry(ctx, e.alice, false, j.ID); err != nil {
		t.Fatal(err)
	}
	e.waitStatus(t, j.ID, StatusDone)
}

func TestMetaBackfillIdlesWhenYouTubeOff(t *testing.T) {
	e := newEnv(t, true)
	e.svc.Sources = &switchSources{on: map[ytdlp.Source]bool{}}
	if n, err := e.svc.BackfillMeta(context.Background(), nil, 0, 5); n != 0 || err != nil {
		t.Fatalf("%d %v", n, err)
	}
	if calls := e.fake.calls(); len(calls) != 0 {
		t.Fatalf("yt-dlp ran: %v", calls)
	}
}
