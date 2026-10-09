package api

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/media"
	"github.com/aaronsuns/lark-server/internal/stream"
	"github.com/aaronsuns/lark-server/internal/testutil"
)

func TestStreamRangeAndTiers(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "kid", "member")
	ctx := context.Background()
	root := t.TempDir()
	testutil.Sample(t, root, "song.wav")
	lib, _ := s.Library.EnsureLibrary(ctx, "real", root, false)
	sc := &library.Scanner{Store: s.Library, Prober: media.FFprobe{Path: "ffprobe"}, Log: s.Log}
	if _, err := sc.Scan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	var id int64
	s.Library.DB.QueryRow(`SELECT id FROM tracks WHERE rel_path='song.wav'`).Scan(&id)
	s.Stream = &stream.Service{Library: s.Library, Cache: stream.NewCache(filepath.Join(t.TempDir(), "c"), 1<<30, 1, stream.ExecRunner{FFmpeg: "ffmpeg"}), Log: s.Log}

	get := func(q, rng string) *http.Response {
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/tracks/%d/stream%s", ts.URL, id, q), nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if r := get("?quality=lossless", ""); r.StatusCode != 200 || r.Header.Get("Content-Type") != "audio/flac" {
		t.Fatalf("lossless %d %s", r.StatusCode, r.Header.Get("Content-Type"))
	}
	if r := get("?quality=high", "bytes=0-99"); r.StatusCode != 206 || r.Header.Get("Content-Type") != "audio/mp4" {
		t.Fatalf("high range %d %s", r.StatusCode, r.Header.Get("Content-Type"))
	}
	if r := get("?quality=bogus", ""); r.StatusCode != 400 {
		t.Fatalf("bogus tier %d", r.StatusCode)
	}
	s.Library.DB.Exec(`UPDATE tracks SET status='trashed' WHERE id=?`, id)
	if r := get("", ""); r.StatusCode != 404 {
		t.Fatalf("trashed %d", r.StatusCode)
	}
}

// A track whose row exists
// but whose file doesn't (e.g. removed between scan and stream) must 404,
// not 500.
func TestStreamMissingFileReturns404(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "kid", "member")
	id := seedTrack(t, s, "missing.mp3", "T", "A", "Al") // mp3/192kbps: passthrough at the default (lossless) tier

	s.Stream = &stream.Service{Library: s.Library, Cache: stream.NewCache(filepath.Join(t.TempDir(), "c"), 1<<30, 1, stream.ExecRunner{FFmpeg: "ffmpeg"}), Log: s.Log}

	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/tracks/%d/stream", ts.URL, id), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("missing file: got %d, want 404", resp.StatusCode)
	}
}

// blockingRunner blocks Run until release is closed, letting a test hold a
// transcode job "in flight" deterministically.
type blockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingRunner) Run(_ context.Context, args []string) error {
	close(b.started)
	<-b.release
	return os.WriteFile(args[len(args)-1], []byte("x"), 0o644)
}

func withRouteID(req *http.Request, id int64) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(id, 10))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// A client that's already gone
// (context cancelled — the phone skipped to the next track) must not be
// logged as a server failure or get a response body written for it.
func TestStreamClientDisconnectNotLoggedAsFailure(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	lib, err := s.Library.EnsureLibrary(ctx, "main2", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Library.DB.ExecContext(ctx, `INSERT INTO tracks(library_id,rel_path,size,mtime,fingerprint,duration_ms,codec,bitrate,status,added_at)
		VALUES (?,?,1,1,'fp-wma',200000,'wmav2',192,'kept',1)`, lib.ID, "slow.wma") // wmav2 always needs a transcode
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()

	var logBuf bytes.Buffer
	s.Log = slog.New(slog.NewTextHandler(&logBuf, nil))
	br := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	defer close(br.release)
	s.Stream = &stream.Service{Library: s.Library, Cache: stream.NewCache(filepath.Join(t.TempDir(), "c"), 1<<30, 1, br), Log: s.Log}

	// A first request starts the transcode job and holds the only slot.
	go func() {
		w0 := httptest.NewRecorder()
		req0 := withRouteID(httptest.NewRequest("GET", "/x", nil), id)
		s.streamTrack(w0, req0)
	}()
	<-br.started // the job is now running (blocked on release)

	// A second request for the same track starts live so its StreamInfo
	// lookup succeeds normally, then queues behind the in-flight transcode
	// (same key: same track, same default tier) and gets cancelled while
	// still waiting there — the client gave up mid-request, same as a phone
	// skipping to the next track.
	reqCtx, cancel := context.WithCancel(context.Background())
	req := withRouteID(httptest.NewRequest("GET", "/x", nil).WithContext(reqCtx), id)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.streamTrack(w, req)
	}()
	time.Sleep(100 * time.Millisecond) // let the lookup finish and the call start queuing on the cache
	cancel()
	<-done

	if logBuf.Len() != 0 {
		t.Fatalf("expected no error log for a disconnected client, got: %s", logBuf.String())
	}
	if w.Body.Len() != 0 {
		t.Fatalf("expected no response body for a disconnected client, got %q", w.Body.String())
	}
}

// The offline cache keeps a copy per track and later checks it is still
// current: the stream says which version of the source file it is.
func TestStreamCarriesTheSourceVersion(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "kid", "member")
	ctx := context.Background()
	root := t.TempDir()
	testutil.Sample(t, root, "song.wav")
	lib, _ := s.Library.EnsureLibrary(ctx, "real", root, false)
	sc := &library.Scanner{Store: s.Library, Prober: media.FFprobe{Path: "ffprobe"}, Log: s.Log}
	if _, err := sc.Scan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	var id int64
	s.Library.DB.QueryRow(`SELECT id FROM tracks WHERE rel_path='song.wav'`).Scan(&id)
	s.Stream = &stream.Service{Library: s.Library, Cache: stream.NewCache(filepath.Join(t.TempDir(), "c"), 1<<30, 1, stream.ExecRunner{FFmpeg: "ffmpeg"}), Log: s.Log}
	version := func() string {
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/tracks/%d/stream?quality=high", ts.URL, id), nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Range", "bytes=0-0")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 206 || resp.Header.Get("ETag") != "" {
			t.Fatalf("%d etag=%q", resp.StatusCode, resp.Header.Get("ETag"))
		}
		return resp.Header.Get("X-Lark-Version")
	}
	v := version()
	if v == "" || version() != v {
		t.Fatalf("version %q", v)
	}
	s.Library.DB.Exec(`UPDATE tracks SET fingerprint='changed0000000000' WHERE id=?`, id)
	if w := version(); w == v || w == "" {
		t.Fatalf("changed file: %q", w)
	}
}

// The player asks the server to have the next few tracks ready: any
// signed-in user (members too) may, the ids are bounded, and a passthrough
// track is never transcoded.
func TestPrepareTracks(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "kid", "member")
	s.Stream = &stream.Service{Library: s.Library, Cache: stream.NewCache(filepath.Join(t.TempDir(), "c"), 1<<30, 1, stream.ExecRunner{FFmpeg: "ffmpeg"}), Log: s.Log}
	s.Prepare = &stream.Preparer{Service: s.Stream, Runner: stream.ExecRunner{FFmpeg: "ffmpeg", Nice: true}, Max: 8, Log: s.Log}
	id := seedTrack(t, s, "p.mp3", "T", "A", "Al")

	resp, body := do(t, ts, tok, "POST", "/api/v1/tracks/prepare", map[string]any{"ids": []int64{id, id}, "quality": "high"})
	if resp.StatusCode != 202 || !bytes.Contains(body, []byte(`"queued":1`)) {
		t.Fatalf("prepare %d %s", resp.StatusCode, body)
	}
	many := make([]int64, 20)
	for i := range many {
		many[i] = int64(1000 + i)
	}
	resp, body = do(t, ts, tok, "POST", "/api/v1/tracks/prepare", map[string]any{"ids": many, "quality": "high"})
	if resp.StatusCode != 202 || !bytes.Contains(body, []byte(`"queued":5`)) {
		t.Fatalf("bounded prepare %d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, ts, tok, "POST", "/api/v1/tracks/prepare", map[string]any{"ids": []int64{id}, "quality": "bogus"}); resp.StatusCode != 400 {
		t.Fatalf("bogus quality %d", resp.StatusCode)
	}
	if resp, _ := do(t, ts, "", "POST", "/api/v1/tracks/prepare", map[string]any{"ids": []int64{id}}); resp.StatusCode != 401 {
		t.Fatalf("anonymous %d", resp.StatusCode)
	}
	s.Prepare = nil // not configured: accepted, nothing done
	if resp, _ := do(t, ts, tok, "POST", "/api/v1/tracks/prepare", map[string]any{"ids": []int64{id}, "quality": "high"}); resp.StatusCode != 202 {
		t.Fatalf("no preparer %d", resp.StatusCode)
	}
}

// Each user may ask for prepares at most 10 times a minute.
func TestPrepareTracksIsRateLimited(t *testing.T) {
	s, ts := newTestServer(t)
	kid := loginAs(t, s, "kid", "member")
	other := loginAs(t, s, "other", "member")
	body := map[string]any{"ids": []int64{1}, "quality": "high"}
	for i := 0; i < 10; i++ {
		if resp, _ := do(t, ts, kid, "POST", "/api/v1/tracks/prepare", body); resp.StatusCode != 202 {
			t.Fatalf("call %d: %d", i, resp.StatusCode)
		}
	}
	if resp, _ := do(t, ts, kid, "POST", "/api/v1/tracks/prepare", body); resp.StatusCode != 429 {
		t.Fatalf("11th call: %d, want 429", resp.StatusCode)
	}
	if resp, _ := do(t, ts, other, "POST", "/api/v1/tracks/prepare", body); resp.StatusCode != 202 {
		t.Fatalf("another user: %d", resp.StatusCode)
	}
}
