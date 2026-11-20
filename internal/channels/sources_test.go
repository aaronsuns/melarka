package channels

import (
	"context"
	"testing"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

type offSources struct{}

func (offSources) Enabled(context.Context, ytdlp.Source) bool { return false }

func TestChannelStepsIdleWhenYouTubeOff(t *testing.T) {
	e, _ := seedDiscovery(t)
	e.svc.Sources = offSources{}
	ctx := context.Background()
	if w := e.svc.pollStep(ctx); w != sourceOffWait {
		t.Errorf("poll: %s", w)
	}
	if w := e.svc.refreshStep(ctx); w != sourceOffWait || len(e.yt.mixCalls) != 0 {
		t.Errorf("discovery: %s, %d mix calls", w, len(e.yt.mixCalls))
	}
	if w := e.svc.workStep(ctx); w != sourceOffWait {
		t.Errorf("episode worker: %s", w)
	}
}
