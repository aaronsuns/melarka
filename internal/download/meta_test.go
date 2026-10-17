package download

import (
	"context"
	"testing"
	"time"
)

func metaRow(t *testing.T, e *env, videoID string) (tags, cats, desc string, ok bool) {
	t.Helper()
	err := e.svc.DB.QueryRow(`SELECT tags, categories, description FROM video_meta WHERE video_id=?`, videoID).Scan(&tags, &cats, &desc)
	return tags, cats, desc, err == nil
}

// A download keeps what YouTube says about the video (tags, categories, description) for tagging.
func TestDownloadKeepsVideoMeta(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"v1_0000000x", "王菲 - 流年", "FayeWongVEVO"}}
	e.fake.meta = map[string]string{"v1_0000000x": `{"tags":["王菲","流年"],"categories":["Music"],"description":"作曲：陈小霞"}`}
	e.start(t)
	jobs, err := e.svc.Enqueue(context.Background(), e.alice, watchURL("v1_0000000x"))
	if err != nil {
		t.Fatal(err)
	}
	e.waitStatus(t, jobs[0].ID, StatusDone)
	tags, cats, desc, ok := metaRow(t, e, "v1_0000000x")
	if !ok || tags != `["王菲","流年"]` || cats != `["Music"]` || desc != "作曲：陈小霞" {
		t.Fatalf("%v %s %s %q", ok, tags, cats, desc)
	}
}

type countGate struct{ n int }

func (g *countGate) Low(ctx context.Context, fn func(context.Context) error) error {
	g.n++
	return fn(ctx)
}

// Songs downloaded before Meta was kept get it in the background (through the low-priority gate),
// and go back to the tagging agent; a video that can't be read is not asked again this run.
func TestMetaBackfillAndRetag(t *testing.T) {
	e := newEnv(t, true)
	e.start(t)
	ctx := context.Background()
	for _, id := range []string{"v1_0000000x", "v2_0000000x"} {
		e.fake.mu.Lock()
		e.fake.entries = []entry{{id, "Song " + id, "C"}} // the fake resolves any link to its entries: one each
		e.fake.mu.Unlock()
		jobs, err := e.svc.Enqueue(ctx, e.alice, watchURL(id))
		if err != nil {
			t.Fatal(err)
		}
		j := e.waitStatus(t, jobs[0].ID, StatusDone)
		if _, err := e.svc.DB.Exec(`INSERT INTO tagging_state(track_id, agent_at) VALUES (?, 100) ON CONFLICT(track_id) DO UPDATE SET agent_at=100`, *j.TrackID); err != nil {
			t.Fatal(err)
		}
	}
	e.fake.mu.Lock()
	e.fake.meta = map[string]string{"v1_0000000x": `{"tags":["Cantopop"],"categories":["Music"],"description":""}`}
	e.fake.mu.Unlock()
	gate := &countGate{}
	n, err := e.svc.BackfillMeta(ctx, gate, 0, 10)
	if err != nil || n != 1 || gate.n != 2 {
		t.Fatalf("filled %d, gate %d, %v", n, gate.n, err)
	}
	if tags, _, _, ok := metaRow(t, e, "v1_0000000x"); !ok || tags != `["Cantopop"]` {
		t.Fatalf("%v %s", ok, tags)
	}
	if _, _, _, ok := metaRow(t, e, "v2_0000000x"); ok {
		t.Fatal("an unreadable video got a row")
	}
	var pending int
	e.svc.DB.QueryRow(`SELECT COUNT(*) FROM tagging_state s JOIN downloads d ON d.track_id=s.track_id
		WHERE d.video_id='v1_0000000x' AND s.agent_at IS NULL`).Scan(&pending)
	if pending != 1 {
		t.Fatal("the track with new YouTube tags was not sent back to the agent")
	}
	// Nothing left to fill (v2 failed this run): no more calls.
	if n, _ := e.svc.BackfillMeta(ctx, gate, time.Millisecond, 10); n != 0 || gate.n != 2 {
		t.Fatalf("second run filled %d, gate %d", n, gate.n)
	}
}
