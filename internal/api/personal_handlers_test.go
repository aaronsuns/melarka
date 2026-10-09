package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPersonalEndpoints(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "kid", "member")
	other := loginAs(t, s, "dad", "admin")
	id := seedTrack(t, s, "a.mp3", "A", "X", "Y")

	if r, _ := do(t, ts, tok, "PUT", fmt.Sprintf("/api/v1/favorites/%d", id), nil); r.StatusCode != 204 {
		t.Fatalf("fav %d", r.StatusCode)
	}
	_, body := do(t, ts, tok, "GET", "/api/v1/tracks?favorite=1", nil)
	if !strings.Contains(string(body), `"favorite":true`) {
		t.Fatalf("favorites %s", body)
	}
	if r, _ := do(t, ts, tok, "PUT", "/api/v1/dislikes/424242", nil); r.StatusCode != 404 {
		t.Fatalf("dislike unknown %d", r.StatusCode)
	}

	r, body := do(t, ts, tok, "POST", "/api/v1/playlists", map[string]any{"name": "车上", "track_ids": []int64{id}})
	if r.StatusCode != 201 {
		t.Fatalf("create playlist %d %s", r.StatusCode, body)
	}
	var pl struct{ ID int64 }
	json.Unmarshal(body, &pl)
	if r, _ := do(t, ts, other, "GET", fmt.Sprintf("/api/v1/playlists/%d", pl.ID), nil); r.StatusCode != 404 {
		t.Fatalf("other user read %d", r.StatusCode)
	}
	_, body = do(t, ts, tok, "GET", fmt.Sprintf("/api/v1/playlists/%d", pl.ID), nil)
	if !strings.Contains(string(body), `"title":"A"`) {
		t.Fatalf("playlist tracks %s", body)
	}

	ev := map[string]any{"events": []map[string]any{{"client_event_id": "u1", "track_id": id, "started_at": 1, "played_seconds": 150}}}
	_, body = do(t, ts, tok, "POST", "/api/v1/events/play", ev)
	if !strings.Contains(string(body), `"accepted":1`) {
		t.Fatalf("events %s", body)
	}

	r, body = do(t, ts, tok, "PUT", "/api/v1/queue", map[string]any{"track_ids": []int64{id}, "current_index": 0, "position_ms": 1234})
	if r.StatusCode != 200 || !strings.Contains(string(body), `"version":1`) {
		t.Fatalf("queue put %d %s", r.StatusCode, body)
	}
	_, body = do(t, ts, tok, "GET", "/api/v1/queue", nil)
	if !strings.Contains(string(body), `"position_ms":1234`) || !strings.Contains(string(body), `"tracks":[`) {
		t.Fatalf("queue get %s", body)
	}

	_, body = do(t, ts, tok, "GET", "/api/v1/radio/next?n=5", nil)
	if !strings.Contains(string(body), `"title":"A"`) {
		t.Fatalf("radio %s", body)
	}
}

// Review fix Minor-6: an out-of-range queue index is caller-input validation
// (personal.ErrInvalid), not an internal error -- it must come back as a
// 400 with a message via s.personalFail/s.fail's sentinel routing, the same
// path every other handler uses, instead of putQueue's own bespoke
// writeError(400, err.Error()) that would just as happily forward a raw
// internal-error message at 400.
func TestQueueOutOfRangeIsA400WithMessage(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "admin")
	id := seedTrack(t, s, "a.mp3", "A", "X", "Y")

	r, body := do(t, ts, tok, "PUT", "/api/v1/queue", map[string]any{"track_ids": []int64{id}, "current_index": 5, "position_ms": 0})
	if r.StatusCode != 400 {
		t.Fatalf("code=%d body=%s", r.StatusCode, body)
	}
	if !strings.Contains(string(body), "out of range") {
		t.Fatalf("body=%s", body)
	}
}

// Review fix round 1, finding 1: a whitespace-only playlist name must be
// rejected with 400, both on create and on update.
func TestPlaylistNameValidationAPI(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "kid2", "member")

	if r, body := do(t, ts, tok, "POST", "/api/v1/playlists", map[string]any{"name": "   ", "track_ids": []int64{}}); r.StatusCode != 400 {
		t.Fatalf("create whitespace name %d %s", r.StatusCode, body)
	}
	_, body := do(t, ts, tok, "POST", "/api/v1/playlists", map[string]any{"name": "ok", "track_ids": []int64{}})
	var pl struct{ ID int64 }
	json.Unmarshal(body, &pl)
	name := "   "
	if r, body := do(t, ts, tok, "PUT", fmt.Sprintf("/api/v1/playlists/%d", pl.ID), map[string]any{"name": name}); r.StatusCode != 400 {
		t.Fatalf("update whitespace name %d %s", r.StatusCode, body)
	}
}

// Review fix round 1, finding 3: an unknown track id in a playlist write
// must be reported as 404, not 500.
func TestPlaylistUnknownTrackAPI(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "kid3", "member")

	if r, body := do(t, ts, tok, "POST", "/api/v1/playlists", map[string]any{"name": "x", "track_ids": []int64{424242}}); r.StatusCode != 404 {
		t.Fatalf("create unknown track %d %s", r.StatusCode, body)
	}
	id := seedTrack(t, s, "b.mp3", "B", "X", "Y")
	_, body := do(t, ts, tok, "POST", "/api/v1/playlists", map[string]any{"name": "y", "track_ids": []int64{id}})
	var pl struct{ ID int64 }
	json.Unmarshal(body, &pl)
	if r, body := do(t, ts, tok, "PUT", fmt.Sprintf("/api/v1/playlists/%d", pl.ID), map[string]any{"track_ids": []int64{424242}}); r.StatusCode != 404 {
		t.Fatalf("update unknown track %d %s", r.StatusCode, body)
	}
}
