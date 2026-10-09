package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/download"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// concurrentSearch issues n GET /youtube/search requests in parallel (q(i)
// picks each request's query string) and waits for all of them, failing the
// test if any transport-level error or non-200 status comes back. It never
// calls a t.Fatal-family method from the spawned goroutines — only Errorf,
// which testing.T documents as safe for concurrent use — so it can run
// inside a goroutine safely.
func concurrentSearch(t *testing.T, ts *httptest.Server, tok string, n int, q func(i int) string) {
	t.Helper()
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req, _ := http.NewRequest("GET", ts.URL+"/api/v1/youtube/search?q="+q(i), nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Errorf("request %d: %v", i, err)
				return
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body)
			if resp.StatusCode != 200 {
				t.Errorf("request %d: status %d", i, resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()
}

// withDownloadTarget marks a fresh library as the download target so
// Enqueue/EnqueueVideo don't fail with ErrNoTarget.
func withDownloadTarget(t *testing.T, s *Server) {
	t.Helper()
	if _, err := s.Library.EnsureLibrary(context.Background(), "youtube", t.TempDir(), true); err != nil {
		t.Fatal(err)
	}
}

func jobsOf(t *testing.T, body []byte) []download.Job {
	t.Helper()
	var out struct {
		Jobs []download.Job `json:"jobs"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode jobs: %v, body=%s", err, body)
	}
	return out.Jobs
}

func TestYouTubeSearchReturnsParsedVideos(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=%E9%82%93%E4%B8%BD%E5%90%9B", nil)
	if r.StatusCode != 200 {
		t.Fatalf("search: %d %s", r.StatusCode, b)
	}
	var res ytdlp.SearchResult
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.Videos) != 1 || res.Videos[0].ID != "v1_0000000x" || res.Videos[0].Title != "测试视频" {
		t.Fatalf("unexpected videos: %+v", res.Videos)
	}
	if !strings.Contains(string(b), `"playlists":[]`) {
		t.Fatalf("playlists must be an empty array, got %s", b)
	}
}

func TestYouTubeSearchCachesIdenticalQuery(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	fr := s.YT.Runner.(*fakeYTRunner)

	for range 2 {
		r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=teresa", nil)
		if r.StatusCode != 200 {
			t.Fatalf("search: %d %s", r.StatusCode, b)
		}
	}
	if n := fr.callCount(); n != 2 { // video search + playlist search, once
		t.Fatalf("want 2 runner calls (video+playlist) for a cached repeat query, got %d", n)
	}
}

func TestYouTubeSearchReturnsPlaylistsAndCachesBoth(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	fr := s.YT.Runner.(*fakeYTRunner)
	pl, err := os.ReadFile("../ytdlp/testdata/search_playlists.json")
	if err != nil {
		t.Fatal(err)
	}
	fr.outFor = func(args []string) ([]byte, error) {
		if strings.Contains(args[len(args)-1], "results?search_query") {
			return pl, nil
		}
		return []byte(oneVideoSearchJSON), nil
	}
	for i := range 2 {
		r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=teng", nil)
		if r.StatusCode != 200 {
			t.Fatalf("search: %d %s", r.StatusCode, b)
		}
		var res ytdlp.SearchResult
		if err := json.Unmarshal(b, &res); err != nil {
			t.Fatal(err)
		}
		if len(res.Videos) != 1 || len(res.Playlists) != 2 || res.Playlists[0].ID != "PL1F3EC94FCEA4669F" {
			t.Fatalf("round %d: %s", i, b)
		}
	}
	if n := fr.callCount(); n != 2 {
		t.Fatalf("want 2 runner calls for the first query and 0 for the cached repeat, got %d", n)
	}
}

func TestYouTubeSearchPlaylistFailureStillReturnsVideos(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	s.YT.Runner.(*fakeYTRunner).outFor = func(args []string) ([]byte, error) {
		if strings.Contains(args[len(args)-1], "results?search_query") {
			return nil, errors.New("yt-dlp: boom")
		}
		return []byte(oneVideoSearchJSON), nil
	}
	r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=x", nil)
	if r.StatusCode != 200 || !strings.Contains(string(b), `"playlists":[]`) || !strings.Contains(string(b), "v1_0000000x") {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
}

func TestYouTubeSearchPlaylistFailureIsNotCached(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	fr := s.YT.Runner.(*fakeYTRunner)
	fr.outFor = func(args []string) ([]byte, error) {
		if strings.Contains(args[len(args)-1], "results?search_query") {
			return nil, errors.New("yt-dlp: blip")
		}
		return []byte(oneVideoSearchJSON), nil
	}
	for i := range 2 {
		r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=blip", nil)
		if r.StatusCode != 200 || !strings.Contains(string(b), `"playlists":[]`) || !strings.Contains(string(b), "v1_0000000x") {
			t.Fatalf("round %d: %d %s", i, r.StatusCode, b)
		}
	}
	if n := fr.callCount(); n != 4 {
		t.Fatalf("a failed playlist search must not be cached: want 4 runner calls, got %d", n)
	}
}

func TestYouTubePlaylistEndpoint(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	fr := s.YT.Runner.(*fakeYTRunner)
	info, err := os.ReadFile("../ytdlp/testdata/playlist_info.json")
	if err != nil {
		t.Fatal(err)
	}
	fr.out = info
	for range 2 {
		r, b := do(t, ts, tok, "GET", "/api/v1/youtube/playlist?list=PL1F3EC94FCEA4669F", nil)
		if r.StatusCode != 200 || !strings.Contains(string(b), `"count":247`) {
			t.Fatalf("%d %s", r.StatusCode, b)
		}
	}
	if n := fr.callCount(); n != 1 {
		t.Fatalf("want 1 runner call (cached), got %d", n)
	}
	for _, bad := range []string{"RDxyzxyzxyzxyz", "", "--exec=x", "short"} {
		r, b := do(t, ts, tok, "GET", "/api/v1/youtube/playlist?list="+bad, nil)
		if r.StatusCode != 400 || !strings.Contains(string(b), `"bad_request"`) {
			t.Fatalf("%q: %d %s", bad, r.StatusCode, b)
		}
	}
}

func TestYouTubeSearchEmptyQueryIs400(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=", nil)
	if r.StatusCode != 400 {
		t.Fatalf("empty query: got %d %s, want 400", r.StatusCode, b)
	}
}

func TestYouTubeSearchRunnerErrorIs502(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	s.YT.Runner.(*fakeYTRunner).err = errors.New("yt-dlp: exit status 1: ERROR: network unreachable")
	r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=x", nil)
	if r.StatusCode != 502 {
		t.Fatalf("runner error: got %d %s, want 502", r.StatusCode, b)
	}
	if !strings.Contains(string(b), "YouTube search failed") || !strings.Contains(string(b), `"code":"youtube_search_failed"`) {
		t.Fatalf("missing message/code: %s", b)
	}
}

// Many concurrent, distinct-query searches must never have more than
// searchTokens (4) yt-dlp processes in flight at once (a query runs two), yet
// must genuinely overlap.
func TestYouTubeSearchProcessesCappedAtFour(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	fr := s.YT.Runner.(*fakeYTRunner)
	fr.delay = 150 * time.Millisecond // long enough that concurrent calls actually overlap

	concurrentSearch(t, ts, tok, 6, func(i int) string { return fmt.Sprintf("query%d", i) })

	if n := fr.callCount(); n != 12 {
		t.Fatalf("6 distinct queries: want 12 runner calls (video+playlist each), got %d", n)
	}
	if max := fr.maxInflightCount(); max <= 2 || max > searchTokens {
		t.Fatalf("concurrency cap: max in flight = %d, want 3..%d", max, searchTokens)
	}
}

// 5 concurrent requests for the identical query must collapse into one
// video+playlist pair (singleflight).
func TestYouTubeSearchSingleflightCollapsesIdenticalQueries(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	fr := s.YT.Runner.(*fakeYTRunner)
	fr.delay = 150 * time.Millisecond

	concurrentSearch(t, ts, tok, 5, func(int) string { return "同一首歌" })

	if n := fr.callCount(); n != 2 {
		t.Fatalf("5 identical concurrent queries: want 2 runner calls (one video+playlist pair), got %d", n)
	}
}

func TestCreateDownloadRejectsNonYouTubeURL(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	tok := loginAs(t, s, "alice", "member")
	for _, u := range []string{"https://evil.com/x", "--exec=x"} {
		r, b := do(t, ts, tok, "POST", "/api/v1/downloads", map[string]string{"url": u})
		if r.StatusCode != 400 {
			t.Fatalf("url=%q: got %d %s, want 400", u, r.StatusCode, b)
		}
		if !strings.Contains(string(b), "only YouTube links are supported") || !strings.Contains(string(b), `"code":"youtube_only"`) {
			t.Fatalf("url=%q: missing message/code: %s", u, b)
		}
	}
}

func TestCreateDownloadNoTargetLibraryIs409(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	r, b := do(t, ts, tok, "POST", "/api/v1/downloads", map[string]string{"url": "https://www.youtube.com/watch?v=v1_0000000x"})
	if r.StatusCode != 409 {
		t.Fatalf("no target library: got %d %s, want 409", r.StatusCode, b)
	}
	if !strings.Contains(string(b), "no library is set as the download target") || !strings.Contains(string(b), `"code":"no_download_target"`) {
		t.Fatalf("missing message/code: %s", b)
	}
}

func TestCreateDownloadValidURL(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	tok := loginAs(t, s, "alice", "member")
	r, b := do(t, ts, tok, "POST", "/api/v1/downloads", map[string]string{"url": "https://www.youtube.com/watch?v=v1_0000000x"})
	if r.StatusCode != 201 {
		t.Fatalf("create: %d %s", r.StatusCode, b)
	}
	jobs := jobsOf(t, b)
	if len(jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(jobs))
	}
}

func TestCreateDownloadFromVideoSkipsResolve(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	tok := loginAs(t, s, "alice", "member")
	fr := s.YT.Runner.(*fakeYTRunner)
	before := fr.callCount()

	body := map[string]any{"video": map[string]any{
		"id": "v9_0000000x", "title": "T", "channel": "C",
		"url": "https://www.youtube.com/watch?v=v9_0000000x", "thumbnail": "", "duration_s": 100,
	}}
	r, b := do(t, ts, tok, "POST", "/api/v1/downloads", body)
	if r.StatusCode != 201 {
		t.Fatalf("create from video: %d %s", r.StatusCode, b)
	}
	jobs := jobsOf(t, b)
	if len(jobs) != 1 || jobs[0].VideoID != "v9_0000000x" {
		t.Fatalf("unexpected jobs: %+v", jobs)
	}
	if jobs[0].Thumbnail != "https://i.ytimg.com/vi/v9_0000000x/hqdefault.jpg" {
		t.Fatalf("thumbnail %q, want canonical hqdefault", jobs[0].Thumbnail)
	}
	if n := fr.callCount(); n != before {
		t.Fatalf("EnqueueVideo must not call yt-dlp (no Resolve/--flat-playlist): %d new calls", n-before)
	}
}

// The video-shortcut path must reject a non-YouTube URL the same way the
// plain-url path does, before ever looking at video.id.
func TestCreateDownloadFromVideoRejectsNonYouTubeURL(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	tok := loginAs(t, s, "alice", "member")
	for _, u := range []string{"https://evil.com/x", "--exec=x"} {
		body := map[string]any{"video": map[string]any{"id": "v1_0000000x", "title": "T", "channel": "C", "url": u}}
		r, b := do(t, ts, tok, "POST", "/api/v1/downloads", body)
		if r.StatusCode != 400 {
			t.Fatalf("video.url=%q: got %d %s, want 400", u, r.StatusCode, b)
		}
		if !strings.Contains(string(b), "only YouTube links are supported") || !strings.Contains(string(b), `"code":"youtube_only"`) {
			t.Fatalf("video.url=%q: missing message/code: %s", u, b)
		}
	}
}

// video.id must match the id actually encoded in video.url — otherwise a
// client-constructed (not search-result-derived) body could attach the
// wrong title/channel/thumbnail to whatever video the url really points at.
func TestCreateDownloadVideoIDMustMatchURL(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	tok := loginAs(t, s, "alice", "member")

	cases := []struct{ id, url string }{
		{"wrong-id", "https://www.youtube.com/watch?v=v1_0000000x"},
		{"wrong-id", "https://youtu.be/v1_0000000x"},
	}
	for _, c := range cases {
		body := map[string]any{"video": map[string]any{"id": c.id, "title": "T", "channel": "C", "url": c.url}}
		r, b := do(t, ts, tok, "POST", "/api/v1/downloads", body)
		if r.StatusCode != 400 {
			t.Fatalf("id=%q url=%q: got %d %s, want 400", c.id, c.url, r.StatusCode, b)
		}
	}

	// A matching id (either URL shape) must still succeed.
	for _, url := range []string{"https://www.youtube.com/watch?v=v1_0000000x", "https://youtu.be/v1_0000000x",
		"https://www.youtube.com/shorts/v1_0000000x", "https://www.youtube.com/live/v1_0000000x", "https://www.youtube.com/embed/v1_0000000x"} {
		body := map[string]any{"video": map[string]any{"id": "v1_0000000x", "title": "T", "channel": "C", "url": url}}
		r, b := do(t, ts, tok, "POST", "/api/v1/downloads", body)
		if r.StatusCode != 201 {
			t.Fatalf("matching id, url=%q: got %d %s, want 201", url, r.StatusCode, b)
		}
	}
}

func TestDownloadOwnershipAndAdminVisibility(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	alice := loginAs(t, s, "alice", "member")
	bob := loginAs(t, s, "bob", "member")
	admin := loginAs(t, s, "dad", "admin")

	r, b := do(t, ts, alice, "POST", "/api/v1/downloads", map[string]string{"url": "https://www.youtube.com/watch?v=v1_0000000x"})
	if r.StatusCode != 201 {
		t.Fatalf("alice create: %d %s", r.StatusCode, b)
	}
	id := jobsOf(t, b)[0].ID
	idStr := fmt.Sprintf("%d", id)

	// bob (another member) must not see alice's job in his own list...
	_, b = do(t, ts, bob, "GET", "/api/v1/downloads", nil)
	if strings.Contains(string(b), `"id":`+idStr) {
		t.Fatalf("bob's list leaks alice's job: %s", b)
	}
	// ...and cancelling it must look like it doesn't exist.
	if r, _ := do(t, ts, bob, "DELETE", "/api/v1/downloads/"+idStr, nil); r.StatusCode != 404 {
		t.Fatalf("bob cancel alice's job: got %d, want 404", r.StatusCode)
	}
	// ...even a member passing ?all=1 themselves: that parameter is admin-only.
	_, b = do(t, ts, bob, "GET", "/api/v1/downloads?all=1", nil)
	if strings.Contains(string(b), `"id":`+idStr) {
		t.Fatalf("member's ?all=1 was honoured and leaked alice's job: %s", b)
	}

	// admin sees it via ?all=1.
	_, b = do(t, ts, admin, "GET", "/api/v1/downloads?all=1", nil)
	if !strings.Contains(string(b), `"id":`+idStr) {
		t.Fatalf("admin ?all=1 doesn't see alice's job: %s", b)
	}
}

// An admin isn't limited to viewing other users' jobs: they can cancel or
// retry them too, unlike a member.
func TestAdminCanCancelAndRetryAnotherUsersJob(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	alice := loginAs(t, s, "alice", "member")
	admin := loginAs(t, s, "dad", "admin")

	_, b := do(t, ts, alice, "POST", "/api/v1/downloads", map[string]string{"url": "https://www.youtube.com/watch?v=v1_0000000x"})
	id := jobsOf(t, b)[0].ID
	idStr := fmt.Sprintf("%d", id)

	if r, _ := do(t, ts, admin, "DELETE", "/api/v1/downloads/"+idStr, nil); r.StatusCode != 204 {
		t.Fatalf("admin cancel alice's job: got %d, want 204", r.StatusCode)
	}
	if r, b := do(t, ts, admin, "POST", "/api/v1/downloads/"+idStr+"/retry", nil); r.StatusCode != 204 {
		t.Fatalf("admin retry alice's job: got %d %s, want 204", r.StatusCode, b)
	}
}

// Review focus (carried from Task 3, Ruling 4): when Enqueue dedupes to a job
// owned by another user, the API must return it with user_id/username
// stripped — members must never learn that another user's job exists.
func TestCreateDownloadDedupeRedactsForeignOwner(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	alice := loginAs(t, s, "alice", "member")
	bob := loginAs(t, s, "bob", "member")
	url := "https://www.youtube.com/watch?v=v1_0000000x"

	if r, b := do(t, ts, alice, "POST", "/api/v1/downloads", map[string]string{"url": url}); r.StatusCode != 201 {
		t.Fatalf("alice create: %d %s", r.StatusCode, b)
	}
	r, b := do(t, ts, bob, "POST", "/api/v1/downloads", map[string]string{"url": url})
	if r.StatusCode != 201 {
		t.Fatalf("bob create (dedupe): %d %s", r.StatusCode, b)
	}
	jobs := jobsOf(t, b)
	if len(jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(jobs))
	}
	if jobs[0].UserID != 0 || jobs[0].Username != "" {
		t.Fatalf("bob must not learn alice owns this job: %+v", jobs[0])
	}
}

func TestRetryDownload(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	alice := loginAs(t, s, "alice", "member")
	bob := loginAs(t, s, "bob", "member")

	_, b := do(t, ts, alice, "POST", "/api/v1/downloads", map[string]string{"url": "https://www.youtube.com/watch?v=v1_0000000x"})
	id := jobsOf(t, b)[0].ID
	if _, err := s.Library.DB.Exec(`UPDATE downloads SET status='failed' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	idStr := fmt.Sprintf("%d", id)

	if r, _ := do(t, ts, bob, "POST", "/api/v1/downloads/"+idStr+"/retry", nil); r.StatusCode != 404 {
		t.Fatalf("bob retry: got %d, want 404", r.StatusCode)
	}
	if r, b := do(t, ts, alice, "POST", "/api/v1/downloads/"+idStr+"/retry", nil); r.StatusCode != 204 {
		t.Fatalf("alice retry: %d %s", r.StatusCode, b)
	}
}

// A retry blocked because another job already covers the video is a 409
// with a message the UI shows on the row, not a silent 204.
func TestRetryBlockedByDedupeIs409(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	alice := loginAs(t, s, "alice", "member")
	url := "https://www.youtube.com/watch?v=v1_0000000x"
	_, b := do(t, ts, alice, "POST", "/api/v1/downloads", map[string]string{"url": url})
	id := jobsOf(t, b)[0].ID
	if _, err := s.Library.DB.Exec(`UPDATE downloads SET status='failed' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if r, b := do(t, ts, alice, "POST", "/api/v1/downloads", map[string]string{"url": url}); r.StatusCode != 201 {
		t.Fatalf("second create: %d %s", r.StatusCode, b)
	}
	r, b := do(t, ts, alice, "POST", fmt.Sprintf("/api/v1/downloads/%d/retry", id), nil)
	if r.StatusCode != 409 || !strings.Contains(string(b), `"error":"this video is already in the download queue"`) || !strings.Contains(string(b), `"code":"already_queued"`) {
		t.Fatalf("blocked retry: %d %s, want 409 already_queued", r.StatusCode, b)
	}
}

func TestCreateDownloadFromListReturnsPlaylist(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	tok := loginAs(t, s, "alice", "member")
	fr := s.YT.Runner.(*fakeYTRunner)
	info, err := os.ReadFile("../ytdlp/testdata/playlist_info.json")
	if err != nil {
		t.Fatal(err)
	}
	fr.out = info
	r, b := do(t, ts, tok, "POST", "/api/v1/downloads", map[string]string{"url": "https://www.youtube.com/playlist?list=PL1F3EC94FCEA4669F"})
	if r.StatusCode != 201 || !strings.Contains(string(b), `"playlist":{"id":`) ||
		!strings.Contains(string(b), `"list_id":"PL1F3EC94FCEA4669F"`) || len(jobsOf(t, b)) != 1 {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	// A single song carries no playlist.
	r, b = do(t, ts, tok, "POST", "/api/v1/downloads", map[string]string{"url": "https://www.youtube.com/watch?v=IiFm7AWP9n4&list=PL1F3EC94FCEA4669F"})
	if r.StatusCode != 201 || strings.Contains(string(b), `"playlist"`) {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
}

func TestRemovingFinishedDownloadHidesItFromLists(t *testing.T) {
	s, ts := newTestServer(t)
	withDownloadTarget(t, s)
	alice := loginAs(t, s, "alice", "member")
	bob := loginAs(t, s, "bob", "member")
	admin := loginAs(t, s, "dad", "admin")

	_, b := do(t, ts, alice, "POST", "/api/v1/downloads", map[string]string{"url": "https://www.youtube.com/watch?v=v1_0000000x"})
	idStr := fmt.Sprintf("%d", jobsOf(t, b)[0].ID)
	// queued -> cancelled, still listed; a second DELETE hides it.
	if r, _ := do(t, ts, alice, "DELETE", "/api/v1/downloads/"+idStr, nil); r.StatusCode != 204 {
		t.Fatalf("cancel: %d", r.StatusCode)
	}
	if _, b = do(t, ts, alice, "GET", "/api/v1/downloads", nil); !strings.Contains(string(b), `"id":`+idStr) {
		t.Fatalf("cancelled job should be listed: %s", b)
	}
	if r, _ := do(t, ts, bob, "DELETE", "/api/v1/downloads/"+idStr, nil); r.StatusCode != 404 {
		t.Fatalf("bob hide: %d, want 404", r.StatusCode)
	}
	if r, _ := do(t, ts, alice, "DELETE", "/api/v1/downloads/"+idStr, nil); r.StatusCode != 204 {
		t.Fatalf("hide: %d", r.StatusCode)
	}
	for _, c := range []struct{ tok, path string }{{alice, "/api/v1/downloads"}, {admin, "/api/v1/downloads?all=1"}} {
		if _, b = do(t, ts, c.tok, "GET", c.path, nil); strings.Contains(string(b), `"id":`+idStr) {
			t.Fatalf("%s still lists the hidden job: %s", c.path, b)
		}
	}
}

// A search for "list:<id>" must not be served as that playlist (it used to
// share the cache key and panic on an empty Playlists).
func TestYouTubePlaylistCacheKeyDistinctFromSearch(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	fr := s.YT.Runner.(*fakeYTRunner)
	info, err := os.ReadFile("../ytdlp/testdata/playlist_info.json")
	if err != nil {
		t.Fatal(err)
	}
	fr.outFor = func(args []string) ([]byte, error) {
		last := args[len(args)-1]
		switch {
		case strings.Contains(last, "results?search_query"):
			return []byte(`{"entries":[]}`), nil
		case strings.HasPrefix(last, "ytsearch"):
			return []byte(oneVideoSearchJSON), nil
		}
		return info, nil
	}
	if r, b := do(t, ts, tok, "GET", "/api/v1/youtube/search?q=list:abcdefghijklm", nil); r.StatusCode != 200 {
		t.Fatalf("search: %d %s", r.StatusCode, b)
	}
	r, b := do(t, ts, tok, "GET", "/api/v1/youtube/playlist?list=abcdefghijklm", nil)
	if r.StatusCode != 200 || !strings.Contains(string(b), `"count":247`) {
		t.Fatalf("playlist after a colliding search: %d %s", r.StatusCode, b)
	}
}

func TestCreateDownloadAcceptsVideoWithChannelID(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "kid", "member")
	if _, err := s.Library.EnsureLibrary(context.Background(), "youtube", t.TempDir(), true); err != nil {
		t.Fatal(err)
	}
	resp, body := do(t, ts, tok, "POST", "/api/v1/downloads", map[string]any{"video": map[string]any{
		"id": "v1_0000000x", "title": "T", "channel": "C", "url": "https://www.youtube.com/watch?v=v1_0000000x",
		"thumbnail": "", "duration_s": 100, "channel_id": "UC0e5c4U67Vm6sAVK0vxN3Uw"}})
	if resp.StatusCode != 201 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}
