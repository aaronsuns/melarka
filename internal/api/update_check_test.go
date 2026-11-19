package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aaronsuns/lark-server/internal/updatecheck"
)

func TestUpdateCheckEndpointAdminOnly(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v9.9.9","html_url":"https://github.com/aaronsuns/melarka/releases/tag/v9.9.9"}`))
	}))
	t.Cleanup(gh.Close)
	s, ts := newTestServer(t)
	s.UpdateCheck = &updatecheck.Service{URL: gh.URL, Version: "0.2.23"}
	admin := loginAs(t, s, "boss", "admin")
	kid := loginAs(t, s, "kid", "member")

	if resp, _ := do(t, ts, kid, "GET", "/api/v1/admin/update-check", nil); resp.StatusCode != 403 {
		t.Fatalf("member: %d", resp.StatusCode)
	}
	// Before the first check: known current version, nothing newer.
	resp, b := do(t, ts, admin, "GET", "/api/v1/admin/update-check", nil)
	var got updatecheck.Result
	if resp.StatusCode != 200 || json.Unmarshal(b, &got) != nil || got.Newer || got.Current != "0.2.23" || !got.Enabled {
		t.Fatalf("before: %d %s", resp.StatusCode, b)
	}
	if err := s.UpdateCheck.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, b = do(t, ts, admin, "GET", "/api/v1/admin/update-check", nil)
	got = updatecheck.Result{}
	if json.Unmarshal(b, &got) != nil || !got.Newer || got.Latest != "9.9.9" || got.CheckedAt == 0 ||
		got.URL != "https://github.com/aaronsuns/melarka/releases/tag/v9.9.9" {
		t.Fatalf("after: %s", b)
	}
	// The endpoint only reads the cache.
	s.UpdateCheck.Off = true
	_, b = do(t, ts, admin, "GET", "/api/v1/admin/update-check", nil)
	got = updatecheck.Result{}
	if json.Unmarshal(b, &got) != nil || got.Enabled || got.Newer {
		t.Fatalf("off: %s", b)
	}
}
