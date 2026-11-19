package videosrc

import (
	"context"
	"reflect"
	"testing"

	"github.com/aaronsuns/lark-server/internal/testutil"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func TestParse(t *testing.T) {
	yt, bb := ytdlp.SourceYouTube, ytdlp.SourceBilibili
	ok := map[string][]ytdlp.Source{
		"youtube":           {yt},
		"Bilibili, youtube": {bb, yt},
		"youtube,youtube":   {yt},
		" bilibili ":        {bb},
		"none":              {},
		"NONE":              {},
	}
	for in, want := range ok {
		got, err := Parse(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "vimeo", "youtube;bilibili", "youtube,", "none,youtube"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) accepted", in)
		}
	}
}

func TestServiceDefaultThenAdminOverride(t *testing.T) {
	ctx := context.Background()
	s := &Service{DB: testutil.DB(t), Default: []ytdlp.Source{ytdlp.SourceYouTube}}
	got, by, err := s.List(ctx)
	if err != nil || by != "env" || !reflect.DeepEqual(got, []ytdlp.Source{ytdlp.SourceYouTube}) {
		t.Fatalf("default: %v %q %v", got, by, err)
	}
	if !s.Enabled(ctx, ytdlp.SourceYouTube) || s.Enabled(ctx, ytdlp.SourceBilibili) {
		t.Fatal("default enablement wrong")
	}
	if err := s.Set(ctx, []ytdlp.Source{ytdlp.SourceBilibili}); err != nil {
		t.Fatal(err)
	}
	got, by, _ = s.List(ctx)
	if by != "admin" || !reflect.DeepEqual(got, []ytdlp.Source{ytdlp.SourceBilibili}) {
		t.Fatalf("after set: %v %q", got, by)
	}
	if s.Enabled(ctx, ytdlp.SourceYouTube) || !s.Enabled(ctx, ytdlp.SourceBilibili) {
		t.Fatal("admin choice not applied")
	}
}

func TestServiceEmptyListIsValid(t *testing.T) {
	ctx := context.Background()
	s := &Service{DB: testutil.DB(t), Default: []ytdlp.Source{ytdlp.SourceYouTube}}
	if err := s.Set(ctx, nil); err != nil {
		t.Fatal(err)
	}
	got, by, _ := s.List(ctx)
	if by != "admin" || len(got) != 0 || got == nil {
		t.Fatalf("empty: %#v %q", got, by)
	}
	if s.Enabled(ctx, ytdlp.SourceYouTube) {
		t.Fatal("youtube still on")
	}
}

func TestServiceSetRejectsUnknown(t *testing.T) {
	s := &Service{DB: testutil.DB(t)}
	if err := s.Set(context.Background(), []ytdlp.Source{"vimeo"}); err == nil {
		t.Fatal("unknown source accepted")
	}
}

func TestNilServiceIsYouTubeOnly(t *testing.T) {
	var s *Service
	if !s.Enabled(context.Background(), ytdlp.SourceYouTube) || s.Enabled(context.Background(), ytdlp.SourceBilibili) {
		t.Fatal("nil service must mean YouTube only")
	}
}
