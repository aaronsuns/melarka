package lyrics

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

var (
	songQuote  = regexp.MustCompile(`《([^》]+)》|「([^」]+)」`)
	anyBracket = regexp.MustCompile(`\([^()]*\)|（[^（）]*）|\[[^\[\]]*\]|【[^【】]*】`)
	hashtag    = regexp.MustCompile(`#(\S+)`)
	// Bare "live", "audio" and "lyrics" are real words in real titles ("Live
	// Forever"); only bracketed "(Live)", "[Audio]" … are noise, and
	// anyBracket already drops those. They go only as part of a phrase.
	asciiNoise  = regexp.MustCompile(`(?i)\b(?:official\s+music\s+video|official\s+lyrics?\s+video|official\s+video|official\s+audio|lyric\s+video|official|mv|m/v|hd|hq|4k|1080p|720p)\b`)
	cjkNoise    = regexp.MustCompile(`动态歌词|動態歌詞|高音质|高音質|无损|無損|完整版|完整|高清|超清|官方|歌词|歌詞|字幕|音质|音質|纯享|純享|现场版|現場版|现场|現場`)
	artistNoise = regexp.MustCompile(`(?i)深情演绎|深情演繹|演唱|演绎|演繹|献唱|獻唱|翻唱|纯享|純享|现场|現場|官方频道|官方頻道|官方|频道|頻道|\bofficial\b|\bvevo\b|-\s*topic\b`)
	spaceRun    = regexp.MustCompile(`\s+`)
	// "Coldplay - Fix You": a dash with spaces round it (never "Fix-You").
	artistDash = regexp.MustCompile(`\s+[-–—]\s+`)
)

const queryEdge = "-_|｜·:： "

func hasHan(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0
}

// hanName: 2–4 Han characters and nothing else — the shape of a Chinese singer's name.
func hanName(s string) bool {
	n := 0
	for _, r := range s {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
		n++
	}
	return n >= 2 && n <= 4
}

func tidy(s string) string {
	return strings.Trim(strings.TrimSpace(spaceRun.ReplaceAllString(s, " ")), queryEdge)
}

// cleanQueryTitle returns the song name a YouTube title most likely carries,
// plus any #hashtags it had (often the singer).
func cleanQueryTitle(s string) (string, []string) {
	var tags []string
	for _, m := range hashtag.FindAllStringSubmatch(s, -1) {
		tags = append(tags, m[1])
	}
	// 《西游记》插曲, 电影「大话西游」 …: the work a song belongs to, not the song.
	s = ytdlp.StripWork(s)
	if m := songQuote.FindStringSubmatch(s); m != nil {
		s = m[1] + m[2]
	}
	s = anyBracket.ReplaceAllString(s, " ")
	s = hashtag.ReplaceAllString(s, " ")
	s = asciiNoise.ReplaceAllString(s, " ")
	s = cjkNoise.ReplaceAllString(s, " ")
	s = tidy(s)
	if hasHan(s) { // "小寶貝 Summer Play Band - My Little Baby": drop the English translation
		for i, r := range s {
			if r == ' ' && hasHan(s[:i]) && !hasHan(s[i:]) &&
				strings.IndexFunc(s[i:], func(r rune) bool { return r < 128 && unicode.IsLetter(r) }) >= 0 {
				s = tidy(s[:i])
				break
			}
		}
	}
	return s, tags
}

// maxHintHan: a longer Han run is a lyric line, not a song title.
const maxHintHan = 14

// hintTitle: a guessed title worth searching — not filler only (合集,
// 中文字幕 …), no brackets left, not a lyric-length line.
func hintTitle(t string) bool {
	n := 0
	for _, r := range t {
		if unicode.Is(unicode.Han, r) {
			n++
		}
	}
	return t != "" && n <= maxHintHan && !ytdlp.FillerOnly(t) && !strings.ContainsAny(t, "【】《》「」[]()（）")
}

// hintSinger reads s as singer names (a leading "- " trimmed) and returns
// the first, or "" when s is filler or not name-shaped.
func hintSinger(s string) string {
	s = strings.TrimLeft(strings.TrimSpace(s), "-–—_ ")
	if ss, ok := ytdlp.Singers(s); ok && utf8.RuneCountInString(ss[0]) <= maxSingerRunes {
		return ss[0]
	}
	return ""
}

// maxSingerRunes: a longer "name" is a phrase or a lyric line.
const maxSingerRunes = 12

// hintPairs are the extra (title, artist) searches a messy title suggests.
// They are guesses, so they only ever add searches (never stored or shown):
//   - the work a song comes from removed (ytdlp.WorkRest), then a leading
//     《X》/「X」/【X】rest in both orders (【一生所爱】卢冠廷, 【张国荣】当年情),
//     or rest alone when X is filler (【粤语】当年情) — any other rest is the
//     base queries' business;
//   - "A【B】rest" (ytdlp.BracketPair) in both orders, B with the singer after
//     the 】 when A is filler (怀旧金曲【千千阙歌】陈慧娴), A when B is filler.
//
// known is the artist to pair with a title found alone. Titles that are
// filler only, still bracketed or lyric-length are dropped, as are pairs
// without an artist.
func hintPairs(raw, known string) [][2]string {
	var out [][2]string
	seen := map[string]bool{}
	add := func(t, a string) {
		t, a = tidy(t), strings.TrimLeft(tidy(a), "-–—_ ")
		k := Norm(t) + "\x00" + Norm(a)
		if hintTitle(t) && a != "" && !ytdlp.Filler(a) && Norm(t) != Norm(a) && !seen[k] {
			seen[k] = true
			out = append(out, [2]string{t, a})
		}
	}
	if rest, ok := ytdlp.WorkRest(raw); ok {
		if x, after, ok := ytdlp.LeadingQuote(rest); ok {
			if ytdlp.FillerOnly(x) || ytdlp.Filler(x) && hintSinger(x) == "" {
				add(after, known)
			} else {
				if a := hintSinger(after); a != "" {
					add(x, a)
				} else {
					add(x, known)
				}
				if a := hintSinger(x); a != "" {
					add(after, a)
				}
			}
		}
	}
	if left, br, rest, ok := ytdlp.BracketPair(raw); ok {
		leftOK := left != "" && !ytdlp.Filler(left)
		brOK := !ytdlp.Filler(br)
		switch {
		case leftOK && brOK:
			add(br, hintSinger(left))
			add(left, hintSinger(br))
		case brOK:
			if a := hintSinger(rest); a != "" {
				add(br, a)
			} else {
				add(br, known)
			}
		case leftOK:
			add(left, known)
		}
	}
	return out
}

func cleanQueryArtist(s string) string {
	s = ytdlp.CleanArtist(s) // 原唱的歌曲, a trailing 的 … — the same rule downloads are tagged with
	s = anyBracket.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "#", " ")
	return tidy(artistNoise.ReplaceAllString(s, " "))
}

// dropArtistPart turns "Coldplay - Fix You" or "Fix You - Coldplay" into
// "Fix You" when the dashed-off part is the artist.
func dropArtistPart(title, artist string) string {
	a := Norm(artist)
	if a == "" {
		return title
	}
	loc := artistDash.FindStringIndex(title)
	if loc == nil {
		return title
	}
	left, right := tidy(title[:loc[0]]), tidy(title[loc[1]:])
	switch {
	case left != "" && right != "" && Norm(left) == a:
		return right
	case left != "" && right != "" && Norm(right) == a:
		return left
	}
	return title
}

// SearchQueries turns q into at most 5 provider queries, best first (the
// first as below; then guesses from the shown and the video title, see
// hintPairs; then the rest as below, at most 3 of those): titles
// cleaned of 电影「…」/《…》插曲/主题曲/片尾曲 wrappers, brackets and noise words, a
// trailing English translation dropped; artists cleaned of brackets, '#' and
// filler words, a leading or trailing "Artist - " part dropped. For a
// channel artist: (title, singer found in the title), (title alone),
// (title, channel) when a singer was found, else (title, channel), (title
// alone) — title alone never first while an artist is known. Never returns an empty slice; never
// changes q. The cleaned values are for searching only — the title shown is
// never changed.
func SearchQueries(q Query) []Query {
	title, tags := cleanQueryTitle(q.Title)
	if title == "" {
		title = strings.TrimSpace(q.Title)
	}
	artist := cleanQueryArtist(q.Artist)
	if artist == "" {
		artist = strings.TrimSpace(q.Artist)
	}
	title = dropArtistPart(title, artist)
	singer := ""
	if f := strings.Fields(title); len(f) >= 2 {
		last, rest := f[len(f)-1], strings.Join(f[:len(f)-1], " ")
		if hanName(last) && hasHan(rest) && (q.ChannelArtist || artist == "" || Norm(last) == Norm(artist)) {
			title, singer = rest, last
		}
	}
	if singer == "" && (q.ChannelArtist || artist == "") && len(tags) > 0 && hanName(tags[0]) {
		singer = tags[0]
	}
	var base [][2]string
	seen := map[string]bool{}
	add := func(t, a string) {
		k := Norm(t) + "\x00" + Norm(a)
		if t == "" || seen[k] || len(base) == 3 {
			return
		}
		seen[k] = true
		base = append(base, [2]string{t, a})
	}
	// Title alone is never asked first while any artist is known: Match takes
	// any similar title for an empty artist, so a short title (当, Hello)
	// would grab another song's lyrics.
	if q.ChannelArtist {
		if singer != "" {
			add(title, singer)
			add(title, "")
			add(title, artist) // the channel name, last
		} else {
			add(title, artist)
			add(title, "")
		}
	} else {
		if artist == "" {
			artist = singer
		}
		add(title, artist)
	}
	if len(base) == 0 {
		return []Query{q}
	}
	// "A【B】" pairs from the shown title and the video's own title go right
	// after the first query (which keeps the rule above), before the rest.
	known := artist
	if known == "" || ytdlp.Filler(known) {
		known = cleanQueryArtist(q.Channel)
	}
	pairs := [][2]string{base[0]}
	for _, raw := range []string{q.Title, q.VideoTitle} {
		for _, p := range hintPairs(raw, known) {
			k := Norm(p[0]) + "\x00" + Norm(p[1])
			if p[0] == "" || p[1] == "" || seen[k] {
				continue // never title alone from a guess
			}
			seen[k] = true
			pairs = append(pairs, p)
		}
	}
	pairs = append(pairs, base[1:]...)
	if len(pairs) > maxQueries {
		pairs = pairs[:maxQueries]
	}
	out := make([]Query, len(pairs))
	for i, p := range pairs {
		v := q
		v.Title, v.Artist = p[0], p[1]
		out[i] = v
	}
	return out
}

// maxQueries bounds the searches one lookup asks each provider (all within
// the one lookup budget).
const maxQueries = 5

// BuildQuery describes a visible track the way providers are asked about it.
// ErrNotFound when not visible.
func BuildQuery(ctx context.Context, db *sql.DB, lib *library.Store, trackID int64) (Query, error) {
	t, err := lib.Track(ctx, 0, trackID)
	if errors.Is(err, library.ErrNotFound) {
		return Query{}, ErrNotFound
	}
	if err != nil {
		return Query{}, err
	}
	path, _, err := lib.TrackPath(ctx, trackID)
	if errors.Is(err, library.ErrNotFound) {
		return Query{}, ErrNotFound
	}
	if err != nil {
		return Query{}, err
	}
	q := Query{TrackID: trackID, Title: t.Title, Artist: t.Artist, Album: t.Album,
		DurationS: int((t.DurationMS + 500) / 1000), Path: path}
	var channel string
	err = db.QueryRowContext(ctx, `SELECT channel FROM downloads WHERE track_id=? AND channel!='' ORDER BY id DESC LIMIT 1`, trackID).Scan(&channel)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return Query{}, err
	default:
		a := Norm(q.Artist)
		q.ChannelArtist = a != "" && strings.Contains(Norm(channel), a)
		q.Channel = channel
	}
	err = db.QueryRowContext(ctx, `SELECT title FROM downloads WHERE track_id=? AND status='done' AND title!='' ORDER BY id DESC LIMIT 1`, trackID).Scan(&q.VideoTitle)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Query{}, err
	}
	return q, nil
}
