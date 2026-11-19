package ytdlp

import (
	"net/url"
	"regexp"
	"strings"
)

// Source is where a video lives. The id's shape says which: a YouTube id is
// 11 characters, a Bilibili id is "BV" plus ten alphanumerics (12), so the two
// never collide and no table needs a source column.
type Source string

const (
	SourceYouTube  Source = "youtube"
	SourceBilibili Source = "bilibili"
)

// Sources is every source Melarka knows, in display order.
var Sources = []Source{SourceYouTube, SourceBilibili}

// bilibiliIDRe is the shape of a Bilibili video id (BV…).
var bilibiliIDRe = regexp.MustCompile(`^BV[0-9A-Za-z]{10}$`)

// IsBilibiliID reports whether id has the shape of a Bilibili video id.
func IsBilibiliID(id string) bool { return bilibiliIDRe.MatchString(id) }

// IsAnyVideoID reports whether id is a video id of any source. The shared
// pipeline (download, preview, thumbnails, watch page) uses it; channels,
// episodes and recommendations keep IsVideoID, which stays YouTube-only.
func IsAnyVideoID(id string) bool { return IsVideoID(id) || IsBilibiliID(id) }

// SourceOf returns the source of video id, from its shape.
func SourceOf(id string) (Source, bool) {
	switch {
	case IsVideoID(id):
		return SourceYouTube, true
	case IsBilibiliID(id):
		return SourceBilibili, true
	}
	return "", false
}

// bilibiliHosts are the allow-listed Bilibili hosts (also in allowedHosts).
// b23.tv short links are deliberately absent: Melarka never follows a
// redirect to a host it has not checked.
var bilibiliHosts = map[string]bool{
	"bilibili.com":       true,
	"www.bilibili.com":   true,
	"m.bilibili.com":     true,
	"space.bilibili.com": true,
}

// SourceOfURL returns the source a URL on the allow-list belongs to.
func SourceOfURL(rawURL string) (Source, bool) {
	u, err := ValidURL(rawURL)
	if err != nil {
		return "", false
	}
	pu, err := url.Parse(u)
	if err != nil {
		return "", false
	}
	if bilibiliHosts[strings.ToLower(pu.Hostname())] {
		return SourceBilibili, true
	}
	return SourceYouTube, true
}

// bilibiliVideoID returns the BV id of a bilibili.com/video/<id> URL (part 1:
// ?p= is ignored), or "".
func bilibiliVideoID(u *url.URL) string {
	h := strings.ToLower(u.Hostname())
	if h == "space.bilibili.com" || !bilibiliHosts[h] {
		return ""
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) == 2 && segs[0] == "video" && IsBilibiliID(segs[1]) {
		return segs[1]
	}
	return ""
}

// IsBilibiliListURL reports whether rawURL names a Bilibili list worth
// importing: favourites, a collection or series, an uploader's videos, or a
// play list (medialist, /list/).
func IsBilibiliListURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if !bilibiliHosts[h] {
		return false
	}
	p := "/" + strings.Trim(u.Path, "/")
	if h == "space.bilibili.com" {
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		return len(segs) >= 2
	}
	return strings.HasPrefix(p, "/medialist/") || strings.HasPrefix(p, "/list/")
}
