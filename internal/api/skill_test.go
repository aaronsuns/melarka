package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tagging skill is read by an agent that will curl these routes; if one is
// renamed the skill must fail here rather than in a 35-batch run.
func TestTaggingSkillCitesRealRoutes(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "skills", "lark-tagging", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	for _, r := range [][2]string{
		{"GET", "/api/v1/tags/vocabulary"},
		{"GET", "/api/v1/admin/tagging/pending?limit=100"},
		{"PUT", "/api/v1/admin/tagging/batch"},
		{"GET", "/api/v1/tags"},
		{"POST", "/api/v1/auth/logout"},
	} {
		if !strings.Contains(doc, strings.SplitN(r[1], "?", 2)[0]) {
			t.Errorf("SKILL.md does not mention %s", r[1])
		}
		var body any
		if r[0] == "PUT" {
			body = []any{} // answers 400 "1 to 500 items", not 404
		}
		if resp, _ := do(t, ts, adm, r[0], r[1], body); resp.StatusCode == 404 || resp.StatusCode == 405 {
			t.Errorf("%s %s → %d", r[0], r[1], resp.StatusCode)
		}
	}
	if !strings.Contains(doc, "/api/v1/auth/login") {
		t.Error("SKILL.md does not mention /api/v1/auth/login")
	}
	for _, must := range []string{"limit=100", "vocabulary", "source", "agent", "unknown_tags"} {
		if !strings.Contains(doc, must) {
			t.Errorf("SKILL.md lacks %q", must)
		}
	}
}

// The lyrics skill curls these routes; a rename must fail here, not mid-run.
func TestLyricsSkillCitesRealRoutes(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "skills", "lark-lyrics", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	id := seedTrack(t, s, "a.mp3", "甜蜜蜜", "邓丽君", "Y")
	// The broad pass's body shape is accepted (before the loop signs out).
	if resp, b := do(t, ts, adm, "POST", fmt.Sprintf("/api/v1/tracks/%d/lyrics/refresh", id), map[string]any{"title": "甜蜜蜜", "artist": "", "broad": true}); resp.StatusCode != 200 {
		t.Errorf("broad refresh → %d %s", resp.StatusCode, b)
	}
	for _, r := range [][3]string{
		{"GET", "/api/v1/admin/lyrics/missing?scope=favorites&limit=50", "/api/v1/admin/lyrics/missing"},
		{"GET", "/api/v1/admin/lyrics/missing?scope=all&reported=1&limit=50", "/api/v1/admin/lyrics/missing"},
		{"GET", "/api/v1/admin/lyrics/missing/count?scope=all", "/api/v1/admin/lyrics/missing/count"},
		{"POST", fmt.Sprintf("/api/v1/tracks/%d/lyrics/refresh", id), "/api/v1/tracks/{id}/lyrics/refresh"},
		{"GET", fmt.Sprintf("/api/v1/tracks/%d/tags", id), "/api/v1/tracks/{id}/tags"},
		{"PUT", fmt.Sprintf("/api/v1/tracks/%d/tags", id), "/api/v1/tracks/{id}/tags"},
		{"PATCH", fmt.Sprintf("/api/v1/tracks/%d", id), "/api/v1/tracks/{id}"},
		{"POST", "/api/v1/auth/logout", "/api/v1/auth/logout"},
	} {
		if !strings.Contains(doc, r[2]) {
			t.Errorf("SKILL.md does not mention %s", r[2])
		}
		var body any
		switch r[0] {
		case "PUT":
			body = map[string]any{"source": "manual", "tags": []any{map[string]string{"name": "instrumental", "kind": "genre"}}}
		case "PATCH":
			body = map[string]any{"title": "甜蜜蜜"}
		case "POST":
			body = map[string]any{"title": "甜蜜蜜", "artist": "邓丽君"}
		}
		if strings.HasSuffix(r[1], "/logout") {
			body = nil
		}
		if resp, b := do(t, ts, adm, r[0], r[1], body); resp.StatusCode >= 400 {
			t.Errorf("%s %s → %d %s", r[0], r[1], resp.StatusCode, b)
		}
	}
	for _, must := range []string{"/api/v1/auth/login", "jq -er", "LARK_URL", "LARK_USER", "LARK_PASSWORD",
		"instrumental", "\"source\":\"manual\"", "scope=favorites", "scope=downloads", "scope=all", "after=",
		"title_edited", "artist_edited", "one field per call",
		"broad:true", "BROAD", "never on scope=all", "reported_wrong", "REPORTED", "reported=1"} {
		if !strings.Contains(doc, must) {
			t.Errorf("SKILL.md lacks %q", must)
		}
	}
}

// The names skill (and the nightly prompt built from it) curls these routes.
func TestNamesSkillCitesRealRoutes(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "skills", "lark-names", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	id := seedTrack(t, s, "a.mp3", "白狐", "陈瑞", "Y")
	for _, r := range [][3]string{
		{"GET", "/api/v1/admin/metadata/review?scope=favorites&limit=50", "/api/v1/admin/metadata/review"},
		{"GET", "/api/v1/admin/metadata/review?scope=downloads&limit=50&after=0", "/api/v1/admin/metadata/review"},
		{"GET", "/api/v1/admin/metadata/review/count?scope=favorites", "/api/v1/admin/metadata/review/count"},
		{"PATCH", fmt.Sprintf("/api/v1/tracks/%d", id), "/api/v1/tracks/{id}"},
		{"GET", fmt.Sprintf("/api/v1/admin/metadata/review/%d", id), "/api/v1/admin/metadata/review/{id}"},
		{"PUT", fmt.Sprintf("/api/v1/admin/metadata/review/%d", id), "/api/v1/admin/metadata/review/{id}"},
		{"POST", "/api/v1/auth/logout", "/api/v1/auth/logout"},
	} {
		if !strings.Contains(doc, r[2]) {
			t.Errorf("SKILL.md does not mention %s", r[2])
		}
		var body any
		switch r[0] {
		case "PATCH":
			body = map[string]any{"no_album": true}
		case "PUT":
			body = map[string]string{"outcome": "fixed"}
		}
		if resp, b := do(t, ts, adm, r[0], r[1], body); resp.StatusCode >= 400 {
			t.Errorf("%s %s → %d %s", r[0], r[1], resp.StatusCode, b)
		}
	}
	for _, must := range []string{"/api/v1/auth/login", "jq -n --arg", "LARK_URL", "LARK_USER", "LARK_PASSWORD",
		"title_edited", "artist_edited", "album_edited", "year_edited", "one field per call", "no_album", "no_year",
		"fixed", "skipped", "after=", "never invent an album", "Untrusted data", "source", "skipped (edited, looks wrong"} {
		if !strings.Contains(doc, must) {
			t.Errorf("SKILL.md lacks %q", must)
		}
	}
	if strings.Contains(doc, "garbage") { // edited fields are never touched, no judgement-call exceptions
		t.Error("SKILL.md allows touching edited fields")
	}
	lb, err := os.ReadFile(filepath.Join("..", "..", "skills", "lark-lyrics", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lb), "lark-names") {
		t.Error("lark-lyrics SKILL.md does not point at lark-names")
	}
}
