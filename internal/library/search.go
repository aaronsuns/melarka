// Package library builds and queries the FTS5 search index for tracks.
package library

import (
	"strings"
	"unicode"

	"github.com/mozillazg/go-pinyin"
)

var pyArgs = pinyin.NewArgs()

func isHan(r rune) bool { return unicode.Is(unicode.Han, r) }

// spaceHan puts spaces around every Han character. FTS5's unicode61 tokenizer
// treats a run of Han characters as one token, so without this "丽君" could
// never match "邓丽君".
func spaceHan(s string) string {
	var b strings.Builder
	for _, r := range s {
		if isHan(r) {
			b.WriteByte(' ')
			b.WriteRune(r)
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SearchDoc builds the indexed text for a track: the fields themselves plus,
// for each field containing Han characters, its full pinyin ("denglijun") and
// pinyin initials ("dlj").
func SearchDoc(fields ...string) string {
	var parts []string
	for _, f := range fields {
		f = strings.ToLower(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		parts = append(parts, spaceHan(f))
		py := pinyin.LazyPinyin(f, pyArgs) // one entry per Han character
		if len(py) == 0 {
			continue
		}
		var initials strings.Builder
		for _, p := range py {
			initials.WriteByte(p[0])
		}
		parts = append(parts, strings.Join(py, ""), initials.String())
		// The pinyin library reads 乐 as "le" (happy); in 音乐 and 乐队 it is
		// "yue". Index the "yue" reading too so "yinyue" finds 音乐.
		if strings.ContainsRune(f, '乐') {
			var alt, altInitials strings.Builder
			for _, r := range f {
				if isHan(r) {
					p := pinyin.LazyPinyin(string(r), pyArgs)
					if r == '乐' {
						p = []string{"yue"}
					}
					if len(p) == 0 {
						continue
					}
					alt.WriteString(p[0])
					altInitials.WriteByte(p[0][0])
				}
			}
			parts = append(parts, alt.String(), altInitials.String())
		}
	}
	return strings.Join(parts, " ")
}

// FTSQuery turns user input into an FTS5 expression: Han runs become exact
// phrases of single characters, other words become prefix matches, and all
// terms must match. Returns "" when q has no searchable text.
func FTSQuery(q string) string {
	var terms []string
	for _, word := range strings.Fields(strings.ToLower(q)) {
		var han, latin strings.Builder
		flushLatin := func() {
			if latin.Len() > 0 {
				terms = append(terms, `"`+latin.String()+`"*`)
				latin.Reset()
			}
		}
		flushHan := func() {
			if han.Len() > 0 {
				terms = append(terms, `"`+strings.TrimSpace(han.String())+`"`)
				han.Reset()
			}
		}
		for _, r := range word {
			switch {
			case isHan(r):
				flushLatin()
				han.WriteRune(r)
				han.WriteByte(' ')
			case unicode.IsLetter(r) || unicode.IsDigit(r):
				flushHan()
				latin.WriteRune(r)
			default: // punctuation, quotes, FTS operators: drop
				flushHan()
				flushLatin()
			}
		}
		flushHan()
		flushLatin()
	}
	return strings.Join(terms, " AND ")
}
