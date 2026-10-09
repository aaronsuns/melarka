package tags

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestVocabularyLookup(t *testing.T) {
	v := Vocabulary() // also proves the shipped file parses (Vocabulary panics otherwise)
	if n := len(v.All()); n < 50 || n > 70 {
		t.Fatalf("vocabulary size %d", n)
	}
	if _, ok := v.Lookup("instrumental"); !ok {
		t.Fatal("vocabulary must contain instrumental (lyrics lookup skips it)")
	}
	for raw, want := range map[string]string{"Chill out": "chill", "C-Pop": "mandopop", "hip_hop": "hip-hop", "R&B": "rnb", "oldies": "classic", "80S": "80s", "mandopop": "mandopop"} {
		if e, ok := v.Lookup(raw); !ok || e.Slug != want {
			t.Errorf("%q → %+v %v, want %s", raw, e, ok, want)
		}
	}
	if _, ok := v.Lookup("seen live"); ok {
		t.Error("unmapped Last.fm tag accepted")
	}
	if w := SearchWords("instrumental"); !slices.Contains(w, "纯音乐") || !slices.Contains(w, "純音樂") || !slices.Contains(w, "Instrumentalt") {
		t.Errorf("search words %v", w)
	}
	if w := SearchWords("开车啦"); !slices.Equal(w, []string{"开车啦"}) {
		t.Errorf("free tag %v", w)
	}
}

func TestParseVocabularyRejectsBadFiles(t *testing.T) {
	names := `names: {en: A, zh-Hans: A, zh-Hant: A, sv: A}`
	for _, bad := range []string{
		"- {slug: Bad Slug, kind: mood, " + names + "}",
		"- {slug: ok, kind: vibe, " + names + "}",
		"- {slug: a, kind: mood, aliases: [x], " + names + "}\n- {slug: b, kind: mood, aliases: [x], " + names + "}",
		"- {slug: a, kind: mood, names: {en: A}}",
		"- {slug: a, kind: mood, " + names + "}\n- {slug: a, kind: mood, " + names + "}",
		"- {slug: a, kind: mood, " + names + "}\n- {slug: b, kind: mood, aliases: [a], " + names + "}",
	} {
		if _, err := ParseVocabulary([]byte(bad)); err == nil {
			t.Errorf("accepted:\n%s", bad)
		}
	}
	// An alias that normalises to the entry's own slug is fine (ruling P14).
	if _, err := ParseVocabulary([]byte("- {slug: hip-hop, kind: genre, aliases: [hip hop], " + names + "}")); err != nil {
		t.Errorf("own-slug alias rejected: %v", err)
	}
}

// The UI labels live in the web locale files; vocabulary.yaml's names must match them,
// so search (which indexes names) finds exactly what the UI shows.
func TestVocabularyMatchesWebLocales(t *testing.T) {
	for _, l := range []string{"en", "zh-Hans", "zh-Hant", "sv"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "i18n", "locales", l+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var d map[string]string
		if err := json.Unmarshal(b, &d); err != nil {
			t.Fatal(err)
		}
		for _, e := range Vocabulary().All() {
			if d["tag."+e.Slug] != e.Names[l] {
				t.Errorf("%s: tag.%s = %q, vocabulary says %q", l, e.Slug, d["tag."+e.Slug], e.Names[l])
			}
		}
		for k := range kinds {
			if d["tagKind."+k] == "" {
				t.Errorf("%s: missing tagKind.%s", l, k)
			}
		}
	}
}
