package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/preview"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

const biliSearchLines = `{"id":"BV1aaaaaaaa1","title":"晴天","uploader":"某UP主","duration":269,"thumbnail":"https://i0.hdslb.com/bfs/archive/a.jpg"}
{"id":"BV1bbbbbbbb2","title":"晴天 钢琴","uploader":"另一个","duration":180}
`

// biliRunner answers Bilibili lookups with biliSearchLines and anything else
// with the canned YouTube search.
func biliRunner(s *Server) *fakeYTRunner {
	fr := s.YT.Runner.(*fakeYTRunner)
	fr.outFor = func(args []string) ([]byte, error) {
		for _, a := range args {
			if strings.HasPrefix(a, "bilisearch") || strings.Contains(a, "bilibili.com") {
				return []byte(biliSearchLines), nil
			}
		}
		return []byte(oneVideoSearchJSON), nil
	}
	return fr
}

func setSources(t *testing.T, s *Server, srcs ...ytdlp.Source) {
	t.Helper()
	if err := s.VideoSources.Set(context.Background(), srcs); err != nil {
		t.Fatal(err)
	}
}

func usedBilibili(fr *fakeYTRunner) bool {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	for _, c := range fr.calls {
		if slices.ContainsFunc(c, func(a string) bool { return strings.HasPrefix(a, "bilisearch") }) {
			return true
		}
	}
	return false
}

func TestSearchBilibiliSource(t *testing.T) {
	s, ts := newTestServer(t)
	setSources(t, s, ytdlp.SourceYouTube, ytdlp.SourceBilibili)
	fr := biliRunner(s)
	tok := loginAs(t, s, "alice", "member")
	r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q="+url.QueryEscape("晴天")+"&source=bilibili", nil)
	var res ytdlp.SearchResult
	if r.StatusCode != 200 || json.Unmarshal(b, &res) != nil {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	if len(res.Videos) != 2 || res.Videos[0].ID != "BV1aaaaaaaa1" || res.Playlists == nil || len(res.Playlists) != 0 {
		t.Fatalf("result %s", b)
	}
	if !usedBilibili(fr) {
		t.Fatal("no bilisearch ran")
	}
	// The same query on YouTube is another cache entry.
	r, b = do(t, ts, tok, "GET", "/api/v1/youtube/search?q="+url.QueryEscape("晴天")+"&source=youtube", nil)
	if r.StatusCode != 200 || !strings.Contains(string(b), "v1_0000000x") {
		t.Fatalf("youtube: %d %s", r.StatusCode, b)
	}
}

func TestSearchDefaultSourceFollowsSettings(t *testing.T) {
	s, ts := newTestServer(t)
	setSources(t, s, ytdlp.SourceBilibili)
	fr := biliRunner(s)
	tok := loginAs(t, s, "alice", "member")
	if r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=x", nil); r.StatusCode != 200 || !usedBilibili(fr) {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
}

func TestSearchSourceDisabledRunsNoYtdlp(t *testing.T) {
	s, ts := newTestServer(t)
	fr := biliRunner(s)
	tok := loginAs(t, s, "alice", "member")
	check := func(path string) {
		t.Helper()
		before := fr.callCount()
		r, b := do(t, ts, tok, "GET", path, nil)
		if r.StatusCode != 409 || !strings.Contains(string(b), `"code":"source_disabled"`) {
			t.Fatalf("%s: %d %s", path, r.StatusCode, b)
		}
		if n := fr.callCount() - before; n != 0 {
			t.Fatalf("%s ran yt-dlp %d times", path, n)
		}
	}
	check("/api/v1/youtube/search?q=x&source=bilibili") // default: YouTube only
	setSources(t, s, ytdlp.SourceBilibili)
	check("/api/v1/youtube/search?q=x&source=youtube")
	check("/api/v1/youtube/playlist?list=PLabcdefghijklmnop")
	check("/api/v1/youtube/playlist/entries?list=PLabcdefghijklmnop")
	setSources(t, s)
	check("/api/v1/youtube/search?q=x")
	if r, _ := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=x&source=vimeo", nil); r.StatusCode != 400 {
		t.Fatalf("unknown source: %d", r.StatusCode)
	}
}

func TestSearchBilibili412NotCachedAndRetryCode(t *testing.T) {
	s, ts := newTestServer(t)
	setSources(t, s, ytdlp.SourceBilibili)
	fr := s.YT.Runner.(*fakeYTRunner)
	fr.err = errors.New("yt-dlp: exit status 1: ERROR: HTTP Error 412: Precondition Failed")
	defer func(d time.Duration) { ytdlp.BilibiliRetryDelay = d }(ytdlp.BilibiliRetryDelay)
	ytdlp.BilibiliRetryDelay = 0
	tok := loginAs(t, s, "alice", "member")
	calls := []int{}
	for i := range 2 {
		r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=x&source=bilibili", nil)
		if r.StatusCode != 503 || !strings.Contains(string(b), `"code":"bilibili_throttled"`) {
			t.Fatalf("try %d: %d %s", i, r.StatusCode, b)
		}
		calls = append(calls, fr.callCount())
	}
	if calls[0] == 0 || calls[1] != 2*calls[0] {
		t.Fatalf("a throttled search must not be cached: calls after each request %v", calls)
	}
	fr.err = errors.New("yt-dlp: exit status 1: ERROR: something broke")
	if r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=y&source=bilibili", nil); r.StatusCode != 502 || !strings.Contains(string(b), `"code":"bilibili_search_failed"`) {
		t.Fatalf("other failure: %d %s", r.StatusCode, b)
	}
}

func TestCreateDownloadBilibiliVideo(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	setSources(t, s, ytdlp.SourceBilibili)
	tok := loginAs(t, s, "alice", "member")
	body := map[string]any{"video": map[string]any{
		"id": "BV1aaaaaaaa1", "title": "晴天", "channel": "某UP主",
		"url": "https://www.bilibili.com/video/BV1aaaaaaaa1?p=1", "thumbnail": "https://i0.hdslb.com/bfs/archive/a.jpg", "duration_s": 269,
	}}
	r, b := do(t, ts, tok, "POST", "/api/v1/downloads", body)
	if r.StatusCode != 201 {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	jobs := jobsOf(t, b)
	if len(jobs) != 1 || jobs[0].VideoID != "BV1aaaaaaaa1" || jobs[0].Thumbnail != "https://i0.hdslb.com/bfs/archive/a.jpg" ||
		jobs[0].URL != "https://www.bilibili.com/video/BV1aaaaaaaa1" {
		t.Fatalf("jobs %+v", jobs)
	}
}

func TestCreateDownloadBilibiliPart2LinkQueuesPart1(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	setSources(t, s, ytdlp.SourceBilibili)
	fr := biliRunner(s)
	tok := loginAs(t, s, "alice", "member")
	r, b := do(t, ts, tok, "POST", "/api/v1/downloads", map[string]string{"url": "https://www.bilibili.com/video/BV1aaaaaaaa1?p=2"})
	if r.StatusCode != 201 {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	fr.mu.Lock()
	last := fr.calls[len(fr.calls)-1]
	fr.mu.Unlock()
	if !slices.Contains(last, "--no-playlist") {
		t.Fatalf("a single-video link resolves part 1 only: %v", last)
	}
}

func TestCreateDownloadDisabledSource409(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	fr := biliRunner(s)
	tok := loginAs(t, s, "alice", "member")
	before := fr.callCount()
	for _, body := range []any{
		map[string]string{"url": "https://www.bilibili.com/video/BV1aaaaaaaa1"},
		map[string]any{"video": map[string]any{"id": "BV1aaaaaaaa1", "title": "t", "url": "https://www.bilibili.com/video/BV1aaaaaaaa1"}},
	} {
		if r, b := do(t, ts, tok, "POST", "/api/v1/downloads", body); r.StatusCode != 409 || !strings.Contains(string(b), "source_disabled") {
			t.Fatalf("%d %s", r.StatusCode, b)
		}
	}
	if fr.callCount() != before {
		t.Fatal("yt-dlp ran for a disabled source")
	}
}

func TestBilibiliListURLCreatesDownloadList(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	setSources(t, s, ytdlp.SourceBilibili)
	fr := s.YT.Runner.(*fakeYTRunner)
	fr.out = []byte(biliSearchLines + `LARKLIST {"id":"456","title":"我的收藏","uploader":"u","playlist_count":2}` + "\n")
	tok := loginAs(t, s, "alice", "member")
	r, b := do(t, ts, tok, "POST", "/api/v1/downloads", map[string]string{"url": "https://space.bilibili.com/123/favlist?fid=456"})
	if r.StatusCode != 201 {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	var got struct {
		Jobs     []any           `json:"jobs"`
		Playlist json.RawMessage `json:"playlist"`
	}
	if json.Unmarshal(b, &got) != nil || len(got.Jobs) != 2 || len(got.Playlist) == 0 || string(got.Playlist) == "null" {
		t.Fatalf("want 2 jobs and a playlist: %s", b)
	}
}

func TestPreviewBilibiliGated(t *testing.T) {
	s, ts := newTestServer(t)
	withPreviews(t, s, "")
	tok := loginAs(t, s, "alice", "member")
	body := map[string]any{"video_id": "BV1aaaaaaaa1", "media": "audio", "title": "t"}
	r, b := do(t, ts, tok, "POST", "/api/v1/previews", body)
	if r.StatusCode != 409 || !strings.Contains(string(b), "source_disabled") {
		t.Fatalf("off: %d %s", r.StatusCode, b)
	}
	setSources(t, s, ytdlp.SourceBilibili)
	if r, b := do(t, ts, tok, "POST", "/api/v1/previews", body); r.StatusCode != 201 {
		t.Fatalf("on: %d %s", r.StatusCode, b)
	}
	// YouTube now off: a YouTube preview is refused.
	if r, b := do(t, ts, tok, "POST", "/api/v1/previews", map[string]any{"video_id": "dQw4w9WgXcQ", "media": "audio"}); r.StatusCode != 409 {
		t.Fatalf("youtube off: %d %s", r.StatusCode, b)
	}
}

func TestChannelLinkRejectsBilibili(t *testing.T) {
	w := httptest.NewRecorder()
	if _, ok := channelLink(w, "https://www.bilibili.com/video/BV1aaaaaaaa1"); ok || w.Code != 400 || !strings.Contains(w.Body.String(), "youtube_only") {
		t.Fatalf("a Bilibili link must not be a channel link: %d %s", w.Code, w.Body)
	}
	if _, ok := channelLink(httptest.NewRecorder(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ"); !ok {
		t.Fatal("a YouTube video link is a channel link")
	}
}

func TestPreviewByVideoGated(t *testing.T) {
	s, ts := newTestServer(t)
	withPreviews(t, s, "")
	tok := loginAs(t, s, "alice", "member")
	r, b := do(t, ts, tok, "GET", "/api/v1/previews/by-video/BV1aaaaaaaa1/stream", nil)
	if r.StatusCode != 409 || !strings.Contains(string(b), "source_disabled") {
		t.Fatalf("Bilibili off: %d %s", r.StatusCode, b)
	}
}

func TestPastedBilibiliLinkErrorsAreCoded(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	setSources(t, s, ytdlp.SourceBilibili)
	fr := s.YT.Runner.(*fakeYTRunner)
	tok := loginAs(t, s, "alice", "member")
	cases := map[string]struct {
		code   int
		marker string
	}{
		"ERROR: HTTP Error 412: Precondition Failed": {503, "bilibili_throttled"},
		"ERROR: [BiliBili] BV1x: 啊叻？视频不见了？":          {404, "bilibili_unavailable"},
	}
	for stderr, want := range cases {
		fr.err = errors.New("yt-dlp: exit status 1: " + stderr)
		r, b := do(t, ts, tok, "POST", "/api/v1/downloads", map[string]string{"url": "https://www.bilibili.com/video/BV1aaaaaaaa1"})
		if r.StatusCode != want.code || !strings.Contains(string(b), want.marker) {
			t.Errorf("%s: %d %s", stderr, r.StatusCode, b)
		}
	}
}

func TestChannelSearchAndLookupGatedWhenYouTubeOff(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	setSources(t, s, ytdlp.SourceBilibili)
	fr := s.YT.Runner.(*fakeYTRunner)
	tok := loginAs(t, s, "alice", "member")
	before := fr.callCount()
	for _, p := range []string{"/api/v1/channels/search?q=x", "/api/v1/channels/UCaaaaaaaaaaaaaaaaaaaaaa"} {
		if r, b := do(t, ts, tok, "GET", p, nil); r.StatusCode != 409 || !strings.Contains(string(b), "source_disabled") {
			t.Errorf("%s: %d %s", p, r.StatusCode, b)
		}
	}
	if fr.callCount() != before {
		t.Fatal("yt-dlp ran while YouTube is off")
	}
}

type countingDoer struct{ n int }

func (c *countingDoer) Do(*http.Request) (*http.Response, error) {
	c.n++
	return nil, errors.New("no network in tests")
}

// With YouTube off, the 视频 thumbnail proxy serves only what it has cached.
func TestVideoThumbnailNoFetchWhenYouTubeOff(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	doer := &countingDoer{}
	dir := t.TempDir()
	s.Thumbs = &preview.Thumbs{Dir: dir, HTTP: doer}
	setSources(t, s, ytdlp.SourceBilibili)
	tok := loginAs(t, s, "alice", "member")
	if r, _ := do(t, ts, tok, "GET", "/api/v1/videos/dQw4w9WgXcQ/thumbnail", nil); r.StatusCode != 404 {
		t.Fatalf("uncached: %d", r.StatusCode)
	}
	if doer.n != 0 {
		t.Fatal("fetched from YouTube while it is off")
	}
	if err := os.WriteFile(filepath.Join(dir, "dQw4w9WgXcQ.jpg"), []byte("\xff\xd8\xff"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r, _ := do(t, ts, tok, "GET", "/api/v1/videos/dQw4w9WgXcQ/thumbnail", nil); r.StatusCode != 200 {
		t.Fatalf("cached: %d", r.StatusCode)
	}
}

// A Bilibili search looks every result up in full (about 2.5 s each), so its
// first page is BilibiliPage results, not YouTube's 10.
func TestSearchBilibiliFirstPageIsSmall(t *testing.T) {
	s, ts := newTestServer(t)
	setSources(t, s, ytdlp.SourceBilibili)
	fr := biliRunner(s)
	tok := loginAs(t, s, "alice", "member")
	if r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=x&source=bilibili", nil); r.StatusCode != 200 {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	fr.mu.Lock()
	last := fr.calls[len(fr.calls)-1]
	fr.mu.Unlock()
	if want := "bilisearch" + strconv.Itoa(ytdlp.BilibiliPage) + ":x"; last[len(last)-1] != want || ytdlp.BilibiliPage != 5 {
		t.Fatalf("ran %v, want %s", last, want)
	}
}

// Abroad, a search can list only videos locked to mainland China: none can be
// looked up. That says so, not "search failed".
func TestSearchBilibiliAllUnavailable(t *testing.T) {
	s, ts := newTestServer(t)
	setSources(t, s, ytdlp.SourceBilibili)
	s.YT.Runner.(*fakeYTRunner).err = errors.New("yt-dlp: exit status 1: ERROR: [BiliBili] 776371743: This video may be deleted or geo-restricted. You might want to try a VPN or a proxy server (with --proxy)")
	tok := loginAs(t, s, "alice", "member")
	r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=x&source=bilibili", nil)
	if r.StatusCode != 404 || !strings.Contains(string(b), `"code":"bilibili_search_unavailable"`) {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
}
