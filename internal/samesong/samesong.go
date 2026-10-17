// Package samesong tells whether a YouTube video is a song the library
// already has: by its cleaned title and singer, compared as lyrics.Norm keys
// (pinyin, so 邓丽君 and 鄧麗君 agree). Recommendations leave such videos out;
// a download asks first.
package samesong

import (
	"strings"

	"github.com/aaronsuns/lark-server/internal/lyrics"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// Video is a YouTube video's cleaned title and artist, and its raw title and
// channel, each normalised for comparing.
type Video struct{ Title, Artist, Raw, CleanTitle string }

func FromVideo(title, channel string) Video {
	t, a := ytdlp.CleanTitle(title, channel)
	return Video{Title: lyrics.Norm(t), Artist: lyrics.Norm(a), Raw: lyrics.Norm(title + " " + channel), CleanTitle: t}
}

// ArtistMatches: a library song's normalised artist is the video's cleaned
// artist, or appears anywhere in its raw title and channel.
func (v Video) ArtistMatches(normArtist string) bool {
	return normArtist == v.Artist || (normArtist != "" && strings.Contains(v.Raw, normArtist))
}

// Same reports whether the library song (title, artist) is this video's song.
func (v Video) Same(title, artist string) bool {
	return v.Title != "" && lyrics.Norm(title) == v.Title && v.ArtistMatches(lyrics.Norm(artist))
}
