package api

import (
	"strings"
	"testing"
)

func TestSearchHistoryEndpoints(t *testing.T) {
	s, ts := newTestServer(t)
	kid := loginAs(t, s, "kid", "member")
	other := loginAs(t, s, "sis", "member")
	if r, b := do(t, ts, kid, "GET", "/api/v1/me/search-history", nil); r.StatusCode != 200 || strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("empty: %d %s", r.StatusCode, b)
	}
	for _, q := range []string{"邓丽君", "甜蜜蜜"} {
		if r, b := do(t, ts, kid, "POST", "/api/v1/me/search-history", map[string]string{"query": q}); r.StatusCode != 204 {
			t.Fatalf("post %q: %d %s", q, r.StatusCode, b)
		}
	}
	if _, b := do(t, ts, kid, "GET", "/api/v1/me/search-history", nil); !strings.Contains(string(b), `["甜蜜蜜","邓丽君"]`) {
		t.Fatalf("order: %s", b)
	}
	if _, b := do(t, ts, other, "GET", "/api/v1/me/search-history", nil); strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("leak: %s", b)
	}
	if r, b := do(t, ts, kid, "POST", "/api/v1/me/search-history", map[string]string{"query": " "}); r.StatusCode != 400 || !strings.Contains(string(b), `"code":"bad_request"`) {
		t.Fatalf("empty query: %d %s", r.StatusCode, b)
	}
	if r, _ := do(t, ts, kid, "DELETE", "/api/v1/me/search-history", nil); r.StatusCode != 204 {
		t.Fatalf("clear: %d", r.StatusCode)
	}
	if _, b := do(t, ts, kid, "GET", "/api/v1/me/search-history", nil); strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("after clear: %s", b)
	}
}
