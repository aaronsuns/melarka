package ytdlp

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Search hints: heuristics for reading a messy video title (the film/drama a
// song comes from, "A【B】" shapes, filler words). They guess, so they only
// feed extra lyrics searches; nothing here is used by CleanTitle or stored.

func hasWordRune(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0
}

func hasHan(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0
}

// workRe is the film/drama/anime a song belongs to: 电影「大话西游」插曲,
// 《西游记》主题曲, 电视剧《还珠格格》 — a quoted name with a kind word before
// it or a 插曲/主题曲/片尾曲… word after it (a bare 《…》 is usually the song).
const (
	workKind   = `电视连续剧|電視連續劇|电视剧|電視劇|电影|電影|动画片|動畫片|动画|動畫|动漫|動漫|剧集|劇集|网剧|網劇`
	workSuffix = `插曲|主题曲|主題曲|主题歌|主題歌|片尾曲|片头曲|片頭曲|片中曲|推广曲|推廣曲|宣传曲|宣傳曲|原声|原聲`
)

var workRe = regexp.MustCompile(`(` + workKind + `)?\s*(?:《[^》]*》|「[^」]*」)\s*(?:` + workKind + `)?\s*(同名)?(` + workSuffix + `)?`)

// StripWork removes the work a song comes from (see workRe), leaving the
// rest of s in place. "…同名主题曲" (the song is named after the work) is
// left alone, and so is a suffix-only quote right after a singer
// ("刘欢《好汉歌》电视剧主题曲": that quote is the song); a suffix-only quote
// counts as the work only at the start or after another bracket.
func StripWork(s string) string {
	var b strings.Builder
	last := 0
	for _, ix := range workRe.FindAllStringSubmatchIndex(s, -1) {
		kind, same, suffix := ix[2] >= 0, ix[4] >= 0, ix[6] >= 0
		before := strings.TrimSpace(s[:ix[0]])
		atStart := before == "" || strings.HasSuffix(before, "】") || strings.HasSuffix(before, "」") || strings.HasSuffix(before, "》")
		if same || !(kind || (suffix && atStart)) {
			continue
		}
		b.WriteString(s[last:ix[0]])
		b.WriteString(" ")
		last = ix[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// leadingQuoteRe is "《Song》rest" / "「Song」rest" / "【Song】rest".
var leadingQuoteRe = regexp.MustCompile(`^(?:《([^》]+)》|「([^」]+)」|【([^【】]+)】)\s*(.*)$`)

// filler marks text that names a category, chart, language, instrument,
// episode, tag or work rather than a singer or a song (儿歌, 钢琴曲, 抖音热歌,
// 2023最火歌曲, 第1集, 粤语, 怀旧金曲, 官方, KTV, 主题曲 …).
var filler = regexp.MustCompile(`(?i)` + workKind + `|` + workSuffix + `|儿歌|兒歌|童谣|童謠|助眠|睡前|故事|胎教|钢琴|鋼琴|古筝|古箏|二胡|琵琶|吉他|小提琴|大提琴|萨克斯|薩克斯|葫芦丝|葫蘆絲|笛子|唢呐|嗩吶|口琴|轻音乐|輕音樂|纯音乐|純音樂|伴奏|音乐|音樂|歌曲|金曲|情歌|群星|热歌|熱歌|最火|抖音|合集|精选|精選|串烧|串燒|经典|經典|老歌|怀旧|懷舊|官方|无损|無損|高音质|高音質|高清|修复|修復|中文字幕|字幕|中字|纯享|純享|首播|首发|首發|新歌|翻唱|合唱|现场|現場|版|粤语|粵語|国语|國語|英文|英语|英語|日语|日語|韩语|韓語|闽南语|閩南語|台语|台語|客家|\bbgm\b|\bdj\b|\bktv\b|\blyrics?\b|\bmv\b|\bcover\b|\blive\b|\bremix\b|^\d|\d{4}|第\s*\d+\s*[集期话話]`)

// notSongName are 【…】 tags that are not a song.
var notSongName = regexp.MustCompile(`^(?:纯享|純享|字幕|中字|中文字幕|首播|首发|首發|新歌|热歌|熱歌|抖音|经典|經典|伴奏|纯音乐|純音樂|合唱|翻唱|现场版|現場版|完整版|高清|无损|無損|动态歌词|動態歌詞|官方|单曲|單曲|歌曲|音乐|音樂)$`)

// Filler reports whether s reads as a category/chart/language/instrument/
// episode/tag/work word rather than a singer or song name (see filler).
func Filler(s string) bool { return filler.MatchString(s) || noiseWord.MatchString(s) }

// singerList reads s as singer names: no brackets or quotes, at most 40
// runes, no filler words; ok is false otherwise.
func singerList(s string) ([]string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > 40 || strings.ContainsAny(s, "【】《》「」[]()（）|｜") ||
		dashSplitRe.MatchString(s) || Filler(s) {
		return nil, false
	}
	ss := SplitSingers(cleanArtist(s, true))
	return ss, len(ss) > 0
}

// bracketPairRe is "left【bracket】rest" with no brackets or quotes in left.
var bracketPairRe = regexp.MustCompile(`^([^【】《》「」\[\]()（）|｜]*?)\s*【([^【】]+)】(.*)$`)

// trailingLatin is a space-separated tail with no Han in it: the English
// translation after a Chinese name ("一生所愛 Love In A Life Time").
var trailingLatin = regexp.MustCompile(`\s+[^\p{Han}]*[A-Za-z][^\p{Han}]*$`)

// BracketPair splits a raw video title shaped "left【bracket】rest" (noise
// brackets and the work removed first) into its three parts, the bracket
// reduced to its Han part. It decides nothing: either side may be the song
// or the singer (盧冠廷 莫文蔚【一生所愛】, 后来【刘若英】), or filler (粤语【千千阙歌】),
// so it is only for generating extra lyrics searches, never for names that
// are stored or shown. ok is false without a Han bracket.
func BracketPair(raw string) (left, bracket, rest string, ok bool) {
	s := collapseAndTrim(StripWork(collapseAndTrim(stripNoiseBrackets(raw))))
	m := bracketPairRe.FindStringSubmatch(s)
	if m == nil || !hasHan(m[2]) {
		return "", "", "", false
	}
	bracket = collapseAndTrim(trailingLatin.ReplaceAllString(strings.TrimSpace(m[2]), ""))
	return collapseAndTrim(m[1]), bracket, collapseAndTrim(m[3]), bracket != ""
}

// Singers is singerList for callers outside the package: s as singer
// names, or ok false when it is filler or not name-shaped.
func Singers(s string) ([]string, bool) { return singerList(s) }

var singerSep = regexp.MustCompile(`\s*[、,，&＆]\s*`)

// SplitSingers splits "盧冠廷 莫文蔚", "张学友&汤宝如", "刘德华、陈慧琳" or
// "周华健和齐豫" into names. Spaces split only between Han names ("Alan
// Walker" is one name), and 和 only when every piece is at least two
// characters ("和平" is one name).
func SplitSingers(s string) []string {
	var out []string
	for _, p := range singerSep.Split(s, -1) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		parts := []string{p}
		if f := strings.Fields(p); len(f) > 1 && allHan(f) {
			parts = f
		}
		for _, q := range parts {
			if q == "和" { // "周华健 和 齐豫"
				continue
			}
			if pieces := strings.Split(q, "和"); len(pieces) > 1 && allHan([]string{q}) && minRunes(pieces) >= 2 {
				out = append(out, pieces...)
				continue
			}
			out = append(out, q)
		}
	}
	return out
}

func allHan(ss []string) bool {
	for _, s := range ss {
		for _, r := range s {
			if !unicode.Is(unicode.Han, r) && r != '·' {
				return false
			}
		}
	}
	return true
}

func minRunes(ss []string) int {
	n := -1
	for _, s := range ss {
		if c := utf8.RuneCountInString(s); n < 0 || c < n {
			n = c
		}
	}
	return n
}

// WorkRest strips noise brackets and then the work a song comes from
// (StripWork) from a raw title; ok only when a work was removed.
func WorkRest(raw string) (string, bool) {
	s := collapseAndTrim(stripNoiseBrackets(raw))
	w := collapseAndTrim(StripWork(s))
	return w, w != s
}

// LeadingQuote splits "《X》rest" / "「X」rest" / "【X】rest" into X and rest.
func LeadingQuote(s string) (x, rest string, ok bool) {
	m := leadingQuoteRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", "", false
	}
	return collapseAndTrim(m[1] + m[2] + m[3]), collapseAndTrim(m[4]), true
}

// fillerUnits are counts and lengths that go with filler ("30分钟", "100首").
var fillerUnits = regexp.MustCompile(`\d+|分钟|分鐘|小时|小時|首|集|期`)

// FillerOnly reports whether s holds nothing but filler (合集, 儿歌合集 30分钟,
// 中文字幕, 经典老歌, KTV, 1993 …) once filler words, numbers and their units
// and punctuation are gone; "你的故事" is not.
func FillerOnly(s string) bool {
	s = filler.ReplaceAllString(s, " ")
	s = noiseWord.ReplaceAllString(s, " ")
	s = fillerUnits.ReplaceAllString(s, " ")
	return !hasWordRune(s)
}
