package prefs

import (
	"context"
	"errors"
	"testing"

	"github.com/aaronsuns/lark-server/internal/testutil"
)

func TestPrefsDefaultsPutAndValidate(t *testing.T) {
	d := testutil.DB(t)
	d.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'a','h','member',0),(2,'b','h','member',0)`)
	s := &Store{DB: d}
	ctx := context.Background()
	p, err := s.Get(ctx, 1)
	if err != nil || p.Language != nil || p.OnOpen != "shuffle_favorites" {
		t.Fatalf("defaults %+v %v", p, err)
	}
	sv := "sv"
	if _, err := s.Put(ctx, 1, Prefs{Language: &sv, OnOpen: "resume"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Get(ctx, 1); *p.Language != "sv" || p.OnOpen != "resume" {
		t.Fatalf("after put %+v", p)
	}
	if p, _ := s.Get(ctx, 2); p.Language != nil {
		t.Fatal("prefs leaked to another user")
	}
	if _, err := s.Put(ctx, 1, Prefs{OnOpen: "explode"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad on_open: %v", err)
	}
	bad := "fr"
	if _, err := s.Put(ctx, 1, Prefs{Language: &bad, OnOpen: "resume"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad language: %v", err)
	}
	if _, err := s.Put(ctx, 1, Prefs{Language: nil, OnOpen: "nothing"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Get(ctx, 1); p.Language != nil || p.OnOpen != "nothing" {
		t.Fatalf("null language must clear: %+v", p)
	}
}

func TestCarLyricsDefaultsOnAndSurvivesOlderClients(t *testing.T) {
	d := testutil.DB(t)
	d.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'a','h','member',0)`)
	s := &Store{DB: d}
	ctx := context.Background()
	if p, err := s.Get(ctx, 1); err != nil || p.CarLyrics == nil || !*p.CarLyrics {
		t.Fatalf("car lyrics must default on: %+v %v", p, err)
	}
	off := false
	p, err := s.Put(ctx, 1, Prefs{OnOpen: "resume", CarLyrics: &off})
	if err != nil || p.CarLyrics == nil || *p.CarLyrics {
		t.Fatalf("put off: %+v %v", p, err)
	}
	// A client that predates the field omits it: the stored choice stays.
	if p, err := s.Put(ctx, 1, Prefs{OnOpen: "nothing"}); err != nil || *p.CarLyrics || p.OnOpen != "nothing" {
		t.Fatalf("omitted car_lyrics must keep the stored value: %+v %v", p, err)
	}
	d.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (2,'b','h','member',0)`)
	if p, err := s.Put(ctx, 2, Prefs{OnOpen: "resume"}); err != nil || !*p.CarLyrics {
		t.Fatalf("first put without the field must default on: %+v %v", p, err)
	}
}

func TestNormalizeLoudnessDefaultsOnAndSurvivesOlderClients(t *testing.T) {
	d := testutil.DB(t)
	d.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'a','h','member',0)`)
	s := &Store{DB: d}
	ctx := context.Background()
	if p, err := s.Get(ctx, 1); err != nil || p.NormalizeLoudness == nil || !*p.NormalizeLoudness {
		t.Fatalf("loudness normalization must default on: %+v %v", p, err)
	}
	off := false
	p, err := s.Put(ctx, 1, Prefs{OnOpen: "resume", NormalizeLoudness: &off})
	if err != nil || p.NormalizeLoudness == nil || *p.NormalizeLoudness {
		t.Fatalf("put off: %+v %v", p, err)
	}
	if p.CarLyrics == nil || !*p.CarLyrics {
		t.Fatalf("turning loudness off must not touch car lyrics: %+v", p)
	}
	// A client that predates the field omits it: the stored choice stays.
	if p, err := s.Put(ctx, 1, Prefs{OnOpen: "nothing"}); err != nil || *p.NormalizeLoudness || p.OnOpen != "nothing" {
		t.Fatalf("omitted normalize_loudness must keep the stored value: %+v %v", p, err)
	}
	d.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (2,'b','h','member',0)`)
	if p, err := s.Put(ctx, 2, Prefs{OnOpen: "resume"}); err != nil || !*p.NormalizeLoudness {
		t.Fatalf("first put without the field must default on: %+v %v", p, err)
	}
}
