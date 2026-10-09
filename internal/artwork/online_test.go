package artwork

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/lyrics"
)

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var tianmimi = lyrics.Query{Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211}

// fixtureSrv serves one fixture and records the requests.
type fixtureSrv struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
}

func newFixtureSrv(t *testing.T, fixture, contentType string) *fixtureSrv {
	t.Helper()
	f := &fixtureSrv{}
	body := mustRead(t, "testdata/"+fixture)
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, r)
		f.mu.Unlock()
		w.Header().Set("Content-Type", contentType)
		w.Write(body)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fixtureSrv) seen() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*http.Request(nil), f.reqs...)
}

func TestITunesFindsUpscaledArtwork(t *testing.T) {
	srv := newFixtureSrv(t, "itunes_search.json", "text/javascript; charset=utf-8")
	p := &ITunes{BaseURL: srv.URL}
	fs, err := p.Find(context.Background(), tianmimi)
	if err != nil || len(fs) != 2 {
		t.Fatalf("%+v %v", fs, err)
	}
	got := srv.seen()[0]
	q := got.URL.Query()
	if got.URL.Path != "/search" || q.Get("term") != "甜蜜蜜 邓丽君" || q.Get("entity") != "song" || q.Get("media") != "music" || q.Get("limit") != "10" {
		t.Fatalf("request %s", got.URL)
	}
	want := "https://is1-ssl.mzstatic.com/image/thumb/Music211/v4/bf/b2/fe/bfb2febf-d4c8-4a15-5c7a-9e3124c89167/10UMGIM27413.rgb.jpg/1000x1000bb.jpg"
	if fs[0].URL != want || fs[0].Title != "甜蜜蜜" || fs[0].Artist != "Teresa Teng" || fs[0].DurationS != 210 || fs[0].Source != "itunes" {
		t.Fatalf("%+v", fs[0])
	}
	if !strings.HasSuffix(fs[1].URL, "/4711232780276.png/1000x1000bb.jpg") {
		t.Fatalf("png source: %s", fs[1].URL)
	}
	// Both match 甜蜜蜜 / 邓丽君 / 211 s despite "Teresa Teng": durations agree (lyrics.Match).
	for _, f := range fs {
		if !lyrics.Match(tianmimi, lyrics.Candidate{Title: f.Title, Artist: f.Artist, DurationS: f.DurationS}) {
			t.Fatalf("no match: %+v", f)
		}
	}
}

func TestNetEaseFindsAlbumArt(t *testing.T) {
	srv := newFixtureSrv(t, "netease_search.json", "application/json")
	p := &NetEase{Search: &lyrics.NetEase{BaseURL: srv.URL}}
	fs, err := p.Find(context.Background(), tianmimi)
	if err != nil || len(fs) != 3 {
		t.Fatalf("%+v %v", fs, err)
	}
	if fs[0].URL != "https://p2.music.126.net/ESGAkd-5ychYahjz4zw2xA==/93458488378175.jpg?param=1000y1000" ||
		fs[0].DurationS != 211 || fs[0].Source != "netease" || fs[0].ExternalID != "204666" || fs[0].Stream != -1 {
		t.Fatalf("%+v", fs[0])
	}
	r := srv.seen()[0]
	if r.URL.Path != "/api/cloudsearch/pc" || r.URL.Query().Get("s") != "甜蜜蜜 邓丽君" || r.Header.Get("Referer") != "https://music.163.com/" {
		t.Fatalf("request %s %v", r.URL, r.Header)
	}
}

func TestNetEaseRefusalIsAnError(t *testing.T) {
	wall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":-462,"data":{"verifyType":1}}`))
	}))
	defer wall.Close()
	fs, err := (&NetEase{Search: &lyrics.NetEase{BaseURL: wall.URL}}).Find(context.Background(), tianmimi)
	if err == nil || fs != nil {
		t.Fatalf("%+v %v", fs, err)
	}
}

func TestQQFindsAlbumArtAndSkipsEmptyMid(t *testing.T) {
	srv := newFixtureSrv(t, "qq_search.json", "application/json")
	p := &QQ{Search: &lyrics.QQ{SearchURL: srv.URL}}
	fs, err := p.Find(context.Background(), tianmimi)
	if err != nil || len(fs) != 2 {
		t.Fatalf("%+v %v", fs, err)
	}
	if fs[0].URL != "https://y.gtimg.cn/music/photo_new/T002R800x800M000004ZPwjb3C3I63.jpg" || fs[0].Source != "qq" ||
		fs[0].ExternalID != "000sdZNg1W94eK" || fs[0].DurationS != 211 {
		t.Fatalf("%+v", fs[0])
	}
	if strings.Contains(fs[1].URL, "M000.jpg") || !strings.Contains(fs[1].URL, "004e0AxV2fweSv") {
		t.Fatalf("%+v", fs[1])
	}
}

func TestOnlineProvidersGarbageIsAnError(t *testing.T) {
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>captcha</html>"))
	}))
	defer html.Close()
	for _, p := range []Provider{
		&ITunes{BaseURL: html.URL},
		&NetEase{Search: &lyrics.NetEase{BaseURL: html.URL}},
		&QQ{Search: &lyrics.QQ{SearchURL: html.URL}},
	} {
		if fs, err := p.Find(context.Background(), tianmimi); err == nil || fs != nil {
			t.Errorf("%s: %+v %v", p.Name(), fs, err)
		}
	}
}

func TestOnlineProvidersCommonBehaviour(t *testing.T) {
	var hits int
	var mu sync.Mutex
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		select { // a POST body left unread hides the client's disconnect from r.Context()
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	mk := func(u string) []Provider {
		return []Provider{
			&ITunes{BaseURL: u},
			&NetEase{Search: &lyrics.NetEase{BaseURL: u}},
			&QQ{Search: &lyrics.QQ{SearchURL: u}},
		}
	}
	for _, p := range mk(srv.URL) {
		if isLocal(p) {
			t.Errorf("%s is local", p.Name())
		}
		if fs, err := p.Find(context.Background(), lyrics.Query{Artist: "x"}); fs != nil || err != nil {
			t.Errorf("%s empty title: %v %v", p.Name(), fs, err)
		}
	}
	mu.Lock()
	if hits != 0 {
		t.Fatalf("empty title made %d requests", hits)
	}
	mu.Unlock()
	for _, p := range mk(srv.URL) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		start := time.Now()
		_, err := p.Find(ctx, tianmimi)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
			t.Errorf("%s ctx: %v after %v", p.Name(), err, time.Since(start))
		}
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	u := gone.URL
	gone.Close()
	for _, p := range mk(u) {
		if fs, err := p.Find(context.Background(), tianmimi); err == nil || fs != nil {
			t.Errorf("%s gone: %v %v", p.Name(), fs, err)
		}
	}
}

func TestBuildOnlineProviders(t *testing.T) {
	ps, err := Build([]string{"embedded", "folder", "itunes", "netease", "qq"}, Deps{})
	if err != nil || len(ps) != 5 {
		t.Fatalf("%v %v", ps, err)
	}
	for i, n := range []string{"embedded", "folder", "itunes", "netease", "qq"} {
		if ps[i].Name() != n || isLocal(ps[i]) != (i < 2) {
			t.Fatalf("%d: %s local=%v", i, ps[i].Name(), isLocal(ps[i]))
		}
	}
}

func TestITunesThroughTheService(t *testing.T) {
	png := filepath.Join(t.TempDir(), "art.png")
	image(t, png, "purple", 1200, 1200)
	body, _ := os.ReadFile(png)
	var srv *httptest.Server
	var mu sync.Mutex
	var imgPaths []string
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search":
			s := strings.ReplaceAll(string(mustRead(t, "testdata/itunes_search.json")), "https://is1-ssl.mzstatic.com/image/thumb/Music211/v4/bf/b2/fe/bfb2febf-d4c8-4a15-5c7a-9e3124c89167/10UMGIM27413.rgb.jpg/100x100bb.jpg", srv.URL+"/img/100x100bb.jpg")
			s = strings.ReplaceAll(s, "https://is1-ssl.mzstatic.com/image/thumb/Music221/v4/a4/4f/3b/a44f3b4d-f0f5-e10b-d0c7-72e50a8446c0/4711232780276.png/100x100bb.jpg", srv.URL+"/img/100x100bb.jpg")
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Write([]byte(s))
		case strings.HasPrefix(r.URL.Path, "/img/"):
			mu.Lock()
			imgPaths = append(imgPaths, r.URL.Path)
			mu.Unlock()
			if r.URL.Path != "/img/1000x1000bb.jpg" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	e := newArtEnv(t, &ITunes{HTTP: srv.Client(), BaseURL: srv.URL})
	e.svc.HTTP = srv.Client()
	id := e.track(t, "o/x.mp3", "甜蜜蜜", "邓丽君", "")
	p, err := e.svc.Get(e.ctx, id, 1000)
	if err != nil || dims(t, p) != "1000,1000" {
		t.Fatalf("%v", err)
	}
	if found, src, _ := e.lookupRow(t, id); found != 1 || src != "itunes" {
		t.Fatalf("row %d %q", found, src)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(imgPaths) == 0 || imgPaths[0] != "/img/1000x1000bb.jpg" {
		t.Fatalf("image requests %v", imgPaths)
	}
	for _, p := range imgPaths {
		if p == "/img/100x100bb.jpg" {
			t.Fatal("thumbnail was fetched")
		}
	}
}
