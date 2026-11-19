package api

import (
	"encoding/json"
	"testing"
)

func TestAdminVideoSourcesRoundTrip(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	var got struct {
		Sources []string `json:"sources"`
		SetBy   string   `json:"set_by"`
	}
	r, b := do(t, ts, adm, "GET", "/api/v1/admin/video-sources", nil)
	if r.StatusCode != 200 || json.Unmarshal(b, &got) != nil || got.SetBy != "env" || len(got.Sources) != 1 || got.Sources[0] != "youtube" {
		t.Fatalf("default: %d %s", r.StatusCode, b)
	}
	r, b = do(t, ts, adm, "PUT", "/api/v1/admin/video-sources", map[string]any{"sources": []string{"bilibili", "youtube"}})
	if r.StatusCode != 200 || json.Unmarshal(b, &got) != nil || got.SetBy != "admin" || len(got.Sources) != 2 || got.Sources[0] != "bilibili" {
		t.Fatalf("put: %d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, adm, "PUT", "/api/v1/admin/video-sources", map[string]any{"sources": []string{"vimeo"}}); r.StatusCode != 400 {
		t.Fatalf("unknown: %d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, adm, "PUT", "/api/v1/admin/video-sources", map[string]any{}); r.StatusCode != 400 {
		t.Fatalf("missing sources: %d %s", r.StatusCode, b)
	}
}

func TestInfoListsVideoSources(t *testing.T) {
	s, ts := newTestServer(t)
	info := func() []string {
		r, b := do(t, ts, "", "GET", "/api/v1/info", nil)
		var got struct {
			VideoSources []string `json:"video_sources"`
		}
		if r.StatusCode != 200 || json.Unmarshal(b, &got) != nil || got.VideoSources == nil {
			t.Fatalf("info: %d %s", r.StatusCode, b)
		}
		return got.VideoSources
	}
	if v := info(); len(v) != 1 || v[0] != "youtube" {
		t.Fatalf("default %v", v)
	}
	adm := loginAs(t, s, "dad", "admin")
	if r, _ := do(t, ts, adm, "PUT", "/api/v1/admin/video-sources", map[string]any{"sources": []string{}}); r.StatusCode != 200 {
		t.Fatal("put empty")
	}
	if v := info(); len(v) != 0 {
		t.Fatalf("after none: %v", v)
	}
}
