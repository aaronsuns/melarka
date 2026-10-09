package lastfm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func serve(t *testing.T, name string) (*Client, func() url.Values) {
	t.Helper()
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Write(fixture(t, name))
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL + "/2.0/", APIKey: "k", Interval: time.Millisecond}, func() url.Values { return got }
}

func TestTrackTopTagsRequestAndParse(t *testing.T) {
	c, q := serve(t, "track_toptags.json")
	ts, err := c.TrackTopTags(context.Background(), "Teresa Teng", "甜蜜蜜")
	if err != nil || len(ts) != 6 || ts[1] != (Tag{"Mandopop", 71}) {
		t.Fatalf("%+v %v", ts, err)
	}
	want := url.Values{"method": {"track.gettoptags"}, "artist": {"Teresa Teng"}, "track": {"甜蜜蜜"}, "autocorrect": {"1"}, "api_key": {"k"}, "format": {"json"}}
	if !reflect.DeepEqual(q(), want) {
		t.Fatalf("query %v", q())
	}
}

func TestArtistTopTagsRequestAndStringCounts(t *testing.T) {
	c, q := serve(t, "artist_toptags.json")
	ts, err := c.ArtistTopTags(context.Background(), "Teresa Teng")
	if err != nil || len(ts) != 3 || ts[0].Count != 100 || ts[1].Count != 60 || ts[2].Count != 55 {
		t.Fatalf("%+v %v", ts, err)
	}
	want := url.Values{"method": {"artist.gettoptags"}, "artist": {"Teresa Teng"}, "autocorrect": {"1"}, "api_key": {"k"}, "format": {"json"}}
	if !reflect.DeepEqual(q(), want) {
		t.Fatalf("query %v", q())
	}
}

func TestSingleTagObject(t *testing.T) {
	c, _ := serve(t, "single_tag.json")
	ts, err := c.TrackTopTags(context.Background(), "B", "A")
	if err != nil || len(ts) != 1 || ts[0] != (Tag{"jazz", 100}) {
		t.Fatalf("%+v %v", ts, err)
	}
}

func TestEmptyTagList(t *testing.T) {
	c, _ := serve(t, "track_empty.json")
	ts, err := c.TrackTopTags(context.Background(), "Y", "X")
	if err != nil || len(ts) != 0 {
		t.Fatalf("%+v %v", ts, err)
	}
}

func TestNotFound(t *testing.T) {
	c, _ := serve(t, "track_notfound.json")
	_, err := c.TrackTopTags(context.Background(), "a", "b")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestOtherErrorsArePlain(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"500":  func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) },
		"html": func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>oops</html>")) },
		"err10": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"error":10,"message":"Invalid API key"}`))
		},
	}
	for name, h := range cases {
		srv := httptest.NewServer(h)
		c := &Client{BaseURL: srv.URL + "/", APIKey: "secretkey", Interval: time.Millisecond}
		_, err := c.TrackTopTags(context.Background(), "a", "b")
		srv.Close()
		if err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: err %v", name, err)
		}
		if strings.Contains(err.Error(), "secretkey") {
			t.Fatalf("%s: error leaks the api key: %v", name, err)
		}
	}
}

func TestTransportErrorRedactsKey(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL + "/"
	srv.Close() // connection refused
	c := &Client{BaseURL: base, APIKey: "secretkey", Interval: time.Millisecond}
	_, err := c.TrackTopTags(context.Background(), "a", "b")
	if err == nil || strings.Contains(err.Error(), "secretkey") {
		t.Fatalf("err %v", err)
	}
}

func TestRateLimit(t *testing.T) {
	c, _ := serve(t, "track_toptags.json")
	c.Interval = 50 * time.Millisecond
	start := time.Now()
	for i := 0; i < 5; i++ {
		if _, err := c.TrackTopTags(context.Background(), "a", "b"); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 200*time.Millisecond {
		t.Fatalf("5 calls took %v, want >= 200ms", d)
	}
}

func TestDefaultInterval(t *testing.T) {
	if (&Client{}).interval() != 250*time.Millisecond {
		t.Fatal("default interval")
	}
}

func TestWaitHonoursContext(t *testing.T) {
	c := &Client{Interval: time.Hour}
	if err := c.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.wait(ctx); err == nil {
		t.Fatal("wait ignored ctx")
	}
}

// Errors that say "Last.fm (or the network) is down" wrap ErrUnavailable so the
// worker backs off without blaming the track; errors about one answer don't.
func TestUnavailableClassification(t *testing.T) {
	cases := map[string]struct {
		h    http.HandlerFunc
		down bool
	}{
		"500":  {func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) }, false},
		"html": {func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>oops</html>")) }, false},
		"err8": {func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"error":8,"message":"Operation failed"}`))
		}, false},
		"count": {func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"toptags":{"tag":[{"name":"x","count":"lots"}]}}`))
		}, false},
		"503": {func(w http.ResponseWriter, r *http.Request) { http.Error(w, "busy", 503) }, true},
		"429": {func(w http.ResponseWriter, r *http.Request) { http.Error(w, "slow down", 429) }, true},
		"err10": {func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"error":10,"message":"Invalid API key"}`))
		}, true},
		"err11": {func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"error":11,"message":"Service Offline"}`))
		}, true},
		"err29": {func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"error":29,"message":"Rate limit exceeded"}`))
		}, true},
	}
	for name, tc := range cases {
		srv := httptest.NewServer(tc.h)
		c := &Client{BaseURL: srv.URL + "/", APIKey: "k", Interval: time.Millisecond}
		_, err := c.TrackTopTags(context.Background(), "a", "b")
		srv.Close()
		if err == nil || errors.Is(err, ErrUnavailable) != tc.down {
			t.Errorf("%s: err %v, unavailable=%v want %v", name, err, errors.Is(err, ErrUnavailable), tc.down)
		}
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL + "/"
	srv.Close()
	c := &Client{BaseURL: base, APIKey: "k", Interval: time.Millisecond}
	if _, err := c.TrackTopTags(context.Background(), "a", "b"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("connection refused: err %v not unavailable", err)
	}
}
