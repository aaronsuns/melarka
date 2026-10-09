package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInfoIsPublic(t *testing.T) {
	s, ts := newTestServer(t)
	s.Language = "zh-Hans"
	r, b := do(t, ts, "", "GET", "/api/v1/info", nil)
	if r.StatusCode != 200 || !strings.Contains(string(b), `"language":"zh-Hans"`) || !strings.Contains(string(b), `"sv"`) || !strings.Contains(string(b), `"name":"Melarka"`) || !strings.Contains(string(b), `"version":"dev"`) {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
}

func TestPreferencesRoundTrip(t *testing.T) {
	s, ts := newTestServer(t)
	kid := loginAs(t, s, "kid", "member")
	other := loginAs(t, s, "mum", "member")
	if _, b := do(t, ts, kid, "GET", "/api/v1/me/preferences", nil); !strings.Contains(string(b), `"on_open":"shuffle_favorites"`) || !strings.Contains(string(b), `"car_lyrics":true`) || !strings.Contains(string(b), `"normalize_loudness":true`) {
		t.Fatalf("default %s", b)
	}
	r, b := do(t, ts, kid, "PUT", "/api/v1/me/preferences", map[string]any{"language": "zh-Hant", "on_open": "resume"})
	if r.StatusCode != 200 || !strings.Contains(string(b), `"language":"zh-Hant"`) {
		t.Fatalf("put %d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, kid, "PUT", "/api/v1/me/preferences", map[string]any{"language": "zh-Hant", "on_open": "resume", "car_lyrics": false}); r.StatusCode != 200 || !strings.Contains(string(b), `"car_lyrics":false`) {
		t.Fatalf("put car_lyrics %d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, kid, "PUT", "/api/v1/me/preferences", map[string]any{"language": "zh-Hant", "on_open": "resume", "normalize_loudness": false}); r.StatusCode != 200 || !strings.Contains(string(b), `"normalize_loudness":false`) || !strings.Contains(string(b), `"car_lyrics":false`) {
		t.Fatalf("put normalize_loudness %d %s", r.StatusCode, b)
	}
	if _, b := do(t, ts, other, "GET", "/api/v1/me/preferences", nil); strings.Contains(string(b), "zh-Hant") {
		t.Fatal("leaked to another user")
	}
	if r, _ := do(t, ts, kid, "PUT", "/api/v1/me/preferences", map[string]any{"language": "fr", "on_open": "resume"}); r.StatusCode != 400 {
		t.Fatalf("bad language %d", r.StatusCode)
	}
	if r, _ := do(t, ts, "", "GET", "/api/v1/me/preferences", nil); r.StatusCode != 401 {
		t.Fatalf("anonymous %d", r.StatusCode)
	}
}

func TestInfoReportsTheOfflineCacheSwitch(t *testing.T) {
	s, ts := newTestServer(t)
	if _, b := do(t, ts, "", "GET", "/api/v1/info", nil); !strings.Contains(string(b), `"offline_cache":true`) {
		t.Fatalf("default %s", b)
	}
	s.OfflineCacheOff = true
	if _, b := do(t, ts, "", "GET", "/api/v1/info", nil); !strings.Contains(string(b), `"offline_cache":false`) {
		t.Fatalf("off %s", b)
	}
}

// Off, /sw.js is a worker that removes itself (the remote kill switch):
// with no fetch handler, its caches deleted, and unregistered.
func TestOfflineCacheOffServesTheKillWorker(t *testing.T) {
	s, _ := newTestServer(t)
	s.OfflineCacheOff = true
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	r, b := do(t, ts, "", "GET", "/sw.js", nil)
	body := string(b)
	if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Type"), "javascript") || r.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("%d %q %q", r.StatusCode, r.Header.Get("Content-Type"), r.Header.Get("Cache-Control"))
	}
	if !strings.Contains(body, "unregister()") || !strings.Contains(body, "caches.delete") || strings.Contains(body, `"fetch"`) {
		t.Fatalf("not a kill worker: %s", body)
	}
}

// The web app hides 频道 when the server has Channels off.
func TestInfoReportsChannels(t *testing.T) {
	s, ts := newTestServer(t)
	if _, b := do(t, ts, "", "GET", "/api/v1/info", nil); !strings.Contains(string(b), `"channels":false`) {
		t.Fatalf("off %s", b)
	}
	withChannels(t, s)
	if _, b := do(t, ts, "", "GET", "/api/v1/info", nil); !strings.Contains(string(b), `"channels":true`) {
		t.Fatalf("on %s", b)
	}
}
