package artwork

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/aaronsuns/lark-server/internal/lyrics"
)

// ITunes asks Apple's iTunes Search API (official, no key):
//
//	GET {BaseURL}/search?term=<title artist>&entity=song&media=music&limit=10
//	→ results[].{trackId, trackName, artistName, trackTimeMillis, artworkUrl100}
//
// artworkUrl100 ends in /100x100bb.jpg; the same path with /1000x1000bb.jpg
// serves 1000 px. The answer's Content-Type is text/javascript, which GetJSON
// ignores. Fixture: testdata/itunes_search.json (2026-10-01, 甜蜜蜜 / 邓丽君).
type ITunes struct {
	HTTP    lyrics.HTTPDoer // default: a client with a 10 s timeout
	BaseURL string          // default "https://itunes.apple.com"
}

var itunesSize = regexp.MustCompile(`/\d+x\d+bb\.(?:jpg|png)$`)

func (*ITunes) Name() string { return "itunes" }

func (p *ITunes) Find(ctx context.Context, q lyrics.Query) ([]Found, error) {
	if strings.TrimSpace(q.Title) == "" {
		return nil, nil
	}
	base := strings.TrimSuffix(p.BaseURL, "/")
	if base == "" {
		base = "https://itunes.apple.com"
	}
	v := url.Values{"term": {lyrics.SearchTerm(q)}, "entity": {"song"}, "media": {"music"}, "limit": {"10"}}
	var r struct {
		Results []struct {
			TrackID         int64  `json:"trackId"`
			TrackName       string `json:"trackName"`
			ArtistName      string `json:"artistName"`
			TrackTimeMillis int    `json:"trackTimeMillis"`
			ArtworkURL100   string `json:"artworkUrl100"`
		} `json:"results"`
	}
	if err := lyrics.GetJSON(ctx, p.HTTP, "GET", base+"/search?"+v.Encode(), nil, nil, &r); err != nil {
		return nil, fmt.Errorf("itunes search: %w", err)
	}
	var out []Found
	for _, x := range r.Results {
		if !itunesSize.MatchString(x.ArtworkURL100) {
			continue
		}
		out = append(out, Found{Source: "itunes", ExternalID: strconv.FormatInt(x.TrackID, 10), Title: x.TrackName,
			Artist: x.ArtistName, DurationS: (x.TrackTimeMillis + 500) / 1000,
			URL: itunesSize.ReplaceAllString(x.ArtworkURL100, "/1000x1000bb.jpg"), Stream: -1})
	}
	return out, nil
}
