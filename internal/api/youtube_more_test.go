package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// nVideosJSON is a flat ytsearch answer with n distinct videos.
func nVideosJSON(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"_type":"playlist","entries":[`)
	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"id":"mv%09d","title":"T%d","channel":"C","duration":60}`, i, i)
	}
	b.WriteString("]}")
	return []byte(b.String())
}

// searchN answers ytsearch<N>: with N videos and the playlist search with none.
func searchN(args []string) ([]byte, error) {
	last := args[len(args)-1]
	if strings.Contains(last, "results?search_query") {
		return []byte(`{"_type":"playlist","entries":[]}`), nil
	}
	var n int
	if _, err := fmt.Sscanf(last, "ytsearch%d:", &n); err != nil {
		return nil, err
	}
	return nVideosJSON(n), nil
}

func decodeSearch(t *testing.T, b []byte) ytdlp.SearchResult {
	t.Helper()
	var res ytdlp.SearchResult
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return res
}

func TestYouTubeSearchFirstPageSaysMore(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	s.YT.Runner.(*fakeYTRunner).outFor = searchN
	r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=more", nil)
	if r.StatusCode != 200 {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	if res := decodeSearch(t, b); len(res.Videos) != 10 || !res.More {
		t.Fatalf("a full first page must offer more: %s", b)
	}
	// Two results: nothing more to show.
	s2, ts2 := newTestServer(t)
	tok2 := loginAs(t, s2, "alice", "member")
	_, b = do(t, ts2, tok2, "GET", "/api/v1/youtube/search?q=x", nil)
	if strings.Contains(string(b), `"more"`) {
		t.Fatalf("a short page must not offer more: %s", b)
	}
}

func TestYouTubeSearchMorePages(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	fr := s.YT.Runner.(*fakeYTRunner)
	fr.outFor = searchN
	for _, c := range []struct {
		n, want  string
		videos   int
		wantMore bool
	}{
		{"20", "ytsearch20:更多", 20, true},
		{"15", "ytsearch20:更多", 20, true}, // rounded up to a page: one cache entry per page
		{"40", "ytsearch40:更多", 40, true},
		{"50", "ytsearch50:更多", 50, false},
		{"999", "ytsearch50:更多", 50, false}, // bounded
	} {
		before := fr.callCount()
		r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=%E6%9B%B4%E5%A4%9A&n="+c.n, nil)
		if r.StatusCode != 200 {
			t.Fatalf("n=%s: %d %s", c.n, r.StatusCode, b)
		}
		res := decodeSearch(t, b)
		if len(res.Videos) != c.videos || res.More != c.wantMore || len(res.Playlists) != 0 || !strings.Contains(string(b), `"playlists":[]`) {
			t.Fatalf("n=%s: %d videos more=%v %s", c.n, len(res.Videos), res.More, b)
		}
		fr.mu.Lock()
		calls := fr.calls[before:]
		fr.mu.Unlock()
		if c.n == "15" || c.n == "999" { // cached: same key as 20 / 50
			if len(calls) != 0 {
				t.Fatalf("n=%s: want a cache hit, got %v", c.n, calls)
			}
			continue
		}
		// A later page runs the video search alone (one process, one token).
		if len(calls) != 1 || calls[0][len(calls[0])-1] != c.want {
			t.Fatalf("n=%s: calls %v, want one %q", c.n, calls, c.want)
		}
	}
	for _, bad := range []string{"0", "-5", "abc"} {
		if r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=x&n="+bad, nil); r.StatusCode != 400 {
			t.Fatalf("n=%s: %d %s", bad, r.StatusCode, b)
		}
	}
}

// A later page shares the limiter with every other search: identical
// concurrent "show more" taps collapse into one yt-dlp run.
func TestYouTubeSearchMoreSingleflight(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	fr := s.YT.Runner.(*fakeYTRunner)
	fr.outFor = searchN
	fr.delay = 150 * time.Millisecond
	concurrentSearch(t, ts, tok, 5, func(int) string { return "same&n=30" })
	if n := fr.callCount(); n != 1 {
		t.Fatalf("5 identical page-3 requests: want 1 runner call, got %d", n)
	}
}

func TestYouTubePlaylistEntries(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	fr := s.YT.Runner.(*fakeYTRunner)
	fr.outFor = func(args []string) ([]byte, error) {
		return []byte(`{"_type":"playlist","id":"PLtestlist0001","title":"L","playlist_count":120,"entries":[` +
			`{"id":"p1_0000000x","title":"Song One","channel":"A","duration":180,"url":"https://www.youtube.com/watch?v=p1_0000000x"},` +
			`{"id":"p2_0000000x","title":"[Private video]","channel":"","duration":0,"url":""},` +
			`{"id":"p3_0000000x","title":"Song Three","uploader":"C","duration":95,"url":"https://www.youtube.com/watch?v=p3_0000000x"}]}`), nil
	}
	for range 2 {
		r, b := do(t, ts, tok, "GET", "/api/v1/youtube/playlist/entries?list=PLtestlist0001", nil)
		if r.StatusCode != 200 {
			t.Fatalf("%d %s", r.StatusCode, b)
		}
		var res struct {
			Videos []ytdlp.Video `json:"videos"`
			Count  int           `json:"count"`
			More   bool          `json:"more"`
		}
		if err := json.Unmarshal(b, &res); err != nil {
			t.Fatal(err)
		}
		if len(res.Videos) != 2 || res.Videos[1].Channel != "C" || res.Videos[1].DurationS != 95 || res.Count != 120 || !res.More {
			t.Fatalf("%s", b)
		}
	}
	fr.mu.Lock()
	calls := fr.calls
	fr.mu.Unlock()
	want := "--playlist-end 50 https://www.youtube.com/playlist?list=PLtestlist0001"
	if len(calls) != 1 || !strings.HasSuffix(strings.Join(calls[0], " "), want) {
		t.Fatalf("want one cached flat-playlist run ending %q, got %v", want, calls)
	}
	// The next page asks for more (bounded at 200, the download cap).
	for n, end := range map[string]string{"100": "100", "51": "100", "5000": "200"} {
		do(t, ts, tok, "GET", "/api/v1/youtube/playlist/entries?list=PLtestlist0001&n="+n, nil)
		fr.mu.Lock()
		last := strings.Join(fr.calls[len(fr.calls)-1], " ")
		fr.mu.Unlock()
		if !strings.Contains(last, "--playlist-end "+end+" ") {
			t.Fatalf("n=%s: %s", n, last)
		}
	}
	for _, bad := range []string{"list=RDxyzxyzxyzxyz", "list=", "list=--exec=x", "list=short", "list=PLtestlist0001&n=0", "list=PLtestlist0001&n=x"} {
		if r, b := do(t, ts, tok, "GET", "/api/v1/youtube/playlist/entries?"+bad, nil); r.StatusCode != 400 {
			t.Fatalf("%q: %d %s", bad, r.StatusCode, b)
		}
	}
}

// A playlist's entries never share a cache entry with its info or a search.
func TestYouTubePlaylistEntriesCacheKeyDistinct(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	fr := s.YT.Runner.(*fakeYTRunner)
	fr.outFor = func(args []string) ([]byte, error) {
		return []byte(`{"_type":"playlist","id":"abcdefghijklm","title":"L","playlist_count":247,"entries":[` +
			`{"id":"p1_0000000x","title":"Song One","channel":"A","duration":180}]}`), nil
	}
	if r, b := do(t, ts, tok, "GET", "/api/v1/youtube/playlist?list=abcdefghijklm", nil); r.StatusCode != 200 {
		t.Fatalf("info: %d %s", r.StatusCode, b)
	}
	r, b := do(t, ts, tok, "GET", "/api/v1/youtube/playlist/entries?list=abcdefghijklm", nil)
	if r.StatusCode != 200 || !strings.Contains(string(b), "p1_0000000x") {
		t.Fatalf("entries after info: %d %s", r.StatusCode, b)
	}
	if n := fr.callCount(); n != 2 {
		t.Fatalf("want 2 runs (info, entries), got %d", n)
	}
}
