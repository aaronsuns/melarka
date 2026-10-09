package ytdlp

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// noiseWord matches, case-insensitively, any of the noise tokens that mark a
// bracketed chunk of a video title as junk rather than meaningful content
// (e.g. "(feat. X)" is kept, "(Official Music Video)" is not). The ASCII
// tokens are matched as whole words (\b...\b) so they don't fire on a
// substring inside an unrelated word — e.g. "live" must not match inside
// "Oliver". \b doesn't work for the CJK tokens (every CJK rune is a
// non-word character as far as Go's regexp package is concerned, so \b
// around one would never be satisfied), so those stay plain substring
// matches.
var noiseWord = regexp.MustCompile(`(?i)\b(?:mv|m/v|official|lyrics?|lyric video|audio|hd|hq|4k|1080p|live)\b|官方|歌词|动态歌词|高音质|无损|完整版|现场`)

// Each bracket style is matched (and, if its content is noise, removed)
// independently: yt-dlp titles mix ASCII and full-width/ideographic
// brackets freely and we must not let one style's content leak past its own
// delimiters.
var noiseBrackets = []*regexp.Regexp{
	regexp.MustCompile(`【([^【】]*)】`),
	regexp.MustCompile(`\[([^\[\]]*)\]`),
	regexp.MustCompile(`\(([^()]*)\)`),
	regexp.MustCompile(`（([^（）]*)）`),
}

func stripNoiseBrackets(s string) string {
	for _, re := range noiseBrackets {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			sub := re.FindStringSubmatch(m)
			if len(sub) > 1 && noiseWord.MatchString(sub[1]) {
				return ""
			}
			return m
		})
	}
	return s
}

var whitespaceRe = regexp.MustCompile(`\s+`)

// edgeCutset are the separator characters trimmed off both ends of a title
// once noise brackets are gone (e.g. a leftover "- " from "Title - [MV]").
const edgeCutset = "-_|｜ "

func collapseAndTrim(s string) string {
	s = whitespaceRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	s = strings.Trim(s, edgeCutset)
	return strings.TrimSpace(s)
}

// Artist/title separators. The dash and full-width-pipe forms take
// "everything after the separator" as the title; the quote-wrap forms
// ("A「T」" / "A《T》") take only what is inside the closing bracket, so any
// trailing text after it (e.g. a trailing "高音质" that isn't itself
// bracketed) is discarded.
//
// The dash form requires whitespace on both sides of the dash
// (\s+[-–—]\s+, not \s*): without it, an artist name that itself contains a
// bare hyphen (e.g. "Jay-Z", "AC-DC") would get split on its own internal
// hyphen instead of (or before) the real "artist - title" separator.
var (
	dashSplitRe = regexp.MustCompile(`^(.+?)\s+[-–—]\s+(.+)$`)
	cornerRe    = regexp.MustCompile(`^(.+?)「(.+?)」`)
	bookRe      = regexp.MustCompile(`^(.+?)《(.+?)》`)
	pipeSplitRe = regexp.MustCompile(`^(.+?)\s*｜\s*(.+)$`)
)

func splitArtistTitle(s string) (artist, title string, ok bool) {
	for _, re := range []*regexp.Regexp{dashSplitRe, cornerRe, bookRe, pipeSplitRe} {
		m := re.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		a, t := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
		if a != "" && t != "" {
			return a, t, true
		}
	}
	return "", "", false
}

// channelSuffixRe strips a trailing auto-channel marker so "Yiruma - Topic"
// becomes "Yiruma".
var channelSuffixRe = regexp.MustCompile(`(?i)\s*(-\s*Topic|官方频道|Official|VEVO)\s*$`)

func cleanChannel(channel string) string {
	return strings.TrimSpace(channelSuffixRe.ReplaceAllString(channel, ""))
}

// CleanTitle turns a raw yt-dlp title (and its channel name, used as the
// artist fallback) into a clean (title, artist) pair for tagging the
// downloaded file. See the noise-bracket, whitespace, and split rules above.
// It is the name persisted for a download, so it stays conservative (Ruling
// Q28: it equals CleanTitleV2); the film/drama-context and A【B】 heuristics
// in hints.go only ever feed extra lyrics searches.
func CleanTitle(title, channel string) (cleanTitle, artist string) {
	s := collapseAndTrim(stripNoiseBrackets(title))
	if a, t, ok := splitArtistTitle(s); ok {
		return t, cleanArtist(a, true)
	}
	return s, CleanArtist(cleanChannel(channel))
}

// CleanTitleV2 is the (title, artist) downloads were tagged with from the
// artist cleanup on: today's CleanTitle, frozen here so the missing-lyrics
// list can tell an automatic name from an edited one even if CleanTitle
// changes later.
func CleanTitleV2(title, channel string) (cleanTitle, artist string) {
	s := collapseAndTrim(stripNoiseBrackets(title))
	if a, t, ok := splitArtistTitle(s); ok {
		return t, cleanArtist(a, true)
	}
	return s, CleanArtist(cleanChannel(channel))
}

// CleanTitleV1 is CleanTitle before CleanArtist existed: the (title, artist)
// downloads were tagged with until then. The one-time artist fix-up compares
// a download's stored override against it to tell an untouched name from one
// an admin edited.
func CleanTitleV1(title, channel string) (cleanTitle, artist string) {
	s := stripNoiseBrackets(title)
	s = collapseAndTrim(s)
	if a, t, ok := splitArtistTitle(s); ok {
		return t, a
	}
	return s, cleanChannel(channel)
}

// artistFiller are words a video title wraps round the singer's name
// ("布仁巴雅尔原唱的歌曲《天边》", "#张信哲 深情演绎《信仰》"), longest first so
// "原唱的歌曲" goes whole rather than leaving "的歌曲" behind.
var artistFiller = []string{"原唱的歌曲", "原唱歌曲", "深情演绎", "深情演繹", "的歌曲", "原唱", "演唱", "演绎", "演繹", "献唱", "獻唱", "翻唱"}

// edgeTagRe is a 【…】 tag at either end of an artist ("【纯享】#张信哲").
var edgeTagRe = regexp.MustCompile(`^\s*【[^【】]*】|【[^【】]*】\s*$`)

// artistEdge are the separators trimmed off an artist's ends once filler is
// gone: spaces and full-width punctuation only — ASCII "-", "_", ":", ","
// and "|" can be part of a real name ("-M-", "Sha-").
const artistEdge = " ：｜，"

// prefixSep may follow a leading filler word ("原唱：刀郎", "献唱 韩红").
const prefixSep = " ：:·"

func trimArtistEdge(s string) string {
	s = strings.Trim(s, artistEdge)
	// A hashtag before a Han name ("#张信哲"), never "#1 Dads".
	if r, ok := strings.CutPrefix(s, "#"); ok {
		if first, _ := utf8.DecodeRuneInString(r); unicode.Is(unicode.Han, first) {
			s = r
		}
	}
	return s
}

// CleanArtist strips filler (原唱的歌曲, 演唱, 深情演绎, 翻唱 …, also with a
// 的 straight after it), 【…】 tags and a '#' before a Han name from the ends
// of an artist name — only as affixes: a trailing filler word may be glued to
// the name ("布仁巴雅尔原唱的歌曲"), a leading one must be followed by a
// separator ("原唱：刀郎", "献唱 韩红"), so a name that merely starts with one
// ("原唱者乐队") is left whole. A bare trailing 的 stays ("小鱼的"): only an
// artist split off a title loses it (see CleanTitle). A cut that would leave
// nothing, or only 的, is not made.
func CleanArtist(s string) string { return cleanArtist(s, false) }

// cleanArtist is CleanArtist; fromTitle also drops a bare trailing 的, which
// in a title ("刀郎的《西海情歌》") belongs to the phrase, not the name.
func cleanArtist(s string, fromTitle bool) string {
	cur := strings.TrimSpace(s)
	keep := func(r string) bool { r = trimArtistEdge(r); return r != "" && r != "的" }
	for {
		prev := cur
		if r := edgeTagRe.ReplaceAllString(cur, ""); keep(r) {
			cur = trimArtistEdge(r)
		}
		for _, f := range artistFiller {
			for _, suf := range []string{f + "的", f} {
				if r, ok := strings.CutSuffix(cur, suf); ok && keep(r) {
					cur = trimArtistEdge(r)
					break
				}
			}
			if r, ok := strings.CutPrefix(cur, f); ok && r != "" && strings.ContainsRune(prefixSep, []rune(r)[0]) {
				if r = strings.TrimLeft(r, prefixSep); keep(r) {
					cur = trimArtistEdge(r)
				}
			}
		}
		if fromTitle {
			if r, ok := strings.CutSuffix(cur, "的"); ok && keep(r) {
				cur = trimArtistEdge(r)
			}
		}
		if cur == prev {
			return cur
		}
	}
}

// unsafeNameChar matches characters that can't appear in a filesystem path
// component: the usual reserved set plus ASCII control characters, and "%",
// which yt-dlp would parse as an output-template field once the name is in -o.
var unsafeNameChar = regexp.MustCompile(`[/\\:*?"<>|%\x00-\x1f]`)

// safeNameMaxBytes caps a cleaned name well under common filesystem limits
// (255 bytes) while leaving room for a numeric dedupe suffix and extension.
const safeNameMaxBytes = 120

// SafeName turns s into a filesystem-safe path component: any invalid UTF-8
// is dropped, reserved characters become "_", surrounding dots/spaces are
// trimmed (so a name that was only dots, e.g. "...", can't collide with "."
// or ".."), and the result is capped at safeNameMaxBytes bytes on a rune
// boundary. Truncation can itself leave a new trailing dot/space (cutting
// into what was, say, ". " followed by more text), so the dot/space trim
// and the empty check both run again after truncating. An empty result
// falls back to "untitled".
func SafeName(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.TrimSpace(s)
	s = unsafeNameChar.ReplaceAllString(s, "_")
	s = strings.Trim(s, ". ")
	if s == "" {
		return "untitled"
	}
	s = truncateUTF8(s, safeNameMaxBytes)
	s = strings.Trim(s, ". ")
	if s == "" {
		return "untitled"
	}
	return s
}

// truncateUTF8 cuts s to at most maxBytes bytes, backing off to the nearest
// preceding rune boundary (utf8.RuneStart) if the naive cut landed inside a
// multi-byte rune, so the result is always valid UTF-8. s is assumed to
// already be valid UTF-8 (SafeName runs strings.ToValidUTF8 first).
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	i := maxBytes
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}
