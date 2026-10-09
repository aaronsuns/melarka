package lastfm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTrackSimilarRequestAndParse(t *testing.T) {
	c, q := serve(t, "track_similar.json")
	ts, err := c.TrackSimilar(context.Background(), "Teresa Teng", "甜蜜蜜", 5)
	if err != nil {
		t.Fatal(err)
	}
	want := []SimilarTrack{
		{Title: "月亮代表我的心", Artist: "鄧麗君", Match: 1},
		{Title: "恰似你的温柔", Artist: "蔡琴", Match: 0.874},
		{Title: "一剪梅", Artist: "費玉清", Match: 0.51},
	}
	if !reflect.DeepEqual(ts, want) {
		t.Fatalf("%+v", ts)
	}
	wantQ := url.Values{"method": {"track.getsimilar"}, "artist": {"Teresa Teng"}, "track": {"甜蜜蜜"}, "limit": {"5"},
		"autocorrect": {"1"}, "api_key": {"k"}, "format": {"json"}}
	if !reflect.DeepEqual(q(), wantQ) {
		t.Fatalf("query %v", q())
	}
}

func TestTrackSimilarEmptyAndNotFound(t *testing.T) {
	c, _ := serve(t, "track_similar_empty.json")
	ts, err := c.TrackSimilar(context.Background(), "Unknown Band", "x", 5)
	if err != nil || len(ts) != 0 {
		t.Fatalf("%+v %v", ts, err)
	}
	c, _ = serve(t, "track_notfound.json")
	if _, err := c.TrackSimilar(context.Background(), "a", "b", 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestArtistSimilarRequestAndParse(t *testing.T) {
	c, q := serve(t, "artist_similar.json")
	as, err := c.ArtistSimilar(context.Background(), "Teresa Teng", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []SimilarArtist{{Name: "蔡琴", Match: 1}, {Name: "鳳飛飛", Match: 0.812345}}
	if !reflect.DeepEqual(as, want) {
		t.Fatalf("%+v", as)
	}
	if q().Get("method") != "artist.getsimilar" || q().Get("limit") != "3" || q().Get("artist") != "Teresa Teng" {
		t.Fatalf("query %v", q())
	}
}

// Same rate limit, outage classification and key redaction as the tag calls.
func TestSimilarSharesErrorHandling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "busy", 503) }))
	c := &Client{BaseURL: srv.URL + "/", APIKey: "secretkey", Interval: time.Millisecond}
	_, err := c.TrackSimilar(context.Background(), "a", "b", 5)
	srv.Close()
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "secretkey") {
		t.Fatalf("err %v", err)
	}
	_, err = c.ArtistSimilar(context.Background(), "a", 5) // server gone: transport error
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "secretkey") {
		t.Fatalf("err %v", err)
	}
	c2, _ := serve(t, "track_similar.json")
	c2.Interval = 40 * time.Millisecond
	start := time.Now()
	for range 3 {
		c2.TrackSimilar(context.Background(), "a", "b", 5)
	}
	c2.ArtistSimilar(context.Background(), "a", 5)
	if d := time.Since(start); d < 120*time.Millisecond {
		t.Fatalf("4 calls took %v: not rate limited", d)
	}
}
