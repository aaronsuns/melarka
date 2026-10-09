package tags

import (
	"context"
	"testing"

	"github.com/aaronsuns/lark-server/internal/testutil"
)

func names(ts []Tag) map[string]bool {
	m := map[string]bool{}
	for _, t := range ts {
		m[t.Name] = true
	}
	return m
}

func TestManualWinsOverAutomatic(t *testing.T) {
	d := testutil.DB(t)
	d.Exec(`INSERT INTO libraries(id,name,root) VALUES (1,'m','/m')`)
	d.Exec(`INSERT INTO tracks(id,library_id,rel_path,size,mtime,fingerprint,status,added_at) VALUES (1,1,'a',1,1,'f','kept',0)`)
	s := &Store{DB: d}
	ctx := context.Background()
	if err := s.Replace(ctx, 1, "lastfm", []Tag{{"oldies", "era"}, {"chill", "mood"}}); err != nil {
		t.Fatal(err)
	}
	// user removes "chill", adds "开车"
	if err := s.Replace(ctx, 1, "manual", []Tag{{"oldies", "era"}, {"开车", "scene"}}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ForTrack(ctx, 1)
	if n := names(got); len(n) != 2 || !n["oldies"] || !n["开车"] {
		t.Fatalf("after manual %v", n)
	}
	// Last.fm re-run must not bring "chill" back
	s.Replace(ctx, 1, "lastfm", []Tag{{"oldies", "era"}, {"chill", "mood"}, {"mandopop", "genre"}})
	got, _ = s.ForTrack(ctx, 1)
	if n := names(got); n["chill"] || !n["mandopop"] || !n["开车"] {
		t.Fatalf("after rerun %v", n)
	}
	if err := s.Replace(ctx, 1, "bogus", nil); err == nil {
		t.Fatal("unknown source accepted")
	}
}

func TestPendingForTagging(t *testing.T) {
	d := testutil.DB(t)
	d.Exec(`INSERT INTO libraries(id,name,root) VALUES (1,'m','/m')`)
	d.Exec(`INSERT INTO tracks(id,library_id,rel_path,size,mtime,fingerprint,status,added_at) VALUES
		(1,1,'a',1,1,'f','kept',0),(2,1,'b',1,1,'f','kept',0),(3,1,'c',1,1,'f','pending',5),(4,1,'d',1,1,'f','trashed',0)`)
	s := &Store{DB: d}
	ctx := context.Background()
	if err := s.MarkAgentDone(ctx, []int64{2}); err != nil {
		t.Fatal(err)
	}
	ids, _ := s.PendingForTagging(ctx, 10)
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 3 {
		t.Fatalf("pending=%v, want [1 3] (kept first, agent-covered and trashed excluded)", ids)
	}
	// Having two tags no longer matters; only an agent batch removes a track from the queue.
	s.Replace(ctx, 1, "manual", []Tag{{"a", "other"}, {"b", "other"}})
	if ids, _ = s.PendingForTagging(ctx, 10); len(ids) != 2 {
		t.Fatalf("tagged-but-unseen track left the queue: %v", ids)
	}
}

func TestOnChangeAndVocabularyKindWins(t *testing.T) {
	d := testutil.DB(t)
	d.Exec(`INSERT INTO libraries(id,name,root) VALUES (1,'m','/m')`)
	d.Exec(`INSERT INTO tracks(id,library_id,rel_path,size,mtime,fingerprint,status,added_at) VALUES (1,1,'a',1,1,'f','kept',0)`)
	var calls []int64
	s := &Store{DB: d, OnChange: func(_ context.Context, id int64) error { calls = append(calls, id); return nil }}
	ctx := context.Background()
	s.Replace(ctx, 1, "manual", []Tag{{"chill", "other"}}) // stale kind
	s.Replace(ctx, 1, "folder_rule", []Tag{{"chill", "mood"}})
	if len(calls) != 2 || calls[0] != 1 {
		t.Fatalf("OnChange calls %v", calls)
	}
	got, _ := s.ForTrack(ctx, 1)
	if len(got) != 1 || got[0].Kind != "mood" {
		t.Fatalf("kind not fixed: %v", got)
	}
}
