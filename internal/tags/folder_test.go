package tags

import (
	"context"
	"slices"
	"sort"
	"testing"

	"github.com/aaronsuns/lark-server/internal/config"
	"github.com/aaronsuns/lark-server/internal/testutil"
)

func TestFolderRules(t *testing.T) {
	f := &FolderTagger{Rules: config.DefaultFolderRules(), Vocab: Vocabulary()}
	got := func(rel string) []string {
		var out []string
		for _, tg := range f.TagsFor(rel) {
			out = append(out, tg.Name)
		}
		sort.Strings(out)
		return out
	}
	for rel, want := range map[string][]string{
		"200首纯音乐/01.wav":             {"instrumental"},
		"咖啡馆休闲音乐/a.flac":             {"cafe", "chill"},
		"邓丽君精选/01 甜蜜蜜.mp3":           {"classic"},
		"Piano Man - Billy Joel.mp3": nil,                  // file names never match, only folders
		"Classic Rock/钢琴曲/x.mp3":     {"piano", "rock"},    // piano songs often have vocals
		"轻音乐精选/x.mp3":                {"chill", "classic"}, // light music isn't necessarily instrumental
		"輕音樂/x.mp3":                  {"chill"},
		"Instrumental Hits/x.mp3":    {"instrumental"},
	} {
		if g := got(rel); !slices.Equal(g, want) {
			t.Errorf("%s → %v, want %v", rel, g, want)
		}
	}
	if err := ValidateRules([]config.FolderRule{{Match: []string{"x"}, Tags: []string{"notaslug"}}}, Vocabulary()); err == nil {
		t.Error("unknown slug accepted")
	}
	if err := ValidateRules(config.DefaultFolderRules(), Vocabulary()); err != nil {
		t.Errorf("default rules invalid: %v", err)
	}
}

func TestBackfillOncePerRuleSetAndTombstonesSurvive(t *testing.T) {
	d := testutil.DB(t)
	d.Exec(`INSERT INTO libraries(id,name,root) VALUES (1,'m','/m')`)
	d.Exec(`INSERT INTO tracks(id,library_id,rel_path,size,mtime,fingerprint,status,added_at) VALUES
		(1,1,'纯音乐/a.mp3',1,1,'f','kept',0),(2,1,'咖啡/b.mp3',1,1,'f','kept',0),(3,1,'x/c.mp3',1,1,'f','trashed',0)`)
	s := &Store{DB: d}
	f := &FolderTagger{Store: s, Rules: config.DefaultFolderRules(), Vocab: Vocabulary()}
	ctx := context.Background()
	if n, err := f.Backfill(ctx); err != nil || n != 2 {
		t.Fatalf("first backfill %d %v", n, err)
	}
	s.Replace(ctx, 2, "manual", []Tag{{"cafe", "scene"}}) // user removes "chill"
	if n, _ := f.Backfill(ctx); n != 0 {
		t.Fatalf("same rules ran twice: %d", n)
	}
	f.Rules = append(f.Rules, config.FolderRule{Match: []string{"咖啡"}, Tags: []string{"morning"}})
	if n, _ := f.Backfill(ctx); n != 2 {
		t.Fatalf("changed rules must re-run: %d", n)
	}
	got, _ := s.ForTrack(ctx, 2)
	if n := names(got); n["chill"] || !n["cafe"] || !n["morning"] {
		t.Fatalf("tombstone lost or new rule missing: %v", n)
	}
}

// The startup backfill runs while the scanner does: a track moved during the
// pass keeps its new folder's tags, and one trashed meanwhile is left alone.
func TestBackfillRereadsEachTrack(t *testing.T) {
	d := testutil.DB(t)
	d.Exec(`INSERT INTO libraries(id,name,root) VALUES (1,'m','/m')`)
	d.Exec(`INSERT INTO tracks(id,library_id,rel_path,size,mtime,fingerprint,status,added_at) VALUES
		(1,1,'纯音乐/a.mp3',1,1,'f','kept',0),(2,1,'纯音乐/b.mp3',1,1,'f','kept',0),(3,1,'纯音乐/c.mp3',1,1,'f','kept',0)`)
	ctx := context.Background()
	var f *FolderTagger
	var changed []int64
	moved := false
	s := &Store{DB: d, OnChange: func(ctx context.Context, id int64) error {
		changed = append(changed, id)
		if id == 1 && !moved { // the scanner moves 2 and trashes 3 mid-pass
			moved = true
			d.Exec(`UPDATE tracks SET rel_path='咖啡/b.mp3' WHERE id=2`)
			if err := f.Apply(ctx, 2, "咖啡/b.mp3"); err != nil {
				return err
			}
			d.Exec(`UPDATE tracks SET status='trashed' WHERE id=3`)
		}
		return nil
	}}
	f = &FolderTagger{Store: s, Rules: config.DefaultFolderRules(), Vocab: Vocabulary()}
	if n, err := f.Backfill(ctx); err != nil || n != 2 {
		t.Fatalf("backfill %d %v", n, err)
	}
	got, _ := s.ForTrack(ctx, 2)
	if n := names(got); n["instrumental"] || !n["cafe"] {
		t.Fatalf("moved track got its old folder's tags: %v", n)
	}
	if slices.Contains(changed, 3) {
		t.Fatalf("trashed track was tagged/reindexed: %v", changed)
	}
}
