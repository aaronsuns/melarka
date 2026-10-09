package library

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aaronsuns/lark-server/internal/config"
	"github.com/aaronsuns/lark-server/internal/tags"
	"github.com/aaronsuns/lark-server/internal/testutil"
)

func TestSearchMatchesChineseAndPinyin(t *testing.T) {
	d := testutil.DB(t)
	docs := map[int]string{
		1: SearchDoc("甜蜜蜜", "邓丽君", "邓丽君精选", "B021.邓丽君－邓丽君精选 -1"),
		2: SearchDoc("Faded", "Alan Walker", "Different World", "Alan Walker (艾伦·沃克)"),
		3: SearchDoc("冲动的惩罚", "刀郎", "", "刀郎"),
	}
	for id, doc := range docs {
		if _, err := d.Exec(`INSERT INTO track_fts(rowid, doc) VALUES (?,?)`, id, doc); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string][]int{
		"丽君":        {1},
		"邓丽君 甜蜜":    {1},
		"dlj":       {1},
		"denglijun": {1},
		"tmm":       {1},
		"walk":      {2},
		"ALAN":      {2},
		"刀":         {3},
		"dl":        {1, 3}, // prefix of dlj and daolang
		"zzzz":      nil,
	}
	for q, want := range cases {
		expr := FTSQuery(q)
		rows, err := d.Query(`SELECT rowid FROM track_fts WHERE track_fts MATCH ? ORDER BY rowid`, expr)
		if err != nil {
			t.Fatalf("%q → %q: %v", q, expr, err)
		}
		var got []int
		for rows.Next() {
			var id int
			rows.Scan(&id)
			got = append(got, id)
		}
		rows.Close()
		if len(got) != len(want) {
			t.Errorf("%q (%s): got %v want %v", q, expr, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%q: got %v want %v", q, got, want)
			}
		}
	}
	if FTSQuery(`  "*  `) != "" {
		t.Error("punctuation-only query must be empty")
	}
}

func TestSearchFindsTagWords(t *testing.T) {
	e := newEnv(t, fakeProber{})
	tg := &tags.Store{DB: e.st.DB, OnChange: e.st.Reindex}
	e.sc.Tagger = &tags.FolderTagger{Store: tg, Rules: config.DefaultFolderRules(), Vocab: tags.Vocabulary()}
	writeRandom(t, filepath.Join(e.root, "200首纯音乐", "01.mp3"))
	writeRandom(t, filepath.Join(e.root, "misc", "other.mp3"))
	e.scan(t)
	var id, other int64
	e.st.DB.QueryRow(`SELECT id FROM tracks WHERE rel_path='200首纯音乐/01.mp3'`).Scan(&id)
	e.st.DB.QueryRow(`SELECT id FROM tracks WHERE rel_path='misc/other.mp3'`).Scan(&other)
	ctx := context.Background()
	found := func(q string, want int64) bool {
		res, err := e.st.Search(ctx, 1, q, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, tr := range res.Tracks {
			if tr.ID == want {
				return true
			}
		}
		return false
	}
	for _, q := range []string{"纯音乐", "instrumental", "chunyinyue", "Instrumentalt"} {
		if !found(q, id) {
			t.Errorf("%q does not find the folder-tagged track", q)
		}
	}
	if found("instrumental", other) {
		t.Error("untagged track matched")
	}
	if found("开车", other) {
		t.Fatal("precondition")
	}
	if err := tg.Replace(ctx, other, "manual", []tags.Tag{{Name: "开车", Kind: "scene"}}); err != nil {
		t.Fatal(err)
	}
	if !found("开车", other) {
		t.Error("manual tag not searchable")
	}
}
