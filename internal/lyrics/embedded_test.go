package lyrics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeTags struct {
	tags map[string]string
	err  error
}

func (f fakeTags) Tags(context.Context, string) (map[string]string, error) { return f.tags, f.err }

func TestEmbeddedSidecarAndTag(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	song := filepath.Join(dir, "song.mp3")
	os.WriteFile(song, []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "song.lrc"), []byte("[00:01.00]a\n[00:02.00]b\n[00:03.00]c\n[00:04.00]d\n"), 0o644)

	p := &Embedded{Tags: fakeTags{tags: map[string]string{}}}
	if p.Name() != "embedded" {
		t.Fatal(p.Name())
	}
	cs, err := p.Search(ctx, Query{Title: "x", Path: song})
	if err != nil || len(cs) != 1 || cs[0].Source != "embedded" || cs[0].ExternalID != "sidecar" || !cs[0].Synced || cs[0].Title != "" {
		t.Fatalf("sidecar: %+v %v", cs, err)
	}

	other := filepath.Join(dir, "other.mp3")
	os.WriteFile(other, []byte("x"), 0o644)
	p = &Embedded{Tags: fakeTags{tags: map[string]string{"title": "x", "lyrics-eng": "line one\nline two"}}}
	cs, err = p.Search(ctx, Query{Title: "x", Path: other})
	if err != nil || len(cs) != 1 || cs[0].ExternalID != "tag" || cs[0].Synced || cs[0].Text != "line one\nline two" {
		t.Fatalf("tag: %+v %v", cs, err)
	}

	p = &Embedded{Tags: fakeTags{tags: map[string]string{"title": "x"}}}
	if cs, err := p.Search(ctx, Query{Title: "x", Path: other}); cs != nil || err != nil {
		t.Fatalf("neither: %+v %v", cs, err)
	}
	p = &Embedded{Tags: fakeTags{err: errors.New("ffprobe: exit status 1")}}
	if _, err := p.Search(ctx, Query{Title: "x", Path: other}); err == nil {
		t.Fatal("tag read failure must be an error, not a clean miss")
	}
}

// Several lyrics-<lang> tags: always the same one, never map order.
func TestEmbeddedTagChoiceIsDeterministic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "multi.mp3")
	os.WriteFile(path, []byte("x"), 0o644)
	p := &Embedded{Tags: fakeTags{tags: map[string]string{"lyrics-eng": "english", "lyrics-chi": "中文", "lyrics-swe": "svenska"}}}
	for range 50 {
		cs, err := p.Search(context.Background(), Query{Path: path})
		if err != nil || len(cs) != 1 || cs[0].Text != "中文" {
			t.Fatalf("%+v %v", cs, err)
		}
	}
	p = &Embedded{Tags: fakeTags{tags: map[string]string{"lyrics-eng": "english", "lyrics": "bare"}}}
	if cs, _ := p.Search(context.Background(), Query{Path: path}); len(cs) != 1 || cs[0].Text != "bare" {
		t.Fatalf("bare lyrics tag must win: %+v", cs)
	}
}
