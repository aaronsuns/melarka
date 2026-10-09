package personal

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/aaronsuns/lark-server/internal/testutil"
)

func TestSearchHistory(t *testing.T) {
	d := testutil.DB(t)
	d.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'a','h','member',0),(2,'b','h','member',0)`)
	s := &Store{DB: d}
	ctx := context.Background()
	got := func(u int64) []string {
		t.Helper()
		h, err := s.SearchHistory(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	if h := got(1); h == nil || len(h) != 0 {
		t.Fatalf("empty history must be [] not nil: %#v", h)
	}
	for _, q := range []string{"邓丽君", "Teresa Teng", "甜蜜蜜"} {
		if err := s.RecordSearch(ctx, 1, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordSearch(ctx, 1, "  teresa   TENG "); err != nil { // same search, other spelling
		t.Fatal(err)
	}
	if h := got(1); !slices.Equal(h, []string{"teresa TENG", "甜蜜蜜", "邓丽君"}) {
		t.Fatalf("dedupe/move-to-top: %v", h)
	}
	if h := got(2); len(h) != 0 {
		t.Fatalf("other user sees %v", h)
	}
	for i := range 25 {
		s.RecordSearch(ctx, 1, fmt.Sprintf("q%02d", i))
	}
	h := got(1)
	if len(h) != SearchHistoryCap || h[0] != "q24" || h[19] != "q05" {
		t.Fatalf("cap: %d %v", len(h), h)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("歌", 101)} {
		if err := s.RecordSearch(ctx, 1, bad); err != ErrInvalid {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if err := s.ClearSearchHistory(ctx, 1); err != nil || len(got(1)) != 0 {
		t.Fatalf("clear: %v %v", err, got(1))
	}
}

// Swedish names dedupe too: SQLite NOCASE is ASCII-only, so the key is lowered in Go.
func TestSearchHistoryUnicodeDedupe(t *testing.T) {
	d := testutil.DB(t)
	d.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'a','h','member',0)`)
	s := &Store{DB: d}
	ctx := context.Background()
	for _, q := range []string{"Åsa", "Kent", " åsa "} {
		if err := s.RecordSearch(ctx, 1, q); err != nil {
			t.Fatal(err)
		}
	}
	h, err := s.SearchHistory(ctx, 1)
	if err != nil || !slices.Equal(h, []string{"åsa", "Kent"}) {
		t.Fatalf("Å/å: %v %v", h, err)
	}
}
