package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/aaronsuns/lark-server/internal/artwork"
	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/download"
	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/lyrics"
	"github.com/aaronsuns/lark-server/internal/media"
	"github.com/aaronsuns/lark-server/internal/personal"
	"github.com/aaronsuns/lark-server/internal/prefs"
	"github.com/aaronsuns/lark-server/internal/radio"
	"github.com/aaronsuns/lark-server/internal/tags"
	"github.com/aaronsuns/lark-server/internal/testutil"
	"github.com/aaronsuns/lark-server/internal/trash"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// oneVideoSearchJSON is a canned flat-playlist JSON response describing one
// video, reused by fakeYTRunner for both a search and a URL resolve (the
// entries' fields don't need to match the request args either way).
const oneVideoSearchJSON = `{"_type":"playlist","entries":[
	{"id":"v1_0000000x","title":"测试视频","channel":"测试频道","duration":120,
	 "url":"https://www.youtube.com/watch?v=v1_0000000x","thumbnail":"https://i.ytimg.com/vi/v1_0000000x/default.jpg"}
]}`

// fakeYTRunner is a ytdlp.Runner whose Output always answers out (or err),
// recording every call — and, when delay is set, how many calls were ever
// in flight at once — so tests can assert on cache hits, call counts and
// concurrency without ever touching a real yt-dlp binary. Only a brief
// bookkeeping section is held under mu; the (optional) delay happens outside
// it, so concurrent Output calls actually overlap in time rather than being
// serialised by the fake itself.
type fakeYTRunner struct {
	mu          sync.Mutex
	out         []byte
	err         error
	outFor      func(args []string) ([]byte, error) // when set, answers instead of out/err
	delay       time.Duration                       // if set, Output waits this long (or ctx.Done()) before answering
	calls       [][]string
	inflight    int
	maxInflight int
}

func (f *fakeYTRunner) Output(ctx context.Context, args []string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.inflight++
	if f.inflight > f.maxInflight {
		f.maxInflight = f.inflight
	}
	delay, out, err, outFor := f.delay, f.out, f.err, f.outFor
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inflight--
		f.mu.Unlock()
	}()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if outFor != nil {
		return outFor(args)
	}
	return out, err
}

func (f *fakeYTRunner) Stream(context.Context, []string, func(string)) error {
	return errors.New("fakeYTRunner: Stream not used by these tests")
}

func (f *fakeYTRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeYTRunner) maxInflightCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxInflight
}

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	d := testutil.DB(t)
	s := &Server{
		Auth:     &auth.Store{DB: d},
		Library:  &library.Store{DB: d},
		Personal: &personal.Store{DB: d},
		Radio:    &radio.Radio{DB: d},
		Prefs:    &prefs.Store{DB: d},
		Language: "en",
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	libStore := s.Library // after constructing s
	s.Tags = &tags.Store{DB: d, OnChange: s.Library.Reindex}
	s.Vocab = tags.Vocabulary()
	s.Trash = &trash.Service{DB: d, Library: libStore, Log: s.Log}
	s.Scans = &library.Service{Store: libStore, Scanner: &library.Scanner{Store: libStore, Prober: media.FFprobe{Path: "ffprobe"}, Log: s.Log}, Log: s.Log}
	s.Lyrics = &lyrics.Service{DB: d, Library: libStore, Log: s.Log}
	s.Artwork = &artwork.Service{DB: d, Library: libStore, Imager: artwork.FFmpeg{Path: "ffmpeg"}, Dir: filepath.Join(t.TempDir(), "artwork"), Log: s.Log}
	s.YT = &ytdlp.Client{Runner: &fakeYTRunner{out: []byte(oneVideoSearchJSON)}}
	s.Downloads = &download.Service{DB: d, YT: s.YT, Library: libStore, Scans: s.Scans, Log: s.Log, Workers: 2}
	s.YtDlpBin = filepath.Join(t.TempDir(), "bin", "yt-dlp") // never exists in tests
	s.YtDlpPath = "yt-dlp"
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

// seedTrack inserts a track row directly (no file on disk) in library "main"
// rooted at a temp dir, and indexes it.
func seedTrack(t *testing.T, s *Server, rel, title, artist, album string) int64 {
	t.Helper()
	ctx := context.Background()
	lib, err := s.Library.EnsureLibrary(ctx, "main", filepath.Join(os.TempDir(), "lark-test-lib"), false)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Library.DB.ExecContext(ctx, `INSERT INTO tracks(library_id,rel_path,size,mtime,fingerprint,duration_ms,codec,bitrate,
		tag_title,tag_artist,tag_album,status,added_at) VALUES (?,?,1,1,?,200000,'mp3',192,?,?,?,'kept',1)`,
		lib.ID, rel, rel, title, artist, album)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if err := s.Library.Reindex(ctx, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func loginAs(t *testing.T, s *Server, username string, role auth.Role) string {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Auth.CreateUser(ctx, username, "pw", role); err != nil && err != auth.ErrExists {
		t.Fatal(err)
	}
	tok, _, err := s.Auth.Login(ctx, username, "pw", "test")
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func do(t *testing.T, ts *httptest.Server, token, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, ts.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
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

// Every API error body is {"error": "<English message>", "code": "<stable
// code>"} — the web client translates known codes itself and otherwise shows
// the English message, so the message must never be Chinese.
func TestErrorBodiesCarryStableCodes(t *testing.T) {
	s, ts := newTestServer(t)
	kid := loginAs(t, s, "kid", "member")
	cases := []struct {
		method, path, tok string
		body              any
		status            int
		code              string
	}{
		{"POST", "/api/v1/downloads", kid, map[string]string{"url": "https://evil.com/x"}, 400, "youtube_only"},
		{"GET", "/api/v1/nope", kid, nil, 404, "not_found"},
		{"GET", "/api/v1/users", kid, nil, 403, "admin_only"},
		{"GET", "/api/v1/me", "", nil, 401, "not_signed_in"},
		{"POST", "/api/v1/auth/login", "", map[string]string{"username": "kid", "password": "nope", "device_name": "x"}, 401, "wrong_credentials"},
	}
	for _, c := range cases {
		r, b := do(t, ts, c.tok, c.method, c.path, c.body)
		var e struct{ Error, Code string }
		json.Unmarshal(b, &e)
		if r.StatusCode != c.status || e.Code != c.code || e.Error == "" || strings.ContainsFunc(e.Error, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			t.Errorf("%s %s → %d %s", c.method, c.path, r.StatusCode, b)
		}
	}
}
