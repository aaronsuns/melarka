package lastfm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// SimilarTrack is one track.getSimilar answer; Match is Last.fm's 0..1 similarity.
type SimilarTrack struct {
	Title, Artist string
	Match         float64
}

// SimilarArtist is one artist.getSimilar answer.
type SimilarArtist struct {
	Name  string
	Match float64
}

// flexFloat accepts 0.5 and "0.5".
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("lastfm: bad match %q", s)
	}
	*f = flexFloat(v)
	return nil
}

// oneOrMany decodes a Last.fm list that is an array, a single object, or empty.
func oneOrMany[T any](raw json.RawMessage) ([]T, error) {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0 || string(raw) == "null" || string(raw) == `""`:
		return nil, nil
	case raw[0] == '[':
		var out []T
		err := json.Unmarshal(raw, &out)
		return out, err
	default:
		var one T
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, err
		}
		return []T{one}, nil
	}
}

// TrackSimilar asks track.getSimilar for up to limit tracks like artist – track.
func (c *Client) TrackSimilar(ctx context.Context, artist, track string, limit int) ([]SimilarTrack, error) {
	body, err := c.call(ctx, url.Values{"method": {"track.getsimilar"}, "artist": {artist}, "track": {track}, "limit": {strconv.Itoa(limit)}})
	if err != nil {
		return nil, err
	}
	var r struct {
		Similar *struct {
			Track json.RawMessage `json:"track"`
		} `json:"similartracks"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.Similar == nil {
		return nil, errors.New("lastfm: response has no similartracks")
	}
	type raw struct {
		Name   string    `json:"name"`
		Match  flexFloat `json:"match"`
		Artist struct {
			Name string `json:"name"`
		} `json:"artist"`
	}
	rs, err := oneOrMany[raw](r.Similar.Track)
	if err != nil {
		return nil, errors.New("lastfm: bad similartracks")
	}
	out := []SimilarTrack{}
	for _, t := range rs {
		if t.Name != "" && t.Artist.Name != "" {
			out = append(out, SimilarTrack{Title: t.Name, Artist: t.Artist.Name, Match: float64(t.Match)})
		}
	}
	return out, nil
}

// ArtistSimilar asks artist.getSimilar for up to limit artists like artist.
func (c *Client) ArtistSimilar(ctx context.Context, artist string, limit int) ([]SimilarArtist, error) {
	body, err := c.call(ctx, url.Values{"method": {"artist.getsimilar"}, "artist": {artist}, "limit": {strconv.Itoa(limit)}})
	if err != nil {
		return nil, err
	}
	var r struct {
		Similar *struct {
			Artist json.RawMessage `json:"artist"`
		} `json:"similarartists"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.Similar == nil {
		return nil, errors.New("lastfm: response has no similarartists")
	}
	type raw struct {
		Name  string    `json:"name"`
		Match flexFloat `json:"match"`
	}
	rs, err := oneOrMany[raw](r.Similar.Artist)
	if err != nil {
		return nil, errors.New("lastfm: bad similarartists")
	}
	out := []SimilarArtist{}
	for _, a := range rs {
		if a.Name != "" {
			out = append(out, SimilarArtist{Name: a.Name, Match: float64(a.Match)})
		}
	}
	return out, nil
}
