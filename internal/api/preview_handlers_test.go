package api

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/preview"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// instantDL "downloads" a 10-byte preview at once — or fails with err.
type instantDL struct {
	channelID string
	err       error
}

func (d instantDL) DownloadPreview(ctx context.Context, v ytdlp.Video, destNoExt string, kind ytdlp.MediaKind, onMeta func(ytdlp.PreviewMeta), _ func(float64)) (string, error) {
	if d.err != nil {
		return "", d.err
	}
	onMeta(ytdlp.PreviewMeta{Size: 10, ChannelID: d.channelID, Title: "T"})
	p := destNoExt + ".m4a"
	return p, os.WriteFile(p, []byte("0123456789"), 0o644)
}

func (d instantDL) DownloadPreviewHD(ctx context.Context, v ytdlp.Video, destNoExt string, onProgress func(float64), onMeta func(ytdlp.PreviewMeta)) (string, error) {
	if d.err != nil {
		return "", d.err
	}
	onMeta(ytdlp.PreviewMeta{ChannelID: d.channelID, Title: "T"})
	onProgress(100)
	p := destNoExt + ".mp4"
	return p, os.WriteFile(p, []byte("0123456789"), 0o644)
}

func withPreviews(t *testing.T, s *Server, channelID string) *preview.Service {
	t.Helper()
	ch := withChannels(t, s)
	s.Previews = &preview.Service{DB: s.Library.DB, YT: instantDL{channelID: channelID}, Music: s.Downloads, Episodes: ch, Root: t.TempDir(), Log: s.Log}
	return s.Previews
}

func waitPreviewStatus(t *testing.T, s *Server, id int64, want string) {
	t.Helper()
	for range 200 {
		if p, _ := s.Previews.Get(context.Background(), id); p.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("preview never %s", want)
}

func waitPreviewDone(t *testing.T, s *Server, id int64) {
	t.Helper()
	waitPreviewStatus(t, s, id, "done")
}

// waitPreviewsIdle: every preview download has finished.
func waitPreviewsIdle(t *testing.T, s *Server) {
	t.Helper()
	for range 200 {
		if !s.Previews.Downloading() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("preview downloads never finished")
}

func TestPreviewAPI(t *testing.T) {
	s, ts := newTestServer(t)
	withPreviews(t, s, liuxiang)
	kid := loginAs(t, s, "kid", "member")
	resp, body := do(t, ts, kid, "POST", "/api/v1/previews", map[string]any{"video_id": "pv_00000001", "media": "audio", "title": "Song", "channel": "Chan", "duration_s": 200})
	if resp.StatusCode != 201 || !strings.Contains(string(body), `"stream_url":"/api/v1/previews/1/stream"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	waitPreviewDone(t, s, 1)
	if resp, body := do(t, ts, kid, "GET", "/api/v1/previews/1/stream", nil); resp.StatusCode != 200 || string(body) != "0123456789" || resp.Header.Get("Content-Type") != "audio/mp4" {
		t.Fatalf("%d %q %v", resp.StatusCode, body, resp.Header)
	}
	if resp, body := do(t, ts, kid, "POST", "/api/v1/previews", map[string]any{"video_id": "pv_00000002", "media": "flac"}); resp.StatusCode != 400 || code(body) != "bad_media" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if resp, body := do(t, ts, kid, "POST", "/api/v1/previews", map[string]any{"video_id": "pv_00000003", "media": "hd"}); resp.StatusCode != 201 || !strings.Contains(string(body), `"media":"hd"`) {
		t.Fatalf("hd: %d %s", resp.StatusCode, body)
	}
	if resp, body := do(t, ts, kid, "POST", "/api/v1/previews", map[string]any{"video_id": "pv_00000004", "media": "4k"}); resp.StatusCode != 400 || code(body) != "bad_media" {
		t.Fatalf("4k: %d %s", resp.StatusCode, body)
	}
	// No download target library: keeping as music says so; keeping to Channels works.
	if resp, body := do(t, ts, kid, "POST", "/api/v1/previews/1/keep", map[string]string{"to": "music"}); resp.StatusCode != 409 || code(body) != "no_download_target" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	resp, body = do(t, ts, kid, "POST", "/api/v1/previews/1/keep", map[string]string{"to": "channel"})
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"episode_id":"pv_00000001"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if _, body := do(t, ts, kid, "GET", "/api/v1/episodes/kept", nil); !strings.Contains(string(body), "pv_00000001") {
		t.Fatal("kept into Channels: " + string(body))
	}
	if resp, _ := do(t, ts, kid, "GET", "/api/v1/previews/1", nil); resp.StatusCode != 404 {
		t.Fatal("a kept preview is gone")
	}
	waitPreviewsIdle(t, s)
}

func TestPreviewRequiresSignIn(t *testing.T) {
	s, ts := newTestServer(t)
	withPreviews(t, s, liuxiang)
	for _, r := range []struct{ method, path string }{
		{"POST", "/api/v1/previews"}, {"GET", "/api/v1/previews/1"}, {"GET", "/api/v1/previews/1/stream"}, {"POST", "/api/v1/previews/1/keep"},
	} {
		if resp, _ := do(t, ts, "", r.method, r.path, nil); resp.StatusCode != 401 {
			t.Errorf("%s %s: %d", r.method, r.path, resp.StatusCode)
		}
	}
}

func TestPreviewLimitsAndCodes(t *testing.T) {
	s, ts := newTestServer(t)
	pv := withPreviews(t, s, "")
	pv.MaxPerUser = 1
	kid := loginAs(t, s, "kid", "member")
	do(t, ts, kid, "POST", "/api/v1/previews", map[string]any{"video_id": "pv_00000001", "media": "audio"})
	if resp, body := do(t, ts, kid, "POST", "/api/v1/previews", map[string]any{"video_id": "pv_00000002", "media": "audio"}); resp.StatusCode != 429 || code(body) != "preview_limit" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	waitPreviewDone(t, s, 1)
	if resp, body := do(t, ts, kid, "POST", "/api/v1/previews/1/keep", map[string]string{"to": "channel"}); resp.StatusCode != 409 || code(body) != "channel_unknown" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, ts, kid, "GET", "/api/v1/previews/99/stream", nil); resp.StatusCode != 404 {
		t.Fatal(resp.StatusCode)
	}
	waitPreviewsIdle(t, s)
	s.Previews = nil
	if resp, body := do(t, ts, kid, "POST", "/api/v1/previews", map[string]any{"video_id": "pv_00000003", "media": "audio"}); resp.StatusCode != 409 || code(body) != "channels_off" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

// Ruling R10: a video without a progressive mp4 → 409 video_preview_unavailable
// (on the stream, and at once when asked again).
func TestPreviewVideoUnavailable(t *testing.T) {
	s, ts := newTestServer(t)
	pv := withPreviews(t, s, liuxiang)
	pv.YT = instantDL{err: errors.New("yt-dlp: exit status 1: ERROR: [youtube] pv_00000001: Requested format is not available. Use --list-formats for a list of available formats")}
	kid := loginAs(t, s, "kid", "member")
	if resp, body := do(t, ts, kid, "POST", "/api/v1/previews", map[string]any{"video_id": "pv_00000001", "media": "video"}); resp.StatusCode != 201 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	waitPreviewStatus(t, s, 1, "failed")
	waitPreviewsIdle(t, s)
	if resp, body := do(t, ts, kid, "GET", "/api/v1/previews/1/stream", nil); resp.StatusCode != 409 || code(body) != "video_preview_unavailable" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if resp, body := do(t, ts, kid, "POST", "/api/v1/previews", map[string]any{"video_id": "pv_00000001", "media": "video"}); resp.StatusCode != 409 || code(body) != "video_preview_unavailable" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

// Ruling R12: YouTube pushing back answers a retryable 503 preview_retry.
func TestPreviewPushBackIsRetryable(t *testing.T) {
	s, ts := newTestServer(t)
	pv := withPreviews(t, s, liuxiang)
	pv.YT = instantDL{err: errors.New("yt-dlp: exit status 1: ERROR: [youtube] pv_00000001: Sign in to confirm you're not a bot")}
	kid := loginAs(t, s, "kid", "member")
	do(t, ts, kid, "POST", "/api/v1/previews", map[string]any{"video_id": "pv_00000001", "media": "audio"})
	waitPreviewsIdle(t, s)
	for _, path := range []string{"/api/v1/previews/1", "/api/v1/previews/1/stream"} {
		if resp, body := do(t, ts, kid, "GET", path, nil); resp.StatusCode != 503 || code(body) != "preview_retry" || resp.Header.Get("Retry-After") == "" {
			t.Fatalf("%s: %d %s %v", path, resp.StatusCode, body, resp.Header)
		}
	}
}

func TestPreviewMoreCodes(t *testing.T) {
	s, ts := newTestServer(t)
	pv := withPreviews(t, s, liuxiang)
	kid := loginAs(t, s, "kid", "member")
	if resp, body := do(t, ts, kid, "POST", "/api/v1/previews", map[string]any{"video_id": "nope", "media": "audio"}); resp.StatusCode != 400 || code(body) != "bad_video_id" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	pv.FreeBytes = func(string) (int64, error) { return 1 << 20, nil }
	if resp, body := do(t, ts, kid, "POST", "/api/v1/previews", map[string]any{"video_id": "pv_00000001", "media": "audio"}); resp.StatusCode != 507 || code(body) != "preview_no_space" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	// Two own timeouts in a row: 409 preview_too_long.
	pv.FreeBytes = nil
	pv.Timeout = 30 * time.Millisecond
	pv.YT = blockingDL{}
	for range 2 {
		do(t, ts, kid, "POST", "/api/v1/previews", map[string]any{"video_id": "pv_00000002", "media": "audio"})
		time.Sleep(10 * time.Millisecond)
		waitPreviewsIdle(t, s)
	}
	if resp, body := do(t, ts, kid, "POST", "/api/v1/previews", map[string]any{"video_id": "pv_00000002", "media": "audio"}); resp.StatusCode != 409 || code(body) != "preview_too_long" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

// blockingDL never finishes on its own (until ctx ends).
type blockingDL struct{}

func (blockingDL) DownloadPreview(ctx context.Context, v ytdlp.Video, destNoExt string, kind ytdlp.MediaKind, onMeta func(ytdlp.PreviewMeta), _ func(float64)) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (blockingDL) DownloadPreviewHD(ctx context.Context, v ytdlp.Video, destNoExt string, onProgress func(float64), onMeta func(ytdlp.PreviewMeta)) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
