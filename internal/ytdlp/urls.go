package ytdlp

import (
	"net/url"
	"regexp"
	"strings"
)

// videoIDRe is the shape of a YouTube video id. Channel (UC…), playlist
// (PL…, RD…, OLAK…) and tab ids that flat-playlist output can also carry
// are all longer, so this one check separates real videos from the rest.
var videoIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// IsVideoID reports whether id has the shape of a YouTube video id.
func IsVideoID(id string) bool { return videoIDRe.MatchString(id) }

// WatchURL is the canonical watch page for video id.
func WatchURL(id string) string { return "https://www.youtube.com/watch?v=" + id }

// pathIDPrefixes are the youtube.com path shapes that carry a video id as
// their second segment.
var pathIDPrefixes = []string{"shorts", "live", "embed"}

// VideoIDFromURL returns the video id rawURL names — watch?v=<id>,
// youtu.be/<id>, /shorts/<id>, /live/<id> or /embed/<id> — or "" when it
// names no single video (a playlist, channel, or anything malformed).
func VideoIDFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	var id string
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case strings.EqualFold(u.Hostname(), "youtu.be"):
		id = segs[0]
	case u.Query().Get("v") != "":
		id = u.Query().Get("v")
	case len(segs) == 2:
		for _, p := range pathIDPrefixes {
			if segs[0] == p {
				id = segs[1]
			}
		}
	}
	if !IsVideoID(id) {
		return ""
	}
	return id
}

// listIDRe is the shape of a playlist id we let reach argv or a URL.
var listIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{12,64}$`)

// ListIDFromURL returns the list= value of rawURL when it has a plausible
// playlist-id shape, else "".
func ListIDFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	id := u.Query().Get("list")
	if !listIDRe.MatchString(id) {
		return ""
	}
	return id
}

// IsMixID reports whether id is a YouTube auto-generated mix (RD…), which is
// endless and personalised and so not a playlist worth offering.
func IsMixID(id string) bool { return strings.HasPrefix(id, "RD") }

// PlaylistURL is the canonical page of playlist id.
func PlaylistURL(id string) string { return "https://www.youtube.com/playlist?list=" + id }

// thumbnailBase is where ThumbnailURL points. SetThumbnailBase changes it
// once, at startup, before anything reads it.
var thumbnailBase = "https://i.ytimg.com/vi"

// SetThumbnailBase points ThumbnailURL at base (LARK_YOUTUBE_COVER_URL: the
// e2e tests serve the thumbnails locally, so no page they load reaches the
// Internet). An empty base keeps the default.
func SetThumbnailBase(base string) {
	if base = strings.TrimRight(base, "/"); base != "" {
		thumbnailBase = base
	}
}

// ThumbnailURL is a video's canonical cover: hqdefault exists for every
// video (maxresdefault doesn't), whatever link or search result it came from.
func ThumbnailURL(id string) string { return thumbnailBase + "/" + id + "/hqdefault.jpg" }
