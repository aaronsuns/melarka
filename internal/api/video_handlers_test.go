package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/preview"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// liveAndVideoJSON is a search answer with one live stream and one video.
const liveAndVideoJSON = `{"_type":"playlist","entries":[
	{"id":"live_000000","title":"直播","channel":"C","live_status":"is_live","url":"https://www.youtube.com/watch?v=live_000000"},
	{"id":"v1_0000000x","title":"测试视频","channel":"测试频道","duration":120,"url":"https://www.youtube.com/watch?v=v1_0000000x"}
]}`

// doRaw sends body as it is (a JSON string, or nothing when empty).
func doRaw(t *testing.T, ts *httptest.Server, token, method, path, body string) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, ts.URL+path, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func videoSearches(t *testing.T, s *Server, userID int64) (n int, seed string) {
	t.Helper()
	s.Library.DB.QueryRow(`SELECT COUNT(*), COALESCE(MAX(seed_video_id),'') FROM video_history WHERE user_id=? AND kind='search'`, userID).Scan(&n, &seed)
	return
}

func TestVideoRoutesForMembersAndPerUser(t *testing.T) {
	s, ts := newTestServer(t)
	withPreviews(t, s, "UC0e5c4U67Vm6sAVK0vxN3Uw")
	kid := loginAs(t, s, "kid", "member")
	admin := loginAs(t, s, "boss", "admin")
	resp, b := do(t, ts, kid, "GET", "/api/v1/videos/search?q=%E5%88%98%E7%BF%94&record=1", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("search %d %s", resp.StatusCode, b)
	}
	var res struct{ Videos []ytdlp.Video }
	json.Unmarshal(b, &res)
	if len(res.Videos) == 0 || res.Videos[0].Thumbnail != "/api/v1/videos/v1_0000000x/thumbnail" {
		t.Fatalf("%+v", res.Videos)
	}
	for _, r := range [][3]string{
		{"POST", "/api/v1/me/video-history/watches", `{"video_id":"rvOZh8idOrU","title":"t"}`},
		{"GET", "/api/v1/me/video-history", ""},
		{"GET", "/api/v1/me/video-recommendations", ""},
		{"GET", "/api/v1/videos/rvOZh8idOrU/related", ""},
		{"DELETE", "/api/v1/me/video-history", ""},
	} {
		if resp, b := doRaw(t, ts, kid, r[0], r[1], r[2]); resp.StatusCode/100 != 2 {
			t.Errorf("member %s %s → %d %s", r[0], r[1], resp.StatusCode, b)
		}
	}
	// Per user: the admin sees none of kid's history.
	if resp, b := do(t, ts, kid, "POST", "/api/v1/me/video-history/watches", map[string]any{"video_id": "rvOZh8idOrU", "title": "t", "channel": "c", "channel_id": liuxiang, "duration_s": 61}); resp.StatusCode != 204 {
		t.Fatalf("watch %d %s", resp.StatusCode, b)
	}
	_, kb := do(t, ts, kid, "GET", "/api/v1/me/video-history", nil)
	var h struct {
		Watches  []map[string]any
		Searches []string
	}
	json.Unmarshal(kb, &h)
	if len(h.Watches) != 1 || h.Watches[0]["video_id"] != "rvOZh8idOrU" || h.Watches[0]["thumbnail"] != "/api/v1/videos/rvOZh8idOrU/thumbnail" ||
		h.Watches[0]["channel_id"] != liuxiang || h.Watches[0]["duration_s"] != float64(61) {
		t.Fatalf("kid history %s", kb)
	}
	_, hb := do(t, ts, admin, "GET", "/api/v1/me/video-history", nil)
	if strings.Contains(string(hb), "rvOZh8idOrU") {
		t.Errorf("history leaked: %s", hb)
	}
	_, rb := do(t, ts, admin, "GET", "/api/v1/me/video-recommendations", nil)
	if !strings.Contains(string(rb), `"items":[]`) {
		t.Errorf("admin recs %s", rb)
	}
}

func TestVideoWatchValidation(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	kid := loginAs(t, s, "kid", "member")
	for _, body := range []string{`{"video_id":"../etc/pass"}`, `{"video_id":""}`, `{"title":"t"}`} {
		if resp, b := doRaw(t, ts, kid, "POST", "/api/v1/me/video-history/watches", body); resp.StatusCode != 400 || code(b) != "bad_video_id" {
			t.Errorf("%s → %d %s", body, resp.StatusCode, b)
		}
	}
	if resp, b := doRaw(t, ts, kid, "POST", "/api/v1/me/video-history/watches", `{"video_id":"rvOZh8idOrU","evil":1}`); resp.StatusCode != 400 {
		t.Errorf("unknown field → %d %s", resp.StatusCode, b)
	}
	// Untrusted text is bounded and a malformed channel id blanked (Task 2).
	long := strings.Repeat("长", 400)
	doRaw(t, ts, kid, "POST", "/api/v1/me/video-history/watches", `{"video_id":"rvOZh8idOrU","title":"`+long+`","channel_id":"not-a-channel"}`)
	var title, chID string
	s.Library.DB.QueryRow(`SELECT label, channel_id FROM video_history WHERE item='rvOZh8idOrU'`).Scan(&title, &chID)
	if len([]rune(title)) != 300 || chID != "" {
		t.Errorf("stored %d runes, channel %q", len([]rune(title)), chID)
	}
}

func TestVideoSearchRecordsOnlyWhenSubmitted(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	kid := loginAs(t, s, "kid", "member")
	uid := userID(t, s, "kid")
	if resp, b := do(t, ts, kid, "GET", "/api/v1/videos/search?q=liu", nil); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if n, _ := videoSearches(t, s, uid); n != 0 {
		t.Fatalf("typing recorded %d searches", n)
	}
	if resp, b := do(t, ts, kid, "GET", "/api/v1/videos/search?q=liu&record=1", nil); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if n, seed := videoSearches(t, s, uid); n != 1 || seed != "v1_0000000x" {
		t.Fatalf("submitted: %d searches, seed %q", n, seed)
	}
	for _, q := range []string{"", "%20%20", strings.Repeat("a", 101)} {
		if resp, b := do(t, ts, kid, "GET", "/api/v1/videos/search?q="+q, nil); resp.StatusCode != 400 || code(b) != "bad_query" {
			t.Errorf("q=%q → %d %s", q, resp.StatusCode, b)
		}
	}
}

func TestVideoSearchDropsLive(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	s.YT = &ytdlp.Client{Runner: &fakeYTRunner{out: []byte(liveAndVideoJSON)}}
	kid := loginAs(t, s, "kid", "member")
	resp, b := do(t, ts, kid, "GET", "/api/v1/videos/search?q=x", nil)
	if resp.StatusCode != 200 || strings.Contains(string(b), "live_000000") || !strings.Contains(string(b), "v1_0000000x") {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if strings.Contains(string(b), "i.ytimg.com") {
		t.Fatalf("a YouTube thumbnail reached the client: %s", b)
	}
}

func TestVideoSearchFailureCodes(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	s.YT = &ytdlp.Client{Runner: &fakeYTRunner{err: io.ErrUnexpectedEOF}}
	kid := loginAs(t, s, "kid", "member")
	if resp, b := do(t, ts, kid, "GET", "/api/v1/videos/search?q=x", nil); resp.StatusCode != 502 || code(b) != "youtube_search_failed" {
		t.Errorf("search %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, ts, kid, "GET", "/api/v1/videos/rvOZh8idOrU/related", nil); resp.StatusCode != 502 || code(b) != "youtube_search_failed" {
		t.Errorf("related %d %s", resp.StatusCode, b)
	}
}

func TestRelatedIsCachedAcrossCalls(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	fr := &fakeYTRunner{out: []byte(liveAndVideoJSON)}
	s.YT = &ytdlp.Client{Runner: fr}
	kid := loginAs(t, s, "kid", "member")
	for range 2 {
		resp, b := do(t, ts, kid, "GET", "/api/v1/videos/rvOZh8idOrU/related", nil)
		if resp.StatusCode != 200 || !strings.Contains(string(b), `"thumbnail":"/api/v1/videos/v1_0000000x/thumbnail"`) || strings.Contains(string(b), "live_000000") {
			t.Fatalf("%d %s", resp.StatusCode, b)
		}
	}
	if n := fr.callCount(); n != 1 {
		t.Fatalf("%d yt-dlp runs, want 1", n)
	}
	// The video itself is never among its related ones.
	_, b := do(t, ts, kid, "GET", "/api/v1/videos/v1_0000000x/related", nil)
	if strings.Contains(string(b), `"id":"v1_0000000x"`) {
		t.Fatalf("itself listed: %s", b)
	}
	if resp, b := do(t, ts, kid, "GET", "/api/v1/videos/bad..id/related", nil); resp.StatusCode != 400 || code(b) != "bad_video_id" {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}

func TestVideosOffWhenChannelsOff(t *testing.T) {
	s, ts := newTestServer(t)
	kid := loginAs(t, s, "kid", "member")
	for _, r := range [][2]string{
		{"GET", "/api/v1/videos/search?q=x"}, {"GET", "/api/v1/videos/rvOZh8idOrU/related"}, {"GET", "/api/v1/videos/rvOZh8idOrU/thumbnail"},
		{"POST", "/api/v1/me/video-history/watches"}, {"GET", "/api/v1/me/video-history"}, {"DELETE", "/api/v1/me/video-history"},
		{"GET", "/api/v1/me/video-recommendations"},
	} {
		if resp, b := do(t, ts, kid, r[0], r[1], map[string]any{"video_id": "rvOZh8idOrU"}); resp.StatusCode != 409 || code(b) != "channels_off" {
			t.Errorf("%s %s → %d %s", r[0], r[1], resp.StatusCode, b)
		}
	}
}

func TestVideoRoutesNeedSignIn(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	for _, p := range []string{"/api/v1/videos/search?q=x", "/api/v1/videos/rvOZh8idOrU/thumbnail", "/api/v1/me/video-history"} {
		if resp, _ := do(t, ts, "", "GET", p, nil); resp.StatusCode != 401 {
			t.Errorf("%s → %d", p, resp.StatusCode)
		}
	}
}

func TestThumbnailRoute(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	jpeg := append([]byte{0xFF, 0xD8, 0xFF}, bytes.Repeat([]byte{7}, 50)...)
	var hits atomic.Int32
	yt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/rvOZh8idOrU/mqdefault.jpg" {
			http.NotFound(w, r)
			return
		}
		w.Write(jpeg)
	}))
	defer yt.Close()
	s.Thumbs = &preview.Thumbs{Dir: t.TempDir(), BaseURL: yt.URL}
	kid := loginAs(t, s, "kid", "member")
	for range 2 {
		resp, b := do(t, ts, kid, "GET", "/api/v1/videos/rvOZh8idOrU/thumbnail", nil)
		if resp.StatusCode != 200 || !bytes.Equal(b, jpeg) || resp.Header.Get("Content-Type") != "image/jpeg" ||
			resp.Header.Get("Cache-Control") != "private, max-age=604800" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%d %v %q", resp.StatusCode, resp.Header, b)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("fetched %d times", hits.Load())
	}
	if resp, b := do(t, ts, kid, "GET", "/api/v1/videos/bad..id/thumbnail", nil); resp.StatusCode != 400 || code(b) != "bad_video_id" {
		t.Fatalf("bad id %d %s", resp.StatusCode, b)
	}
	// A miss is cached by the browser for 10 minutes, like the server does.
	if resp, _ := do(t, ts, kid, "GET", "/api/v1/videos/missing_000/thumbnail", nil); resp.StatusCode != 404 ||
		resp.Header.Get("Cache-Control") != "private, max-age=600" {
		t.Fatalf("missing %d %v", resp.StatusCode, resp.Header)
	}
	s.Thumbs = nil // not wired: nothing to serve
	if resp, _ := do(t, ts, kid, "GET", "/api/v1/videos/rvOZh8idOrU/thumbnail", nil); resp.StatusCode != 404 {
		t.Fatalf("no proxy %d", resp.StatusCode)
	}
}

func TestVideoRecsRequestsARefreshOnVisit(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	kid := loginAs(t, s, "kid", "member")
	do(t, ts, kid, "POST", "/api/v1/me/video-history/watches", map[string]any{"video_id": "rvOZh8idOrU", "title": "t"})
	resp, b := do(t, ts, kid, "GET", "/api/v1/me/video-recommendations", nil)
	var recs struct {
		Items      []any
		Refreshing bool
	}
	json.Unmarshal(b, &recs)
	if resp.StatusCode != 200 || recs.Items == nil || !recs.Refreshing {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if resp, _ := do(t, ts, kid, "DELETE", "/api/v1/me/video-history", nil); resp.StatusCode != 204 {
		t.Fatalf("clear %d", resp.StatusCode)
	}
	h, _ := s.Channels.VideoHistory(context.Background(), userID(t, s, "kid"))
	if len(h.Watches) != 0 {
		t.Fatalf("not cleared: %+v", h)
	}
}

// A 404 because every fetch slot stayed busy is not a miss: the browser must
// not cache it.
func TestThumbnailBusyIsNotCached(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	release := make(chan struct{})
	yt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Write([]byte{0xFF, 0xD8, 0xFF, 1})
	}))
	defer yt.Close()
	defer close(release)
	s.Thumbs = &preview.Thumbs{Dir: t.TempDir(), BaseURL: yt.URL, SlotWait: 50 * time.Millisecond}
	kid := loginAs(t, s, "kid", "member")
	for i := range 4 { // hold every slot
		go s.Thumbs.Path(context.Background(), "blocking_0"+string(rune('0'+i)))
	}
	time.Sleep(100 * time.Millisecond)
	resp, _ := do(t, ts, kid, "GET", "/api/v1/videos/waiting_000/thumbnail", nil)
	if resp.StatusCode != 404 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("busy %d %v", resp.StatusCode, resp.Header)
	}
}
