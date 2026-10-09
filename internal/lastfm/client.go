// Package lastfm fetches Last.fm top tags and maps them into Lark's tag
// vocabulary.
package lastfm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	HTTP     HTTPDoer // default http.DefaultClient with a 15 s timeout
	BaseURL  string   // default "https://ws.audioscrobbler.com/2.0/"
	APIKey   string
	Interval time.Duration // minimum spacing between request starts; default 250ms (= 4/s)

	mu   sync.Mutex
	last time.Time
}

type Tag struct {
	Name  string
	Count int
}

// ErrNotFound is Last.fm error 6 (unknown artist or track).
var ErrNotFound = errors.New("lastfm: not found")

// ErrUnavailable wraps errors that say Last.fm (or the network, or our API
// key) is unusable right now, as opposed to an error about one request:
// transport failures, HTTP 429/502/503/504, and Last.fm errors 10 (invalid
// key), 11 (offline), 16 (temporarily unavailable), 26 (key suspended) and
// 29 (rate limited).
var ErrUnavailable = errors.New("lastfm: unavailable")

func unavailableCode(code int) bool {
	switch code {
	case 10, 11, 16, 26, 29:
		return true
	}
	return false
}

func unavailableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

var defaultHTTP = &http.Client{Timeout: 15 * time.Second}

func (c *Client) interval() time.Duration {
	if c.Interval <= 0 {
		return 250 * time.Millisecond
	}
	return c.Interval
}

// wait blocks until at least interval() has passed since the previous
// request started, or ctx ends.
func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := time.Until(c.last.Add(c.interval())); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.last = time.Now()
	return nil
}

func (c *Client) TrackTopTags(ctx context.Context, artist, track string) ([]Tag, error) {
	return c.get(ctx, url.Values{"method": {"track.gettoptags"}, "artist": {artist}, "track": {track}})
}

func (c *Client) ArtistTopTags(ctx context.Context, artist string) ([]Tag, error) {
	return c.get(ctx, url.Values{"method": {"artist.gettoptags"}, "artist": {artist}})
}

// flexInt accepts 100 and "100".
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("lastfm: bad count %q", s)
	}
	*f = flexInt(n)
	return nil
}

type response struct {
	TopTags *struct {
		Tag json.RawMessage `json:"tag"`
	} `json:"toptags"`
}

type rawTag struct {
	Name  string  `json:"name"`
	Count flexInt `json:"count"`
}

// call runs one Last.fm method (rate limited) and returns the raw JSON body,
// mapping Last.fm's error envelope and HTTP failures to ErrNotFound /
// ErrUnavailable / plain errors. Errors never carry the api_key.
func (c *Client) call(ctx context.Context, q url.Values) ([]byte, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	base := c.BaseURL
	if base == "" {
		base = "https://ws.audioscrobbler.com/2.0/"
	}
	q.Set("autocorrect", "1")
	q.Set("api_key", c.APIKey)
	q.Set("format", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+q.Encode(), nil)
	if err != nil {
		return nil, errors.New("lastfm: bad request")
	}
	doer := c.HTTP
	if doer == nil {
		doer = defaultHTTP
	}
	resp, err := doer.Do(req)
	if err != nil {
		// A *url.Error carries the full request URL, api_key included.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("%w: request failed: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: reading response: %w", ErrUnavailable, err)
	}
	var r struct {
		Error   int    `json:"error"`
		Message string `json:"message"`
	}
	jerr := json.Unmarshal(body, &r)
	if jerr == nil && r.Error == 6 {
		return nil, ErrNotFound
	}
	if jerr == nil && unavailableCode(r.Error) {
		return nil, fmt.Errorf("%w: error %d: %s", ErrUnavailable, r.Error, r.Message)
	}
	if unavailableStatus(resp.StatusCode) {
		return nil, fmt.Errorf("%w: http %d", ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("lastfm: http %d", resp.StatusCode)
	}
	if jerr != nil {
		return nil, errors.New("lastfm: response is not JSON")
	}
	if r.Error != 0 {
		return nil, fmt.Errorf("lastfm: error %d: %s", r.Error, r.Message)
	}
	return body, nil
}

func (c *Client) get(ctx context.Context, q url.Values) ([]Tag, error) {
	body, err := c.call(ctx, q)
	if err != nil {
		return nil, err
	}
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, errors.New("lastfm: response is not JSON")
	}
	if r.TopTags == nil {
		return nil, errors.New("lastfm: response has no toptags")
	}
	raw := bytes.TrimSpace(r.TopTags.Tag)
	var rts []rawTag
	switch {
	case len(raw) == 0 || string(raw) == "null":
	case raw[0] == '[':
		if err := json.Unmarshal(raw, &rts); err != nil {
			return nil, err
		}
	default:
		var one rawTag
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, err
		}
		rts = []rawTag{one}
	}
	out := make([]Tag, 0, len(rts))
	for _, t := range rts {
		out = append(out, Tag{Name: t.Name, Count: int(t.Count)})
	}
	return out, nil
}
