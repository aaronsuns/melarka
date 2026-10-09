package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorded is one request an unofficial-provider fake server saw.
type recorded struct {
	Method, Path string
	Query        url.Values
	Header       http.Header
	Body         string
}

// fakeSite serves handler(path) → fixture name (or "" for 404) and records
// every request. fixtures: map of request path → (status, fixture file).
type fakeSite struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []recorded
}

type reply struct {
	code    int
	fixture string // file under testdata/ without .json; "" → raw
	raw     string
}

func newFakeSite(t *testing.T, routes map[string]reply) *fakeSite {
	t.Helper()
	f := &fakeSite{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, recorded{r.Method, r.URL.Path, r.URL.Query(), r.Header.Clone(), string(b)})
		f.mu.Unlock()
		rp, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if rp.code != 0 {
			w.WriteHeader(rp.code)
		}
		if rp.fixture != "" {
			data, err := os.ReadFile("testdata/" + rp.fixture + ".json")
			if err != nil {
				t.Error(err)
			}
			w.Write(data)
			return
		}
		io.WriteString(w, rp.raw)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeSite) on(path string) []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recorded
	for _, r := range f.reqs {
		if r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

var tianmimi = Query{Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211}

// ---- NetEase

func netease(f *fakeSite) *NetEase { return &NetEase{HTTP: f.Client(), BaseURL: f.URL} }

func TestNetEaseSearch(t *testing.T) {
	f := newFakeSite(t, map[string]reply{
		"/api/cloudsearch/pc": {fixture: "netease_search"},
		"/api/song/lyric":     {fixture: "netease_lyric"},
	})
	cs, err := netease(f).Search(context.Background(), tianmimi)
	if err != nil || len(cs) != 2 {
		t.Fatalf("%+v %v", cs, err)
	}
	if cs[0].Source != "netease" || cs[0].ExternalID != "204666" || cs[1].ExternalID != "26608741" ||
		!cs[0].Synced || cs[0].Title != "甜蜜蜜" || cs[0].Artist != "邓丽君" || cs[0].DurationS != 211 {
		t.Fatalf("%+v", cs)
	}
	s := f.on("/api/cloudsearch/pc")
	if len(s) != 1 || s[0].Query.Get("s") != "甜蜜蜜 邓丽君" || s[0].Query.Get("type") != "1" || s[0].Query.Get("limit") != "5" {
		t.Fatalf("%+v", s)
	}
	if s[0].Header.Get("Referer") != "https://music.163.com/" || s[0].Header.Get("User-Agent") != "Mozilla/5.0" {
		t.Fatalf("headers %v", s[0].Header)
	}
	ly := f.on("/api/song/lyric")
	if len(ly) != 2 || ly[0].Query.Get("id") != "204666" || ly[0].Query.Get("lv") != "1" || ly[0].Query.Get("tv") != "-1" {
		t.Fatalf("the DJ version must be filtered before its lyric is fetched: %+v", ly)
	}
}

func TestNetEaseRawQueryIsPercentEncoded(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		io.WriteString(w, `{"code":200,"result":{"songs":[]}}`)
	}))
	defer srv.Close()
	if _, err := (&NetEase{HTTP: srv.Client(), BaseURL: srv.URL}).Search(context.Background(), tianmimi); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "s=%E7%94%9C%E8%9C%9C%E8%9C%9C+%E9%82%93%E4%B8%BD%E5%90%9B") && !strings.Contains(raw, "s=%E7%94%9C%E8%9C%9C%E8%9C%9C%20%E9%82%93%E4%B8%BD%E5%90%9B") {
		t.Fatalf("raw query %q", raw)
	}
}

func TestNetEaseCaptchaWallIsAnError(t *testing.T) {
	f := newFakeSite(t, map[string]reply{"/api/cloudsearch/pc": {fixture: "netease_verify"}})
	cs, err := netease(f).Search(context.Background(), tianmimi)
	if err == nil || !strings.Contains(err.Error(), "-462") || len(cs) != 0 {
		t.Fatalf("%+v %v", cs, err)
	}
}

func TestNetEaseNoSongsIsACleanMiss(t *testing.T) {
	f := newFakeSite(t, map[string]reply{"/api/cloudsearch/pc": {raw: `{"code":200,"result":{"songCount":0}}`}})
	cs, err := netease(f).Search(context.Background(), tianmimi)
	if err != nil || len(cs) != 0 {
		t.Fatalf("%+v %v", cs, err)
	}
}

func TestNetEaseSkipsPureMusicAndEmpty(t *testing.T) {
	for _, body := range []string{
		`{"code":200,"pureMusic":true,"lrc":{"lyric":"[00:01.00]a\n[00:02.00]b\n[00:03.00]c"}}`,
		`{"code":200,"nolyric":true}`,
		`{"code":200,"lrc":{"lyric":"  "}}`,
	} {
		f := newFakeSite(t, map[string]reply{
			"/api/cloudsearch/pc": {fixture: "netease_search"},
			"/api/song/lyric":     {raw: body},
		})
		cs, err := netease(f).Search(context.Background(), tianmimi)
		if err != nil || len(cs) != 0 {
			t.Fatalf("%s: %+v %v", body, cs, err)
		}
	}
}

func TestNetEaseGarbageBody(t *testing.T) {
	f := newFakeSite(t, map[string]reply{"/api/cloudsearch/pc": {raw: "<html>blocked</html>"}})
	if _, err := netease(f).Search(context.Background(), tianmimi); err == nil {
		t.Fatal("expected an error")
	}
}

// ---- QQ

func qq(f *fakeSite) *QQ {
	return &QQ{HTTP: f.Client(), SearchURL: f.URL + "/cgi-bin/musicu.fcg", LyricURL: f.URL + "/lyric/fcgi-bin/fcg_query_lyric_new.fcg"}
}

func qqRoutes() map[string]reply {
	return map[string]reply{
		"/cgi-bin/musicu.fcg":                     {fixture: "qq_search"},
		"/lyric/fcgi-bin/fcg_query_lyric_new.fcg": {fixture: "qq_lyric"},
	}
}

func TestQQSearch(t *testing.T) {
	f := newFakeSite(t, qqRoutes())
	cs, err := qq(f).Search(context.Background(), tianmimi)
	if err != nil || len(cs) != 2 {
		t.Fatalf("%+v %v", cs, err)
	}
	c := cs[0]
	if c.Source != "qq" || c.ExternalID != "000sdZNg1W94eK" || !c.Synced || c.Title != "甜蜜蜜" || c.Artist != "邓丽君" || c.DurationS != 211 {
		t.Fatalf("%+v", c)
	}
	if strings.Contains(c.Text, "&#58;") || !strings.Contains(c.Text, "[ti:甜蜜蜜]") {
		t.Fatalf("lyric not unescaped: %q", c.Text)
	}
	if lines, _ := ParseLRC(c.Text); len(lines) != 3 {
		t.Fatalf("lines %v", lines)
	}
	s := f.on("/cgi-bin/musicu.fcg")
	if len(s) != 1 || s[0].Method != "POST" || !strings.HasPrefix(s[0].Header.Get("Content-Type"), "application/json") {
		t.Fatalf("%+v", s)
	}
	var body struct {
		Comm map[string]any `json:"comm"`
		Req  struct {
			Method, Module string
			Param          struct {
				Grp        int    `json:"grp"`
				NumPerPage int    `json:"num_per_page"`
				PageNum    int    `json:"page_num"`
				Query      string `json:"query"`
				SearchType int    `json:"search_type"`
			}
		} `json:"req"`
	}
	if err := json.Unmarshal([]byte(s[0].Body), &body); err != nil {
		t.Fatal(err)
	}
	if body.Req.Method != "DoSearchForQQMusicDesktop" || body.Req.Module != "music.search.SearchCgiService" ||
		body.Req.Param.Query != "甜蜜蜜 邓丽君" || body.Req.Param.NumPerPage != 5 || body.Req.Param.PageNum != 1 ||
		body.Req.Param.Grp != 1 || body.Comm["cv"] != float64(1859) || body.Comm["uin"] != "0" {
		t.Fatalf("%s", s[0].Body)
	}
	ly := f.on("/lyric/fcgi-bin/fcg_query_lyric_new.fcg")
	if len(ly) != 2 {
		t.Fatalf("%+v", ly)
	}
	q := ly[0].Query
	if q.Get("songmid") != "000sdZNg1W94eK" || q.Get("format") != "json" || q.Get("nobase64") != "1" || q.Get("g_tk") != "5381" ||
		ly[0].Header.Get("Referer") != "https://y.qq.com/" {
		t.Fatalf("%+v", ly[0])
	}
}

func TestQQRetcodeSkipsAndGarbage(t *testing.T) {
	r := qqRoutes()
	r["/lyric/fcgi-bin/fcg_query_lyric_new.fcg"] = reply{raw: `{"retcode":-1901,"code":-1901}`}
	f := newFakeSite(t, r)
	cs, err := qq(f).Search(context.Background(), tianmimi)
	if err != nil || len(cs) != 0 {
		t.Fatalf("%+v %v", cs, err)
	}
	r["/cgi-bin/musicu.fcg"] = reply{raw: "<html>nope</html>"}
	f = newFakeSite(t, r)
	if _, err := qq(f).Search(context.Background(), tianmimi); err == nil {
		t.Fatal("expected an error")
	}
	// An API refusal on the search itself is an error, not a miss.
	r["/cgi-bin/musicu.fcg"] = reply{raw: `{"code":500001,"req":{"code":2001}}`}
	f = newFakeSite(t, r)
	if _, err := qq(f).Search(context.Background(), tianmimi); err == nil {
		t.Fatal("expected an error")
	}
	r["/cgi-bin/musicu.fcg"] = reply{raw: `{"code":0,"req":{"code":0,"data":{"body":{"song":{"list":[]}}}}}`}
	f = newFakeSite(t, r)
	if cs, err := qq(f).Search(context.Background(), tianmimi); err != nil || len(cs) != 0 {
		t.Fatalf("%+v %v", cs, err)
	}
}

// ---- Kugou

func kugou(f *fakeSite) *Kugou {
	return &Kugou{HTTP: f.Client(), SearchURL: f.URL + "/api/v3/search/song", CandidatesURL: f.URL + "/search", DownloadURL: f.URL + "/download"}
}

func kugouRoutes() map[string]reply {
	return map[string]reply{
		"/api/v3/search/song": {fixture: "kugou_search"},
		"/search":             {fixture: "kugou_candidates"},
		"/download":           {fixture: "kugou_download"},
	}
}

func TestKugouSearch(t *testing.T) {
	f := newFakeSite(t, kugouRoutes())
	q := Query{Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 209}
	cs, err := kugou(f).Search(context.Background(), q)
	if err != nil || len(cs) != 1 {
		t.Fatalf("%+v %v", cs, err)
	}
	c := cs[0]
	if c.Source != "kugou" || c.ExternalID != "8bbb308fa5357a01c85e4d3787751d82" || !c.Synced || c.Title != "甜蜜蜜" || c.Artist != "邓丽君" || c.DurationS != 209 {
		t.Fatalf("%+v", c)
	}
	if !strings.HasPrefix(c.Text, "[ar:") {
		t.Fatalf("BOM not stripped: %q", c.Text)
	}
	s := f.on("/api/v3/search/song")
	if len(s) != 1 || s[0].Query.Get("keyword") != "甜蜜蜜 邓丽君" || s[0].Query.Get("pagesize") != "5" || s[0].Query.Get("format") != "json" {
		t.Fatalf("%+v", s)
	}
	cd := f.on("/search")
	if len(cd) != 1 || cd[0].Query.Get("duration") != "209000" || cd[0].Query.Get("hash") != "8bbb308fa5357a01c85e4d3787751d82" ||
		cd[0].Query.Get("man") != "yes" || cd[0].Query.Get("client") != "mobi" {
		t.Fatalf("%+v", cd)
	}
	dl := f.on("/download")
	if len(dl) != 1 || dl[0].Query.Get("fmt") != "lrc" || dl[0].Query.Get("id") != "163707630" ||
		dl[0].Query.Get("accesskey") != "FAKEACCESSKEY0000000000000000000" || dl[0].Query.Get("charset") != "utf8" || dl[0].Query.Get("client") != "pc" {
		t.Fatalf("%+v", dl)
	}
}

func TestKugouFailures(t *testing.T) {
	q := Query{Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 209}
	// no candidates / download refused → clean miss
	r := kugouRoutes()
	r["/search"] = reply{raw: `{"status":200,"candidates":[]}`}
	if cs, err := kugou(newFakeSite(t, r)).Search(context.Background(), q); err != nil || len(cs) != 0 {
		t.Fatalf("%+v %v", cs, err)
	}
	r = kugouRoutes()
	r["/download"] = reply{raw: `{"status":404,"info":"not found"}`}
	if cs, err := kugou(newFakeSite(t, r)).Search(context.Background(), q); err != nil || len(cs) != 0 {
		t.Fatalf("%+v %v", cs, err)
	}
	// garbage search body, or an API refusal → error
	r = kugouRoutes()
	r["/api/v3/search/song"] = reply{raw: "<html>x</html>"}
	if _, err := kugou(newFakeSite(t, r)).Search(context.Background(), q); err == nil {
		t.Fatal("expected an error")
	}
	r["/api/v3/search/song"] = reply{raw: `{"status":0,"errcode":20010,"error":"bad"}`}
	if _, err := kugou(newFakeSite(t, r)).Search(context.Background(), q); err == nil {
		t.Fatal("expected an error")
	}
	// undecodable download content → error
	r = kugouRoutes()
	r["/download"] = reply{raw: `{"status":200,"content":"!!!not base64"}`}
	if _, err := kugou(newFakeSite(t, r)).Search(context.Background(), q); err == nil {
		t.Fatal("expected an error")
	}
}

func TestKugouNeverFetchesNonMatchingHash(t *testing.T) {
	f := newFakeSite(t, kugouRoutes())
	kugou(f).Search(context.Background(), Query{Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 209})
	for _, r := range f.on("/search") {
		if r.Query.Get("hash") == "e8db7bce002a25b09a6a67aafc61ef3f" {
			t.Fatal("DJ hash requested")
		}
	}
}

// ---- shared behaviour

func TestUnofficialProvidersFailWhenServerIsGone(t *testing.T) {
	for name, mk := range map[string]func(*fakeSite) Provider{
		"netease": func(f *fakeSite) Provider { return netease(f) },
		"qq":      func(f *fakeSite) Provider { return qq(f) },
		"kugou":   func(f *fakeSite) Provider { return kugou(f) },
	} {
		f := newFakeSite(t, nil)
		p := mk(f)
		f.Close()
		if cs, err := p.Search(context.Background(), tianmimi); err == nil || len(cs) != 0 {
			t.Errorf("%s: %+v %v", name, cs, err)
		}
	}
}

func TestUnofficialProvidersHonourContext(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	ps := map[string]Provider{
		"netease": &NetEase{HTTP: srv.Client(), BaseURL: srv.URL},
		"qq":      &QQ{HTTP: srv.Client(), SearchURL: srv.URL, LyricURL: srv.URL},
		"kugou":   &Kugou{HTTP: srv.Client(), SearchURL: srv.URL, CandidatesURL: srv.URL, DownloadURL: srv.URL},
	}
	for name, p := range ps {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		start := time.Now()
		_, err := p.Search(ctx, tianmimi)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
			t.Errorf("%s: %v after %v", name, err, time.Since(start))
		}
	}
}

func TestUnofficialProvidersEmptyTitle(t *testing.T) {
	for _, p := range []Provider{&NetEase{}, &QQ{}, &Kugou{}} {
		if cs, err := p.Search(context.Background(), Query{Artist: "x"}); err != nil || cs != nil {
			t.Errorf("%s: %v %v", p.Name(), cs, err)
		}
	}
}

func TestBuildUnofficialProviders(t *testing.T) {
	names := []string{"embedded", "lrclib", "netease", "qq", "kugou"}
	ps, err := Build(names, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range ps {
		got = append(got, p.Name())
		if p.Name() != "embedded" && isLocal(p) {
			t.Errorf("%s must not be Local", p.Name())
		}
	}
	if !slices.Equal(got, names) {
		t.Fatalf("%v", got)
	}
	if _, err := Build([]string{"spotify"}, Deps{}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestNetEaseLyricRefusalIsAnError(t *testing.T) {
	f := newFakeSite(t, map[string]reply{
		"/api/cloudsearch/pc": {fixture: "netease_search"},
		"/api/song/lyric":     {fixture: "netease_verify"},
	})
	if cs, err := netease(f).Search(context.Background(), tianmimi); err == nil || !strings.Contains(err.Error(), "-462") || len(cs) != 0 {
		t.Fatalf("%+v %v", cs, err)
	}
}

func TestQQSearchWithoutListIsAnError(t *testing.T) {
	for _, body := range []string{`{}`, `{"code":0,"req":{"code":0}}`} {
		f := newFakeSite(t, map[string]reply{"/cgi-bin/musicu.fcg": {raw: body}})
		if _, err := qq(f).Search(context.Background(), tianmimi); err == nil {
			t.Fatalf("%s: expected an error", body)
		}
	}
}

// A search that works but whose lyric requests all fail is an error, not a miss.
func TestUnofficialLyricFailureWithoutCandidatesIsAnError(t *testing.T) {
	for _, bad := range []reply{{code: 500, raw: "boom"}, {raw: "<html>x</html>"}} {
		nr := map[string]reply{"/api/cloudsearch/pc": {fixture: "netease_search"}, "/api/song/lyric": bad}
		if cs, err := netease(newFakeSite(t, nr)).Search(context.Background(), tianmimi); err == nil || len(cs) != 0 {
			t.Errorf("netease %+v: %+v %v", bad, cs, err)
		}
		qr := qqRoutes()
		qr["/lyric/fcgi-bin/fcg_query_lyric_new.fcg"] = bad
		if cs, err := qq(newFakeSite(t, qr)).Search(context.Background(), tianmimi); err == nil || len(cs) != 0 {
			t.Errorf("qq %+v: %+v %v", bad, cs, err)
		}
		kr := kugouRoutes()
		kr["/search"] = bad
		kq := Query{Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 209}
		if cs, err := kugou(newFakeSite(t, kr)).Search(context.Background(), kq); err == nil || len(cs) != 0 {
			t.Errorf("kugou candidates %+v: %+v %v", bad, cs, err)
		}
		kr = kugouRoutes()
		kr["/download"] = bad
		if cs, err := kugou(newFakeSite(t, kr)).Search(context.Background(), kq); err == nil || len(cs) != 0 {
			t.Errorf("kugou download %+v: %+v %v", bad, cs, err)
		}
	}
}

// ---- Songs (shared with the artwork providers)

func TestNetEaseSongs(t *testing.T) {
	f := newFakeSite(t, map[string]reply{"/api/cloudsearch/pc": {fixture: "netease_search"}})
	ss, err := netease(f).Songs(context.Background(), SearchTerm(tianmimi))
	if err != nil || len(ss) == 0 {
		t.Fatalf("%+v %v", ss, err)
	}
	s := ss[0]
	if s.ID != "204666" || s.Title != "甜蜜蜜" || s.Artist != "邓丽君" || s.DurationS != 211 || s.Album == "" {
		t.Fatalf("%+v", s)
	}
	for _, s := range ss {
		if s.AlbumPic != "" { // the lyrics fixture has no picUrl
			t.Fatalf("pic %+v", s)
		}
	}
	f2 := newFakeSite(t, map[string]reply{"/api/cloudsearch/pc": {raw: `{"code":-462}`}})
	if _, err := netease(f2).Songs(context.Background(), "x"); err == nil {
		t.Fatal("captcha wall accepted")
	}
}

func TestQQSongs(t *testing.T) {
	f := newFakeSite(t, qqRoutes())
	ss, err := qq(f).Songs(context.Background(), SearchTerm(tianmimi))
	if err != nil || len(ss) == 0 {
		t.Fatalf("%+v %v", ss, err)
	}
	if ss[0].ID != "000sdZNg1W94eK" || ss[0].Title != "甜蜜蜜" || ss[0].Artist != "邓丽君" || ss[0].DurationS != 211 {
		t.Fatalf("%+v", ss[0])
	}
	for _, s := range ss {
		if s.AlbumMID != "" { // the lyrics fixture has no album object
			t.Fatalf("mid %+v", s)
		}
	}
}
