package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aaronsuns/lark-server/internal/library"
)

func jsonHas(b []byte, s string) bool { return strings.Contains(string(b), s) }

func uidOf(t *testing.T, s *Server, username string) int64 {
	t.Helper()
	var id int64
	if err := s.Library.DB.QueryRow(`SELECT id FROM users WHERE username=?`, username).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedDoneDownload(t *testing.T, s *Server, owner int64, video string, track, at int64) {
	t.Helper()
	res, err := s.Library.DB.Exec(`INSERT INTO downloads(user_id,url,video_id,status,track_id,created_at,updated_at)
		VALUES (?,?,?,'done',?,?,?)`, owner, "https://www.youtube.com/watch?v="+video, video, track, at, at)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := s.Library.DB.Exec(`INSERT INTO download_requests(download_id,user_id,created_at) VALUES (?,?,?)`, id, owner, at); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadTracksEndpoint(t *testing.T) {
	s, ts := newTestServer(t)
	kid := loginAs(t, s, "kid", "member")
	adm := loginAs(t, s, "dad", "admin")
	kidID, admID := uidOf(t, s, "kid"), uidOf(t, s, "dad")
	t1 := seedTrack(t, s, "yt/one.m4a", "One", "Chan", "")
	t2 := seedTrack(t, s, "yt/two.m4a", "Two", "Chan", "")
	seedDoneDownload(t, s, kidID, "v1_0000000x", t1, 10)
	seedDoneDownload(t, s, admID, "v2_0000000x", t2, 20)
	ids := func(b []byte) []int64 {
		var ts []library.Track
		if err := json.Unmarshal(b, &ts); err != nil {
			t.Fatalf("%v: %s", err, b)
		}
		out := []int64{}
		for _, x := range ts {
			out = append(out, x.ID)
		}
		return out
	}
	if r, b := do(t, ts, kid, "GET", "/api/v1/downloads/tracks", nil); r.StatusCode != 200 || fmt.Sprint(ids(b)) != fmt.Sprint([]int64{t1}) {
		t.Fatalf("member own: %d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, kid, "GET", fmt.Sprintf("/api/v1/downloads/tracks?user=%d", kidID), nil); r.StatusCode != 200 || len(ids(b)) != 1 {
		t.Fatalf("member own by id: %d %s", r.StatusCode, b)
	}
	for _, q := range []string{fmt.Sprintf("?user=%d", admID), "?user=all"} {
		r, b := do(t, ts, kid, "GET", "/api/v1/downloads/tracks"+q, nil)
		if r.StatusCode != 403 || !jsonHas(b, `"code":"own_downloads_only"`) {
			t.Fatalf("member %s: %d %s", q, r.StatusCode, b)
		}
	}
	if r, _ := do(t, ts, kid, "GET", "/api/v1/downloads/tracks?user=abc", nil); r.StatusCode != 400 {
		t.Fatalf("bad user param: %d", r.StatusCode)
	}
	if r, b := do(t, ts, adm, "GET", fmt.Sprintf("/api/v1/downloads/tracks?user=%d", kidID), nil); r.StatusCode != 200 || fmt.Sprint(ids(b)) != fmt.Sprint([]int64{t1}) {
		t.Fatalf("admin other: %d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, adm, "GET", "/api/v1/downloads/tracks?user=all", nil); r.StatusCode != 200 || fmt.Sprint(ids(b)) != fmt.Sprint([]int64{t2, t1}) {
		t.Fatalf("admin all: %d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, adm, "GET", "/api/v1/downloads/tracks?user=999", nil); r.StatusCode != 200 || len(ids(b)) != 0 {
		t.Fatalf("admin unknown user: %d %s", r.StatusCode, b)
	}
	if r, _ := do(t, ts, "", "GET", "/api/v1/downloads/tracks", nil); r.StatusCode != 401 {
		t.Fatalf("no token: %d", r.StatusCode)
	}
}
