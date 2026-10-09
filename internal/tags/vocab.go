package tags

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed vocabulary.yaml
var vocabularyYAML []byte

// locales are the UI languages every vocabulary entry must be named in.
var locales = []string{"en", "zh-Hans", "zh-Hant", "sv"}

var slugRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type VocabEntry struct {
	Slug    string            `yaml:"slug" json:"slug"`
	Kind    string            `yaml:"kind" json:"kind"`
	Aliases []string          `yaml:"aliases" json:"-"` // Last.fm / agent synonyms
	Names   map[string]string `yaml:"names" json:"-"`   // display name per locale = web tag.<slug>
}

type Vocab struct {
	entries []VocabEntry
	byKey   map[string]int // normKey(slug or alias) → index into entries
	bySlug  map[string]int
}

// normKey makes lookups case, '-', '_' and spacing insensitive.
func normKey(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("-", " ", "_", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

func ParseVocabulary(b []byte) (*Vocab, error) {
	var entries []VocabEntry
	if err := yaml.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("vocabulary: %w", err)
	}
	v := &Vocab{entries: entries, byKey: map[string]int{}, bySlug: map[string]int{}}
	for i, e := range entries {
		if !slugRE.MatchString(e.Slug) {
			return nil, fmt.Errorf("vocabulary: bad slug %q", e.Slug)
		}
		if !kinds[e.Kind] {
			return nil, fmt.Errorf("vocabulary: %s: unknown kind %q", e.Slug, e.Kind)
		}
		if _, dup := v.bySlug[e.Slug]; dup {
			return nil, fmt.Errorf("vocabulary: duplicate slug %q", e.Slug)
		}
		for _, l := range locales {
			if strings.TrimSpace(e.Names[l]) == "" {
				return nil, fmt.Errorf("vocabulary: %s: missing name for %s", e.Slug, l)
			}
		}
		v.bySlug[e.Slug] = i
		v.byKey[normKey(e.Slug)] = i
	}
	// Aliases second, so "an alias equals another entry's slug" is detected
	// regardless of file order. An alias equal to the entry's own slug is harmless.
	for i, e := range entries {
		for _, a := range e.Aliases {
			k := normKey(a)
			if j, ok := v.byKey[k]; ok {
				if j == i {
					continue
				}
				return nil, fmt.Errorf("vocabulary: alias %q of %s is already used by %s", a, e.Slug, entries[j].Slug)
			}
			v.byKey[k] = i
		}
	}
	return v, nil
}

// Vocabulary returns the embedded vocabulary.yaml, parsed once. The shipped
// file is validated by TestVocabularyLookup, so a parse error is a build bug.
var Vocabulary = sync.OnceValue(func() *Vocab {
	v, err := ParseVocabulary(vocabularyYAML)
	if err != nil {
		panic(err)
	}
	return v
})

func (v *Vocab) All() []VocabEntry { return v.entries }

func (v *Vocab) Lookup(raw string) (VocabEntry, bool) {
	i, ok := v.byKey[normKey(raw)]
	if !ok {
		return VocabEntry{}, false
	}
	return v.entries[i], true
}

// SearchWords returns the words under which a tag must be searchable: for a
// vocabulary tag its slug and every locale name, otherwise just the name.
func SearchWords(name string) []string {
	e, ok := Vocabulary().Lookup(name)
	if !ok || e.Slug != name {
		return []string{name}
	}
	out := []string{e.Slug}
	for _, l := range locales {
		out = append(out, e.Names[l])
	}
	return out
}
