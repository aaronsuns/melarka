package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/channels"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

const chanPageJSON = `{"_type":"playlist","id":"UC0e5c4U67Vm6sAVK0vxN3Uw","channel":"刘翔的投资频道","channel_id":"UC0e5c4U67Vm6sAVK0vxN3Uw",
 "title":"刘翔的投资频道 - Videos","uploader_id":"@liu-xiang","thumbnails":[{"url":"https://yt3.googleusercontent.com/ytc/fake=s0","id":"avatar_uncropped"}],"entries":[]}`

const chanSearchJSON = `{"_type":"playlist","id":"q","entries":[
 {"_type":"url","ie_key":"YoutubeTab","id":"UC0e5c4U67Vm6sAVK0vxN3Uw","channel":"刘翔的投资频道","uploader_id":"@liu-xiang","thumbnails":[]},
 {"_type":"url","ie_key":"YoutubeTab","id":"UCRABK12_6Ie2X549K9cXS0g","channel":"刘翔","thumbnails":[]}]}`

const liuxiang = "UC0e5c4U67Vm6sAVK0vxN3Uw"

type apiFeeds struct{ feed channels.Feed }

func (f apiFeeds) Fetch(context.Context, string) (channels.Feed, error) { return f.feed, nil }

// withChannels switches Channels on for a test server, rooted in a temp dir.
func withChannels(t *testing.T, s *Server) *channels.Service {
	t.Helper()
	s.Channels = &channels.Service{DB: s.Library.DB, Root: t.TempDir(), Feeds: apiFeeds{}, KeepDays: 10, Log: s.Log}
	return s.Channels
}

// seedEpisode stores a followed-or-not channel's episode with a done audio file on disk.
func seedEpisode(t *testing.T, s *Server, id string, withFile bool) {
	t.Helper()
	d := s.Library.DB
	now := time.Now().Unix()
	d.Exec(`INSERT OR IGNORE INTO channels(id,title,created_at) VALUES (?,?,?)`, liuxiang, "刘翔的投资频道", now)
	if _, err := d.Exec(`INSERT INTO episodes(video_id,channel_id,title,published_at,kind,seen_at) VALUES (?,?,?,?,'video',?)`, id, liuxiang, "第"+id, now-60, now); err != nil {
		t.Fatal(err)
	}
	if withFile {
		dir := filepath.Join(s.Channels.Root, liuxiang)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, id+".m4a"), []byte("0123456789"), 0o644)
		d.Exec(`INSERT INTO episode_files(video_id,kind,status,path,bytes,created_at,updated_at) VALUES (?,'audio','done',?,10,1,1)`, id, liuxiang+"/"+id+".m4a")
	}
}

func code(b []byte) string {
	var e struct{ Code string }
	json.Unmarshal(b, &e)
	return e.Code
}

func TestChannelsOff(t *testing.T) {
	s, ts := newTestServer(t)
	kid := loginAs(t, s, "kid", "member")
	for _, r := range [][2]string{{"GET", "/api/v1/channels"}, {"GET", "/api/v1/episodes/latest"}, {"POST", "/api/v1/channels/follow"}} {
		resp, body := do(t, ts, kid, r[0], r[1], map[string]any{})
		if resp.StatusCode != 409 || code(body) != "channels_off" {
			t.Errorf("%s %s → %d %s", r[0], r[1], resp.StatusCode, body)
		}
	}
}

func TestFollowByLinkSettingsUnfollow(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	s.YT = &ytdlp.Client{Runner: &fakeYTRunner{out: []byte(chanPageJSON)}}
	anna, bo := loginAs(t, s, "anna", "member"), loginAs(t, s, "bo", "member")
	resp, body := do(t, ts, anna, "POST", "/api/v1/channels/resolve", map[string]string{"url": "https://www.youtube.com/@liu-xiang"})
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"id":"`+liuxiang+`"`) || !strings.Contains(string(body), `"following":false`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	resp, body = do(t, ts, anna, "POST", "/api/v1/channels/follow", map[string]string{"url": "https://www.youtube.com/@liu-xiang"})
	if resp.StatusCode != 201 || !strings.Contains(string(body), `"media":"audio"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	resp, body = do(t, ts, anna, "GET", "/api/v1/channels", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"default_keep_days":10`) || !strings.Contains(string(body), liuxiang) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if _, body := do(t, ts, bo, "GET", "/api/v1/channels", nil); strings.Contains(string(body), liuxiang) {
		t.Fatal("bo must not see anna's follows")
	}
	resp, body = do(t, ts, anna, "PUT", "/api/v1/channels/"+liuxiang+"/settings", map[string]any{"media": "video", "keep_days": 30, "paused": false, "include_shorts": true, "include_live": false})
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"keep_days":30`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if resp, body := do(t, ts, anna, "PUT", "/api/v1/channels/"+liuxiang+"/settings", map[string]any{"media": "flac"}); resp.StatusCode != 400 || code(body) != "bad_settings" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if resp, body := do(t, ts, bo, "PUT", "/api/v1/channels/"+liuxiang+"/settings", map[string]any{"media": "audio"}); resp.StatusCode != 404 || code(body) != "not_following" {
		t.Fatalf("bo changing anna's follow: %d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, ts, anna, "DELETE", "/api/v1/channels/"+liuxiang+"/follow", nil); resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
	if resp, body := do(t, ts, anna, "DELETE", "/api/v1/channels/"+liuxiang+"/follow", nil); resp.StatusCode != 404 || code(body) != "not_following" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	// Following a known channel by id needs no yt-dlp.
	s.YT = &ytdlp.Client{Runner: &fakeYTRunner{err: fmt.Errorf("must not run")}}
	if resp, body := do(t, ts, bo, "POST", "/api/v1/channels/follow", map[string]string{"id": liuxiang}); resp.StatusCode != 201 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

func TestResolveErrors(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	kid := loginAs(t, s, "kid", "member")
	for url, want := range map[string]string{
		"https://example.com/@x": "youtube_only", "https://www.youtube.com/playlist?list=PLabcdefghijkl": "not_a_channel_link",
	} {
		if resp, body := do(t, ts, kid, "POST", "/api/v1/channels/resolve", map[string]string{"url": url}); resp.StatusCode != 400 || code(body) != want {
			t.Errorf("%s → %d %s", url, resp.StatusCode, body)
		}
	}
	s.YT = &ytdlp.Client{Runner: &fakeYTRunner{out: []byte(`{"_type":"playlist","id":"x","entries":[]}`)}}
	if resp, body := do(t, ts, kid, "POST", "/api/v1/channels/resolve", map[string]string{"url": "https://www.youtube.com/@nobody"}); resp.StatusCode != 404 || code(body) != "channel_not_found" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

func TestFollowLimitCode(t *testing.T) {
	s, ts := newTestServer(t)
	ch := withChannels(t, s)
	kid := loginAs(t, s, "kid", "member")
	var uid int64
	s.Library.DB.QueryRow(`SELECT id FROM users WHERE username='kid'`).Scan(&uid)
	for i := range channels.MaxFollows {
		if _, err := ch.Follow(context.Background(), uid, ytdlp.Channel{ID: fmt.Sprintf("UC%022d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	seedEpisode(t, s, "ep000000001", false) // stores the liuxiang channel row: follow by id needs no yt-dlp
	if resp, body := do(t, ts, kid, "POST", "/api/v1/channels/follow", map[string]string{"id": liuxiang}); resp.StatusCode != 409 || code(body) != "follow_limit" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

// Progress, keep and hide are per user; stream honours Range.
func TestEpisodesAPI(t *testing.T) {
	s, ts := newTestServer(t)
	ch := withChannels(t, s)
	anna, bo := loginAs(t, s, "anna", "member"), loginAs(t, s, "bo", "member")
	var annaID int64
	s.Library.DB.QueryRow(`SELECT id FROM users WHERE username='anna'`).Scan(&annaID)
	seedEpisode(t, s, "ep000000001", true)
	seedEpisode(t, s, "ep000000002", false)
	s.Library.DB.Exec(`INSERT INTO episode_files(video_id,kind,status,created_at,updated_at) VALUES ('ep000000002','audio','queued',1,1)`)
	if _, err := ch.Follow(context.Background(), annaID, ytdlp.Channel{ID: liuxiang}); err != nil {
		t.Fatal(err)
	}
	resp, body := do(t, ts, anna, "GET", "/api/v1/episodes/latest", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"unplayed":1`) || strings.Count(string(body), `"video_id"`) != 2 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, ts, anna, "PUT", "/api/v1/episodes/ep000000001/progress", map[string]any{"position_s": 123.5}); resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
	if _, body := do(t, ts, anna, "GET", "/api/v1/episodes/ep000000001", nil); !strings.Contains(string(body), `"position_s":123.5`) {
		t.Fatal(string(body))
	}
	if _, body := do(t, ts, bo, "GET", "/api/v1/episodes/ep000000001", nil); !strings.Contains(string(body), `"position_s":0`) {
		t.Fatal("bo has his own position: " + string(body))
	}
	do(t, ts, anna, "PUT", "/api/v1/episodes/ep000000001/progress", map[string]any{"position_s": 0, "played": true})
	if _, body := do(t, ts, anna, "GET", "/api/v1/episodes/latest", nil); !strings.Contains(string(body), `"unplayed":0`) {
		t.Fatal(string(body))
	}
	if resp, _ := do(t, ts, anna, "PUT", "/api/v1/episodes/ep000000001/keep", nil); resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
	if _, body := do(t, ts, anna, "GET", "/api/v1/episodes/kept", nil); !strings.Contains(string(body), "ep000000001") {
		t.Fatal(string(body))
	}
	if _, body := do(t, ts, bo, "GET", "/api/v1/episodes/kept", nil); strings.Contains(string(body), "ep000000001") {
		t.Fatal("keeps are per user")
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/episodes/ep000000001/stream", nil)
	req.Header.Set("Authorization", "Bearer "+bo)
	req.Header.Set("Range", "bytes=2-5")
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != 206 || r2.Header.Get("Content-Type") != "audio/mp4" || r2.Header.Get("Content-Range") != "bytes 2-5/10" {
		t.Fatalf("%d %v", r2.StatusCode, r2.Header)
	}
	if resp, body := do(t, ts, anna, "GET", "/api/v1/episodes/ep000000002/stream", nil); resp.StatusCode != 409 || code(body) != "episode_not_ready" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, ts, anna, "GET", "/api/v1/episodes/ep000000001/stream?kind=video", nil); resp.StatusCode != 404 {
		t.Fatal(resp.StatusCode)
	}
	s.Library.DB.Exec(`UPDATE episode_files SET status='expired', path='' WHERE video_id='ep000000001'`)
	if resp, body := do(t, ts, anna, "GET", "/api/v1/episodes/ep000000001/stream", nil); resp.StatusCode != 410 || code(body) != "episode_expired" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, ts, anna, "PUT", "/api/v1/episodes/ep000000002/hide", nil); resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
	if _, body := do(t, ts, anna, "GET", "/api/v1/episodes/latest", nil); strings.Contains(string(body), "ep000000002") {
		t.Fatal("hidden")
	}
	if resp, _ := do(t, ts, anna, "PUT", "/api/v1/episodes/not-an-id/progress", map[string]any{"position_s": 1}); resp.StatusCode != 400 {
		t.Fatal(resp.StatusCode)
	}
}

func TestEpisodeThumbnail(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	kid := loginAs(t, s, "kid", "member")
	seedEpisode(t, s, "ep000000001", false)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func() *http.Response {
		req, _ := http.NewRequest("GET", ts.URL+"/api/v1/episodes/ep000000001/thumbnail", nil)
		req.Header.Set("Authorization", "Bearer "+kid)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if r := get(); r.StatusCode != 307 || r.Header.Get("Location") != "https://i.ytimg.com/vi/ep000000001/hqdefault.jpg" {
		t.Fatalf("%d %v", r.StatusCode, r.Header)
	}
	os.MkdirAll(filepath.Join(s.Channels.Root, liuxiang), 0o755)
	os.WriteFile(filepath.Join(s.Channels.Root, liuxiang, "ep000000001.jpg"), []byte("\xff\xd8jpg"), 0o644)
	if r := get(); r.StatusCode != 200 || r.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("%d %v", r.StatusCode, r.Header)
	}
}

func TestChannelSearchAndPage(t *testing.T) {
	s, ts := newTestServer(t)
	ch := withChannels(t, s)
	s.YT = &ytdlp.Client{Runner: &fakeYTRunner{outFor: func(args []string) ([]byte, error) {
		if strings.Contains(args[len(args)-1], "results?search_query") {
			return []byte(chanSearchJSON), nil
		}
		return []byte(chanPageJSON), nil
	}}}
	kid := loginAs(t, s, "kid", "member")
	var uid int64
	s.Library.DB.QueryRow(`SELECT id FROM users WHERE username='kid'`).Scan(&uid)
	ch.Follow(context.Background(), uid, ytdlp.Channel{ID: "UCRABK12_6Ie2X549K9cXS0g", Title: "刘翔"})
	resp, body := do(t, ts, kid, "GET", "/api/v1/channels/search?q=%E5%88%98%E7%BF%94", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"id":"UCRABK12_6Ie2X549K9cXS0g","title":"刘翔","handle":"","avatar":"","description":"","followers":0,"following":true`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	// Unknown channel: resolved through yt-dlp, stored, then shown (feed read: none here).
	resp, body = do(t, ts, kid, "GET", "/api/v1/channels/"+liuxiang, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"following":null`) || !strings.Contains(string(body), "刘翔的投资频道") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, ts, kid, "GET", "/api/v1/channels/not-a-channel", nil); resp.StatusCode != 400 {
		t.Fatal(resp.StatusCode)
	}
}

func TestChannelSuggestionsAPI(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	kid := loginAs(t, s, "kid", "member")
	resp, body := do(t, ts, kid, "GET", "/api/v1/me/channel-suggestions", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"channels":[]`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, ts, kid, "POST", "/api/v1/me/channel-suggestions/refresh", nil); resp.StatusCode != 202 {
		t.Fatal(resp.StatusCode)
	}
	if resp, _ := do(t, ts, kid, "POST", "/api/v1/me/channel-suggestions/refresh", nil); resp.StatusCode != 202 {
		t.Fatal("a second request joins the pending one")
	}
	if resp, _ := do(t, ts, kid, "PUT", "/api/v1/me/channel-suggestions/channels/"+liuxiang+"/dismiss", nil); resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
	if resp, _ := do(t, ts, kid, "PUT", "/api/v1/me/channel-suggestions/videos/nope/dismiss", nil); resp.StatusCode != 400 {
		t.Fatal(resp.StatusCode)
	}
}

// Past the daily budget the refusal says so (not "a few minutes").
func TestChannelSuggestionsRefreshTooSoonText(t *testing.T) {
	s, ts := newTestServer(t)
	withChannels(t, s)
	kid := loginAs(t, s, "kid", "member")
	var uid int64
	s.Library.DB.QueryRow(`SELECT id FROM users WHERE username='kid'`).Scan(&uid)
	if _, err := s.Library.DB.Exec(`INSERT INTO discovery_users(user_id,day,mixes) VALUES (?,?,1000)`, uid, time.Now().Format(time.DateOnly)); err != nil {
		t.Fatal(err)
	}
	resp, body := do(t, ts, kid, "POST", "/api/v1/me/channel-suggestions/refresh", nil)
	if resp.StatusCode != 429 || code(body) != "too_soon" ||
		!strings.Contains(string(body), "suggestions were refreshed too often today; try again tomorrow") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

// 最新 sort/filter and 按频道 are per user and behind sign-in.
func TestLatestOrderUnplayedAndByChannelAPI(t *testing.T) {
	s, ts := newTestServer(t)
	ch := withChannels(t, s)
	anna, bo := loginAs(t, s, "anna", "member"), loginAs(t, s, "bo", "member")
	ids := map[string]int64{}
	for _, u := range []string{"anna", "bo"} {
		var id int64
		s.Library.DB.QueryRow(`SELECT id FROM users WHERE username=?`, u).Scan(&id)
		ids[u] = id
	}
	seedEpisode(t, s, "ep000000001", true)
	seedEpisode(t, s, "ep000000002", true)
	s.Library.DB.Exec(`UPDATE episodes SET published_at=published_at-100 WHERE video_id='ep000000001'`)
	if _, err := ch.Follow(context.Background(), ids["anna"], ytdlp.Channel{ID: liuxiang}); err != nil {
		t.Fatal(err)
	}
	do(t, ts, anna, "PUT", "/api/v1/episodes/ep000000002/progress", map[string]any{"position_s": 0, "played": true})
	order := func(body []byte) string {
		var r struct{ Items []struct{ VideoID string `json:"video_id"` } }
		json.Unmarshal(body, &r)
		out := []string{}
		for _, it := range r.Items {
			out = append(out, it.VideoID)
		}
		return strings.Join(out, ",")
	}
	if _, body := do(t, ts, anna, "GET", "/api/v1/episodes/latest", nil); order(body) != "ep000000002,ep000000001" {
		t.Fatal(string(body))
	}
	if _, body := do(t, ts, anna, "GET", "/api/v1/episodes/latest?order=asc", nil); order(body) != "ep000000001,ep000000002" {
		t.Fatal(string(body))
	}
	if _, body := do(t, ts, anna, "GET", "/api/v1/episodes/latest?unplayed=1", nil); order(body) != "ep000000001" {
		t.Fatal(string(body))
	}
	if resp, body := do(t, ts, anna, "GET", "/api/v1/episodes/latest?order=sideways", nil); resp.StatusCode != 400 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	resp, body := do(t, ts, anna, "GET", "/api/v1/episodes/by-channel?per=1", nil)
	var g struct {
		Groups []struct {
			Channel  struct{ ID string }
			Unplayed int
			Episodes []struct{ VideoID string `json:"video_id"` }
		}
	}
	json.Unmarshal(body, &g)
	if resp.StatusCode != 200 || len(g.Groups) != 1 || g.Groups[0].Channel.ID != liuxiang || g.Groups[0].Unplayed != 1 ||
		len(g.Groups[0].Episodes) != 1 || g.Groups[0].Episodes[0].VideoID != "ep000000002" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if _, body := do(t, ts, bo, "GET", "/api/v1/episodes/by-channel", nil); string(body) != `{"groups":[]}`+"\n" && string(body) != `{"groups":[]}` {
		t.Fatalf("bo follows nothing: %s", body)
	}
	if resp, _ := do(t, ts, "", "GET", "/api/v1/episodes/by-channel", nil); resp.StatusCode != 401 {
		t.Fatalf("signed out: %d", resp.StatusCode)
	}
}
