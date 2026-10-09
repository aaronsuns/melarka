package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBrowseEndpoints(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "kid", "member")
	id := seedTrack(t, s, "邓丽君/甜蜜蜜.mp3", "甜蜜蜜", "邓丽君", "精选")
	seedTrack(t, s, "x/faded.mp3", "Faded", "Alan Walker", "Different World")

	resp, body := do(t, ts, tok, "GET", "/api/v1/tracks?sort=title&limit=1", nil)
	var page struct {
		Items      []map[string]any
		NextCursor string `json:"next_cursor"`
	}
	json.Unmarshal(body, &page)
	if resp.StatusCode != 200 || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("tracks %d %s", resp.StatusCode, body)
	}

	resp, body = do(t, ts, tok, "GET", fmt.Sprintf("/api/v1/tracks/%d", id), nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "甜蜜蜜") {
		t.Fatalf("track %s", body)
	}
	resp, _ = do(t, ts, tok, "GET", "/api/v1/tracks/999999", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("missing track code=%d", resp.StatusCode)
	}

	for _, p := range []string{"/api/v1/albums", "/api/v1/artists", "/api/v1/tags", "/api/v1/libraries"} {
		if resp, body := do(t, ts, tok, "GET", p, nil); resp.StatusCode != 200 {
			t.Fatalf("%s %d %s", p, resp.StatusCode, body)
		}
	}
	resp, body = do(t, ts, tok, "GET", "/api/v1/search?q=dlj", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "甜蜜蜜") || strings.Contains(string(body), "Faded") {
		t.Fatalf("search %s", body)
	}
	resp, _ = do(t, ts, tok, "GET", "/api/v1/search?q=", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("empty search code=%d", resp.StatusCode)
	}
}

func TestRandomRouteIsNotShadowedByTrackID(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "kid", "member")
	seedTrack(t, s, "a.mp3", "A", "X", "Y")
	resp, body := do(t, ts, tok, "GET", "/api/v1/tracks/random?n=5", nil)
	if resp.StatusCode != 200 || !strings.HasPrefix(strings.TrimSpace(string(body)), "[") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

func TestRandomSourceFavoritesShape(t *testing.T) {
	s, ts := newTestServer(t)
	kid := loginAs(t, s, "kid", "member")
	seedTrack(t, s, "a.mp3", "A", "X", "Y")
	r, b := do(t, ts, kid, "GET", "/api/v1/tracks/random?source=favorites&n=5", nil)
	if r.StatusCode != 200 || !strings.Contains(string(b), `"source":"all"`) || !strings.Contains(string(b), `"tracks":[`) {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	if _, b := do(t, ts, kid, "GET", "/api/v1/tracks/random?n=5", nil); !strings.HasPrefix(strings.TrimSpace(string(b)), "[") {
		t.Fatalf("plain form must stay an array: %s", b)
	}
}

// Every track carries gain_db: the attenuation computed from its measured
// loudness, or null while it has not been measured.
func TestTracksCarryGainDB(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "member")
	loud := seedTrack(t, s, "loud.mp3", "Loud", "X", "Y")
	quiet := seedTrack(t, s, "unmeasured.mp3", "Unmeasured", "X", "Y")
	if _, err := s.Library.DB.Exec(`UPDATE tracks SET loudness_lufs=-9, true_peak_db=-0.5, loudness_checked_at=1 WHERE id=?`, loud); err != nil {
		t.Fatal(err)
	}
	_, body := do(t, ts, tok, "GET", "/api/v1/tracks", nil)
	var page struct{ Items []map[string]json.RawMessage }
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range page.Items {
		var id int64
		json.Unmarshal(it["id"], &id)
		got[fmt.Sprint(id)] = string(it["gain_db"])
	}
	if got[fmt.Sprint(loud)] != "-5" || got[fmt.Sprint(quiet)] != "null" {
		t.Fatalf("gain_db: %v in %s", got, body)
	}
	if !strings.Contains(string(body), `"gain_db":-5`) || !strings.Contains(string(body), `"gain_db":null`) {
		t.Fatalf("raw JSON %s", body)
	}
	if _, b := do(t, ts, tok, "GET", fmt.Sprintf("/api/v1/tracks/%d", loud), nil); !strings.Contains(string(b), `"gain_db":-5`) {
		t.Fatalf("single track %s", b)
	}
}
