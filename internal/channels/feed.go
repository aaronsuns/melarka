// Package channels follows YouTube channels without an account:
// their public RSS feeds are read every poll interval, new episodes are
// downloaded once for all followers into channels.root, and old ones are
// deleted by the retention sweeper. Episodes are never music tracks.
package channels

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aaronsuns/lark-server/internal/buildinfo"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// DefaultFeedURL is YouTube's public per-channel feed (?channel_id=UC…).
const DefaultFeedURL = "https://www.youtube.com/feeds/videos.xml"

const (
	maxFeedBytes   = 4 << 20 // 4 MiB; a real feed of 15 entries is ~30 KB
	feedTimeout    = 20 * time.Second
	maxDescription = 4000 // runes kept per episode
)

// userAgent identifies Melarka (and its version) to YouTube's feed server.
var userAgent = buildinfo.UserAgent()

// ErrFeedNotFound: YouTube answered 404 (the channel is gone or has no feed).
var ErrFeedNotFound = errors.New("channels: feed not found")

// FeedEntry is one video in a channel's feed.
type FeedEntry struct {
	VideoID, Title, Description string
	Published                   time.Time
	// Short: the feed links the entry as /shorts/<id> (how YouTube marks Shorts there).
	Short bool
}

// Feed is a channel's feed: its title and newest entries (YouTube lists 15).
type Feed struct {
	Title   string
	Entries []FeedEntry
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

type atomEntry struct {
	VideoID   string     `xml:"http://www.youtube.com/xml/schemas/2015 videoId"`
	Title     string     `xml:"http://www.w3.org/2005/Atom title"`
	Links     []atomLink `xml:"http://www.w3.org/2005/Atom link"`
	Published string     `xml:"http://www.w3.org/2005/Atom published"`
	Group     struct {
		Description string `xml:"http://search.yahoo.com/mrss/ description"`
	} `xml:"http://search.yahoo.com/mrss/ group"`
}

type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Title   string      `xml:"http://www.w3.org/2005/Atom title"`
	Entries []atomEntry `xml:"http://www.w3.org/2005/Atom entry"`
}

// ParseFeed reads a YouTube channel feed. Entries without a valid video id
// or publish time are skipped; anything that isn't an Atom feed (an HTML
// error page) is an error, never an empty feed.
func ParseFeed(r io.Reader) (Feed, error) {
	var f atomFeed
	if err := xml.NewDecoder(r).Decode(&f); err != nil {
		return Feed{}, fmt.Errorf("channels: parse feed: %w", err)
	}
	out := Feed{Title: strings.TrimSpace(f.Title), Entries: []FeedEntry{}}
	for _, e := range f.Entries {
		if !ytdlp.IsVideoID(e.VideoID) {
			continue
		}
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(e.Published))
		if err != nil {
			continue
		}
		fe := FeedEntry{VideoID: e.VideoID, Title: strings.TrimSpace(e.Title), Published: at.UTC(),
			Description: truncate(strings.TrimSpace(e.Group.Description), maxDescription)}
		for _, l := range e.Links {
			if l.Rel == "alternate" && strings.Contains(l.Href, "/shorts/") {
				fe.Short = true
			}
		}
		out.Entries = append(out.Entries, fe)
	}
	return out, nil
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// HTTPDoer is the HTTP client surface (tests inject an httptest server's URL).
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// FeedClient fetches channel feeds.
type FeedClient struct {
	HTTP    HTTPDoer // nil: http.DefaultClient
	BaseURL string   // "": DefaultFeedURL
}

// Fetch reads channelID's feed (20 s timeout, 4 MiB cap). A 404 is
// ErrFeedNotFound; any other non-200 answer is a plain error.
func (c *FeedClient) Fetch(ctx context.Context, channelID string) (Feed, error) {
	if !ytdlp.IsChannelID(channelID) {
		return Feed{}, ytdlp.ErrBadURL
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultFeedURL
	}
	ctx, cancel := context.WithTimeout(ctx, feedTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?channel_id="+channelID, nil)
	if err != nil {
		return Feed{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	doer := c.HTTP
	if doer == nil {
		doer = http.DefaultClient
	}
	resp, err := doer.Do(req)
	if err != nil {
		return Feed{}, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Feed{}, ErrFeedNotFound
	case resp.StatusCode != http.StatusOK:
		return Feed{}, fmt.Errorf("channels: feed answered %d", resp.StatusCode)
	}
	return ParseFeed(io.LimitReader(resp.Body, maxFeedBytes))
}
