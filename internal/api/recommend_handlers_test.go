package api

import (
	"encoding/json"
	"testing"

	"github.com/aaronsuns/lark-server/internal/recommend"
)

func withRecs(t *testing.T, s *Server) {
	t.Helper()
	s.Recs = &recommend.Service{DB: s.Library.DB, Library: s.Library, YT: s.YT, Gate: s.SearchGate(), Log: s.Log}
}

func addRec(t *testing.T, s *Server, user int64, video string, score float64, reason int64) {
	t.Helper()
	var r any
	if reason != 0 {
		r = reason
	}
	if _, err := s.Library.DB.Exec(`INSERT INTO recommendations(user_id,video_id,title,channel,duration_s,thumbnail,score,reason_track_id,reason_kind,created_at)
		VALUES (?,?,?,?,200,?,?,?,'favorite',1)`, user, video, "T "+video, "C", "https://i.ytimg.com/vi/"+video+"/hqdefault.jpg", score, r); err != nil {
		t.Fatal(err)
	}
}

func userID(t *testing.T, s *Server, name string) int64 {
	t.Helper()
	var id int64
	if err := s.Library.DB.QueryRow(`SELECT id FROM users WHERE username=?`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

type recsBody struct {
	Items []struct {
		ID        string  `json:"video_id"`
		DurationS int     `json:"duration_s"`
		U         string  `json:"url"`
		Score     float64 `json:"score"`
		Reason    *struct {
			TrackID int64  `json:"track_id"`
			Title   string `json:"title"`
			Artist  string `json:"artist"`
			Kind    string `json:"kind"`
		} `json:"reason"`
	} `json:"items"`
	RefreshedAt *int64 `json:"refreshed_at"`
	Refreshing  bool   `json:"refreshing"`
	Enabled     bool   `json:"enabled"`
}

// Each user sees, refreshes and dismisses only their own recommendations.
func TestRecommendationsAPIOwnDataOnly(t *testing.T) {
	s, ts := newTestServer(t)
	withRecs(t, s)
	a, b := loginAs(t, s, "anna", "member"), loginAs(t, s, "bert", "member")
	aid, bid := userID(t, s, "anna"), userID(t, s, "bert")
	seed := seedTrack(t, s, "x/甜蜜蜜.mp3", "甜蜜蜜", "邓丽君", "精选")
	addRec(t, s, aid, "annavideo01", 2, seed)
	addRec(t, s, aid, "annavideo02", 1, 0)
	addRec(t, s, bid, "bertvideo01", 1, 0)

	r, body := do(t, ts, a, "GET", "/api/v1/me/recommendations", nil)
	var got recsBody
	json.Unmarshal(body, &got)
	if r.StatusCode != 200 || len(got.Items) != 2 || got.Items[0].ID != "annavideo01" || !got.Enabled || got.Refreshing {
		t.Fatalf("%d %s", r.StatusCode, body)
	}
	if rs := got.Items[0].Reason; rs == nil || rs.TrackID != seed || rs.Title != "甜蜜蜜" || rs.Artist != "邓丽君" || rs.Kind != "favorite" ||
		got.Items[0].U != "https://www.youtube.com/watch?v=annavideo01" || got.Items[0].DurationS != 200 {
		t.Fatalf("%s", body)
	}
	if got.Items[1].Reason != nil {
		t.Fatalf("no seed, no reason: %s", body)
	}

	// Bert dismissing Anna's video changes nothing for Anna.
	if r, body := do(t, ts, b, "PUT", "/api/v1/me/recommendations/annavideo01/dismiss", nil); r.StatusCode != 204 {
		t.Fatalf("%d %s", r.StatusCode, body)
	}
	_, body = do(t, ts, b, "GET", "/api/v1/me/recommendations", nil)
	json.Unmarshal(body, &got)
	if len(got.Items) != 1 || got.Items[0].ID != "bertvideo01" {
		t.Fatalf("bert sees %s", body)
	}
	_, body = do(t, ts, a, "GET", "/api/v1/me/recommendations", nil)
	json.Unmarshal(body, &got)
	if len(got.Items) != 2 {
		t.Fatalf("anna lost a row to bert's dismissal: %s", body)
	}
	if r, _ := do(t, ts, a, "PUT", "/api/v1/me/recommendations/annavideo01/dismiss", nil); r.StatusCode != 204 {
		t.Fatal(r.StatusCode)
	}
	_, body = do(t, ts, a, "GET", "/api/v1/me/recommendations", nil)
	json.Unmarshal(body, &got)
	if len(got.Items) != 1 || got.Items[0].ID != "annavideo02" {
		t.Fatalf("%s", body)
	}

	// Refresh: 202; again at once: 202 too (deduped, no time limit).
	if r, body := do(t, ts, a, "POST", "/api/v1/me/recommendations/refresh", nil); r.StatusCode != 202 {
		t.Fatalf("%d %s", r.StatusCode, body)
	}
	_, body = do(t, ts, a, "GET", "/api/v1/me/recommendations", nil)
	json.Unmarshal(body, &got)
	if !got.Refreshing {
		t.Fatalf("not refreshing: %s", body)
	}
	if r, body := do(t, ts, a, "POST", "/api/v1/me/recommendations/refresh", nil); r.StatusCode != 202 {
		t.Fatalf("second refresh: %d %s", r.StatusCode, body)
	}
	if r, _ := do(t, ts, b, "GET", "/api/v1/me/recommendations", nil); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	if r, body := do(t, ts, b, "POST", "/api/v1/me/recommendations/refresh", nil); r.StatusCode != 202 {
		t.Fatalf("bert limited by anna: %d %s", r.StatusCode, body)
	}
}

func TestRecommendationsAPIErrors(t *testing.T) {
	s, ts := newTestServer(t)
	withRecs(t, s)
	kid := loginAs(t, s, "kid", "member")
	do(t, ts, kid, "POST", "/api/v1/me/recommendations/refresh", nil)
	cases := []struct {
		method, path, tok string
		status            int
		code              string
	}{
		{"PUT", "/api/v1/me/recommendations/bad!/dismiss", kid, 400, "bad_request"},
		{"GET", "/api/v1/me/recommendations", "", 401, "not_signed_in"},
		{"POST", "/api/v1/me/recommendations/refresh", "", 401, "not_signed_in"},
		{"PUT", "/api/v1/me/recommendations/annavideo01/dismiss", "", 401, "not_signed_in"},
	}
	for _, c := range cases {
		r, b := do(t, ts, c.tok, c.method, c.path, nil)
		var e struct{ Error, Code string }
		json.Unmarshal(b, &e)
		if r.StatusCode != c.status || e.Code != c.code || e.Error == "" {
			t.Errorf("%s %s → %d %s", c.method, c.path, r.StatusCode, b)
		}
	}
}

// Recommendations switched off (config): an empty list says so; refresh is refused.
func TestRecommendationsAPIDisabled(t *testing.T) {
	s, ts := newTestServer(t)
	kid := loginAs(t, s, "kid", "member")
	r, body := do(t, ts, kid, "GET", "/api/v1/me/recommendations", nil)
	var got recsBody
	json.Unmarshal(body, &got)
	if r.StatusCode != 200 || got.Enabled || got.Items == nil || len(got.Items) != 0 {
		t.Fatalf("%d %s", r.StatusCode, body)
	}
	r, body = do(t, ts, kid, "POST", "/api/v1/me/recommendations/refresh", nil)
	var e struct{ Code string }
	json.Unmarshal(body, &e)
	if r.StatusCode != 409 || e.Code != "recommendations_off" {
		t.Fatalf("%d %s", r.StatusCode, body)
	}
}
