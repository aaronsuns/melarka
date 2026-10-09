package lyrics

import (
	"context"
	"errors"
	"math"
	"net/url"
	"strconv"
	"strings"
)

// LRCLIB asks lrclib.net (https://lrclib.net/docs), an open lyrics database:
// GET /api/get?track_name=&artist_name=&album_name=&duration= for an exact
// signature, then GET /api/search?track_name=&artist_name= when that answers
// any 4xx.
// Fixtures in testdata/ follow the shape of a real response recorded with
// curl from https://lrclib.net/api/get?track_name=Faded&artist_name=Alan+Walker&duration=212.
type LRCLIB struct {
	HTTP    HTTPDoer // default: a client with a 10 s timeout
	BaseURL string   // default "https://lrclib.net"
}

// lrclibSearchMax caps how many search results become candidates.
const lrclibSearchMax = 5

type lrclibItem struct {
	ID           int64   `json:"id"`
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  *string `json:"plainLyrics"`
	SyncedLyrics *string `json:"syncedLyrics"`
}

func (*LRCLIB) Name() string { return "lrclib" }

func (l *LRCLIB) base() string {
	if l.BaseURL == "" {
		return "https://lrclib.net"
	}
	return strings.TrimSuffix(l.BaseURL, "/")
}

func (l *LRCLIB) Search(ctx context.Context, q Query) ([]Candidate, error) {
	if strings.TrimSpace(q.Title) == "" {
		return nil, nil
	}
	// /api/get needs an artist; without a duration it picks an arbitrary
	// version of the song, so it's only worth asking with both.
	if q.Artist != "" && q.DurationS > 0 {
		v := url.Values{"track_name": {q.Title}, "artist_name": {q.Artist}, "duration": {strconv.Itoa(q.DurationS)}}
		if q.Album != "" {
			v.Set("album_name", q.Album)
		}
		var it lrclibItem
		err := getJSON(ctx, l.HTTP, "GET", l.base()+"/api/get?"+v.Encode(), nil, nil, &it)
		switch {
		case err == nil:
			if c, ok := it.candidate(); ok {
				return []Candidate{c}, nil
			}
			return nil, nil
		case !clientError(err): // any 4xx (404, a 400 over an odd signature…) → try search
			return nil, err
		}
	}
	v := url.Values{"track_name": {q.Title}}
	if q.Artist != "" {
		v.Set("artist_name", q.Artist)
	}
	var items []lrclibItem
	if err := getJSON(ctx, l.HTTP, "GET", l.base()+"/api/search?"+v.Encode(), nil, nil, &items); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var out []Candidate
	for _, it := range items {
		if len(out) == lrclibSearchMax {
			break
		}
		if c, ok := it.candidate(); ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// candidate turns an item into a Candidate, preferring synced lyrics;
// instrumental and empty items yield none.
func (it lrclibItem) candidate() (Candidate, bool) {
	if it.Instrumental {
		return Candidate{}, false
	}
	c := Candidate{Source: "lrclib", ExternalID: strconv.FormatInt(it.ID, 10), Title: it.TrackName, Artist: it.ArtistName,
		DurationS: int(math.Round(it.Duration))}
	switch {
	case it.SyncedLyrics != nil && strings.TrimSpace(*it.SyncedLyrics) != "":
		c.Synced, c.Text = true, *it.SyncedLyrics
	case it.PlainLyrics != nil && strings.TrimSpace(*it.PlainLyrics) != "":
		c.Text = *it.PlainLyrics
	default:
		return Candidate{}, false
	}
	return c, true
}
