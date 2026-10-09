package lyrics

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/mozillazg/go-pinyin"
)

var (
	brackets = regexp.MustCompile(`\([^)]*\)|（[^）]*）|\[[^\]]*\]|【[^】]*】`)
	pyArgs   = pinyin.NewArgs()
)

// Norm reduces a title or artist to a comparable key: bracketed parts
// ("(Live)", "【MV】") dropped, every Han character replaced by its toneless
// pinyin (so traditional and simplified spellings agree), and everything
// else reduced to lowercase letters and digits.
func Norm(s string) string {
	s = brackets.ReplaceAllString(s, " ")
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			if py := pinyin.LazyPinyin(string(r), pyArgs); len(py) > 0 {
				b.WriteString(py[0]) // 鄧 and 邓 both → "deng"
			}
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// similar: equal, or one contains the other and the shorter is ≥ 60 % of the longer.
func similar(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return a == b || (strings.Contains(b, a) && len(a)*10 >= len(b)*6)
}

// Match reports whether candidate c is lyrics for the track q describes: a
// similar normalized title, durations within ±3 s when both are known, and
// an agreeing artist — or, when the artist spellings can't be compared (e.g.
// "Teresa Teng" vs 邓丽君), agreeing durations instead.
func Match(q Query, c Candidate) bool {
	if c.Title == "" {
		return true
	}
	both := q.DurationS > 0 && c.DurationS > 0
	if both && abs(q.DurationS-c.DurationS) > 3 {
		return false
	}
	if !similar(Norm(q.Title), Norm(c.Title)) {
		return false
	}
	qa, ca := Norm(q.Artist), Norm(c.Artist)
	return qa == "" || ca == "" || strings.Contains(ca, qa) || strings.Contains(qa, ca) || both
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Rank sorts cs in place: synced first, then the duration closest to the
// track's (unknown last; a different version is the usual reason lyrics run
// early or late), then provider order (sources not in order go last), then
// original order.
func Rank(q Query, cs []Candidate, order []string) {
	pos := func(src string) int {
		if i := slices.Index(order, src); i >= 0 {
			return i
		}
		return len(order)
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Synced != cs[j].Synced {
			return cs[i].Synced
		}
		if di, dj := durationGap(q.DurationS, cs[i].DurationS), durationGap(q.DurationS, cs[j].DurationS); di != dj {
			return di < dj
		}
		return pos(cs[i].Source) < pos(cs[j].Source)
	})
}

// durationGap is how far a candidate's duration is from the track's, in
// seconds; unknownGap when either is unknown.
func durationGap(track, cand int) int {
	if track <= 0 || cand <= 0 {
		return unknownGap
	}
	return abs(track - cand)
}

const unknownGap = 1 << 30

// broadDurationS is how far a broad match's duration may be off.
const broadDurationS = 20

// broadDurationOK: durations within ±20 s, or not comparable — one unknown,
// or the track ≥ 1.5× the candidate (a video with a long intro or talk).
func broadDurationOK(q Query, c Candidate) bool {
	if q.DurationS <= 0 || c.DurationS <= 0 || q.DurationS*2 >= c.DurationS*3 {
		return true
	}
	return abs(q.DurationS-c.DurationS) <= broadDurationS
}

// titleScore rates how alike two normalized titles are, 0..1: 1 equal; one
// containing the other (the shorter at least 4 letters, so "ai" doesn't sit
// inside every pinyin string) 0.5..1 by length ratio; else the edit-distance
// similarity.
func titleScore(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(a) >= 4 && strings.Contains(b, a) {
		return 0.5 + 0.5*float64(len(a))/float64(len(b))
	}
	ra, rb := []rune(a), []rune(b)
	return 1 - float64(levenshtein(ra, rb))/float64(max(len(ra), len(rb)))
}

func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// MatchBroad is the admin's "broad search" match, for songs strict matching
// can't find: any Match, or a title that is alike (titleScore ≥ 0.5) with
// durations within ±20 s (see broadDurationOK) — by any artist.
func MatchBroad(q Query, c Candidate) bool {
	if Match(q, c) {
		return true
	}
	return broadDurationOK(q, c) && titleScore(Norm(q.Title), Norm(c.Title)) >= 0.5
}

func artistAgrees(q Query, c Candidate) bool {
	qa, ca := Norm(q.Artist), Norm(c.Artist)
	return qa != "" && ca != "" && (strings.Contains(ca, qa) || strings.Contains(qa, ca))
}

// RankBroad sorts broad matches in place: strict matches first (in Rank's
// order: synced, closest duration, provider), then the agreeing artist, the more alike title, the closer
// duration (unknown last), synced, and provider order.
func RankBroad(q Query, cs []Candidate, order []string) {
	pos := func(src string) int {
		if i := slices.Index(order, src); i >= 0 {
			return i
		}
		return len(order)
	}
	type key struct {
		strict, artist bool
		title          float64
		dur, pos       int
		synced         bool
	}
	qt := Norm(q.Title)
	ks := make([]key, len(cs))
	for i := range cs {
		c := cs[i]
		d := durationGap(q.DurationS, c.DurationS)
		ks[i] = key{Match(q, c), artistAgrees(q, c), titleScore(qt, Norm(c.Title)), d, pos(c.Source), c.Synced}
	}
	idx := make([]int, len(cs))
	for i := range idx {
		idx[i] = i
	}
	less := func(a, b key) bool {
		switch {
		case a.strict != b.strict:
			return a.strict
		case a.strict: // both strict: Rank's order
			if a.synced != b.synced {
				return a.synced
			}
			if a.dur != b.dur {
				return a.dur < b.dur
			}
			return a.pos < b.pos
		case a.artist != b.artist:
			return a.artist
		case a.title != b.title:
			return a.title > b.title
		case a.dur != b.dur:
			return a.dur < b.dur
		case a.synced != b.synced:
			return a.synced
		}
		return a.pos < b.pos
	}
	sort.SliceStable(idx, func(i, j int) bool { return less(ks[idx[i]], ks[idx[j]]) })
	sorted := make([]Candidate, len(cs))
	for i, k := range idx {
		sorted[i] = cs[k]
	}
	copy(cs, sorted)
}
