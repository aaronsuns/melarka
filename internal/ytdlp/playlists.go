package ytdlp

import (
	"context"
	"net/url"
)

// Playlist is one playlist search result; Count is 0 when unknown (search
// results carry no length — PlaylistInfo fills it).
type Playlist struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Channel   string `json:"channel"`
	URL       string `json:"url"` // always PlaylistURL(ID)
	Thumbnail string `json:"thumbnail"`
	Count     int    `json:"count"`
}

// SearchResult is what GET /youtube/search returns; Channels is filled only
// by channel searches and resolves (never by the music search).
type SearchResult struct {
	Videos    []Video    `json:"videos"`
	Playlists []Playlist `json:"playlists"`
	Channels  []Channel  `json:"channels,omitempty"`
	// More: a larger page (GET /youtube/search?n=…) may find further videos.
	More bool `json:"more,omitempty"`
}

// List is a resolved playlist: its metadata and up to max videos. Count is
// YouTube's playlist_count (the full length, whatever max was).
type List struct {
	ID, Title, Channel string
	Count              int
	Videos             []Video
}

// playlistFilter is YouTube's "Playlists" search filter (the sp parameter,
// already percent-encoded once; url.QueryEscape is not applied to it).
const playlistFilter = "EgIQAw%253D%253D"

// SearchPlaylists lists up to 5 playlists matching q.
func (c *Client) SearchPlaylists(ctx context.Context, q string) ([]Playlist, error) {
	clean, err := CleanQuery(q)
	if err != nil {
		return nil, err
	}
	args := append(c.baseArgs(), "--flat-playlist", "-J", "--no-warnings", "--playlist-end", "5",
		"https://www.youtube.com/results?search_query="+url.QueryEscape(clean)+"&sp="+playlistFilter)
	out, err := c.Runner.Output(ctx, args)
	if err != nil {
		return nil, err
	}
	top, _, err := parseTop(out)
	if err != nil {
		return nil, err
	}
	pls := []Playlist{}
	if top.Type != "playlist" {
		return pls, nil
	}
	for _, e := range top.Entries {
		if e.IEKey != "YoutubeTab" {
			continue
		}
		id := ListIDFromURL(e.URL)
		if id == "" || IsMixID(id) {
			continue
		}
		pls = append(pls, Playlist{
			ID: id, Title: e.Title, Channel: entryChannel(e),
			URL: PlaylistURL(id), Thumbnail: lastThumb(e.Thumbnails, e.Thumbnail),
		})
	}
	return pls, nil
}

func lastThumb(ts []thumbnail, fallback string) string {
	if n := len(ts); n > 0 {
		return ts[n-1].URL
	}
	return fallback
}

// PlaylistInfo fetches a playlist's title, channel, cover and full length.
func (c *Client) PlaylistInfo(ctx context.Context, listID string) (Playlist, error) {
	if !listIDRe.MatchString(listID) || IsMixID(listID) {
		return Playlist{}, ErrBadURL
	}
	args := append(c.baseArgs(), "--flat-playlist", "-J", "--no-warnings", "--playlist-end", "1", PlaylistURL(listID))
	out, err := c.Runner.Output(ctx, args)
	if err != nil {
		return Playlist{}, err
	}
	top, _, err := parseTop(out)
	if err != nil {
		return Playlist{}, err
	}
	return Playlist{
		ID: listID, Title: top.Title, Channel: entryChannel(top.ytEntry),
		URL: PlaylistURL(listID), Thumbnail: lastThumb(top.Thumbnails, top.Thumbnail), Count: top.PlaylistCount,
	}, nil
}
