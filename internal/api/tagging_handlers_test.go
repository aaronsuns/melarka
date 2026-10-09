package api

import (
	"context"
	"strings"
	"testing"
)

func TestTaggingBatchVocabularyOnly(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	a := seedTrack(t, s, "邓丽君精选/a.mp3", "甜蜜蜜", "邓丽君", "精选")
	b := seedTrack(t, s, "b.mp3", "B", "X", "Y")
	_, body := do(t, ts, adm, "GET", "/api/v1/admin/tagging/pending?limit=100", nil)
	if !strings.Contains(string(body), `"folder":"邓丽君精选"`) || !strings.Contains(string(body), `"tags":[`) {
		t.Fatalf("pending not enriched: %s", body)
	}
	r, body := do(t, ts, adm, "PUT", "/api/v1/admin/tagging/batch", []map[string]any{
		{"track_id": a, "tags": []string{"mandopop", "romantic"}}, {"track_id": b, "tags": []string{"vibes"}},
	})
	if r.StatusCode != 400 || !strings.Contains(string(body), `"code":"unknown_tags"`) || !strings.Contains(string(body), "vibes") {
		t.Fatalf("%d %s", r.StatusCode, body)
	}
	if got, _ := s.Tags.ForTrack(context.Background(), a); len(got) != 0 {
		t.Fatalf("rejected batch wrote tags: %v", got)
	}
	r, body = do(t, ts, adm, "PUT", "/api/v1/admin/tagging/batch", []map[string]any{
		{"track_id": a, "tags": []string{"Mandopop", "romantic"}}, {"track_id": b, "tags": []string{}}, {"track_id": 99999, "tags": []string{"sad"}},
	})
	if r.StatusCode != 200 || !strings.Contains(string(body), `"updated":2`) || !strings.Contains(string(body), `"skipped":[99999]`) {
		t.Fatalf("%d %s", r.StatusCode, body)
	}
	_, body = do(t, ts, adm, "GET", "/api/v1/admin/tagging/pending?limit=100", nil)
	if strings.Contains(string(body), `"id":`) {
		t.Fatalf("tagged tracks still pending: %s", body)
	}
	kid := loginAs(t, s, "kid", "member")
	if _, body := do(t, ts, kid, "GET", "/api/v1/tracks?tag=mandopop", nil); !strings.Contains(string(body), "甜蜜蜜") {
		t.Fatalf("tag filter: %s", body)
	}
	if _, body := do(t, ts, kid, "GET", "/api/v1/tags/vocabulary", nil); !strings.Contains(string(body), `{"slug":"mandopop","kind":"genre"}`) {
		t.Fatalf("vocabulary: %s", body)
	}
}

func TestTaggingBatchLimits(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	if r, _ := do(t, ts, adm, "PUT", "/api/v1/admin/tagging/batch", []map[string]any{}); r.StatusCode != 400 {
		t.Fatalf("empty batch %d", r.StatusCode)
	}
	big := make([]map[string]any, 501)
	for i := range big {
		big[i] = map[string]any{"track_id": i + 1, "tags": []string{}}
	}
	if r, _ := do(t, ts, adm, "PUT", "/api/v1/admin/tagging/batch", big); r.StatusCode != 400 {
		t.Fatalf("501 items %d", r.StatusCode)
	}
}
