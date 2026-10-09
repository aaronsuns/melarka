package lyrics

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Line is one timed lyric line.
type Line struct {
	TMS  int64  `json:"t_ms"`
	Text string `json:"text"`
}

var (
	timeTag  = regexp.MustCompile(`^\[(\d{1,3}):(\d{1,2})(?:[.:](\d{1,3}))?\]`)
	metaLine = regexp.MustCompile(`^\[([A-Za-z#]+):([^\]]*)\]$`)
)

func normalizeNewlines(s string) string {
	return strings.TrimPrefix(strings.ReplaceAll(s, "\r\n", "\n"), "\ufeff")
}

// ParseLRC returns the timed lines of s sorted by time (one line per time
// tag, [offset:] applied). synced is true when at least three timed lines
// carry text — fewer is treated as plain text.
func ParseLRC(s string) (lines []Line, synced bool) {
	var offset int64
	var out []Line
	for _, raw := range strings.Split(normalizeNewlines(s), "\n") {
		line := strings.TrimSpace(raw)
		if m := metaLine.FindStringSubmatch(line); m != nil {
			if strings.EqualFold(m[1], "offset") {
				offset, _ = strconv.ParseInt(strings.TrimSpace(m[2]), 10, 64)
			}
			continue
		}
		var stamps []int64
		for {
			m := timeTag.FindStringSubmatch(line)
			if m == nil {
				break
			}
			stamps = append(stamps, stampMS(m))
			line = strings.TrimSpace(line[len(m[0]):])
		}
		for _, ms := range stamps {
			out = append(out, Line{TMS: max(0, ms-offset), Text: line})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TMS < out[j].TMS })
	n := 0
	for _, l := range out {
		if l.Text != "" {
			n++
		}
	}
	return out, n >= 3
}

func stampMS(m []string) int64 {
	mins, _ := strconv.ParseInt(m[1], 10, 64)
	secs, _ := strconv.ParseInt(m[2], 10, 64)
	var frac int64
	if f := m[3]; f != "" {
		frac, _ = strconv.ParseInt(f, 10, 64)
		frac *= [...]int64{0, 100, 10, 1}[len(f)]
	}
	return (mins*60+secs)*1000 + frac
}

// PlainText strips LRC metadata and time tags, dropping empty lines.
func PlainText(s string) string {
	var keep []string
	for _, raw := range strings.Split(normalizeNewlines(s), "\n") {
		line := strings.TrimSpace(raw)
		if metaLine.MatchString(line) {
			continue
		}
		for m := timeTag.FindString(line); m != ""; m = timeTag.FindString(line) {
			line = strings.TrimSpace(line[len(m):])
		}
		if line != "" {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n")
}
