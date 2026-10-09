package api

import (
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/tags"
)

const maxTaggingBatch = 500

func (s *Server) vocab() *tags.Vocab {
	if s.Vocab != nil {
		return s.Vocab
	}
	return tags.Vocabulary()
}

func (s *Server) listVocabulary(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.vocab().All())
}

type pendingItem struct {
	ID     int64      `json:"id"`
	Title  string     `json:"title"`
	Artist string     `json:"artist"`
	Album  string     `json:"album"`
	Folder string     `json:"folder"`
	Path   string     `json:"path"`
	Status string     `json:"status"`
	Tags   []tags.Tag `json:"tags"`
}

// taggingPending lists tracks the agent has not covered yet, with the context
// (folder, existing tags) it needs to pick vocabulary tags.
func (s *Server) taggingPending(w http.ResponseWriter, r *http.Request) {
	ids, err := s.Tags.PendingForTagging(r.Context(), int(qInt(r, "limit")))
	if err != nil {
		s.fail(w, err)
		return
	}
	trs, err := s.Library.TracksByIDs(r.Context(), auth.UserFrom(r.Context()).ID, ids)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]pendingItem, 0, len(trs))
	for _, tr := range trs {
		folder := path.Dir(tr.Path)
		if folder == "." {
			folder = ""
		}
		ts, err := s.Tags.ForTrack(r.Context(), tr.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		out = append(out, pendingItem{ID: tr.ID, Title: tr.Title, Artist: tr.Artist, Album: tr.Album,
			Folder: folder, Path: tr.Path, Status: tr.Status, Tags: ts})
	}
	writeJSON(w, 200, out)
}

// taggingBatch stores agent-chosen tags. Vocabulary slugs only: one unknown
// tag rejects the whole batch before anything is written.
func (s *Server) taggingBatch(w http.ResponseWriter, r *http.Request) {
	var in []struct {
		TrackID int64    `json:"track_id"`
		Tags    []string `json:"tags"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if len(in) == 0 || len(in) > maxTaggingBatch {
		writeError(w, 400, "batch must have 1 to 500 items")
		return
	}
	v := s.vocab()
	resolved := make([][]tags.Tag, len(in))
	var unknown []string
	seenUnknown := map[string]bool{}
	for i, it := range in {
		for _, raw := range it.Tags {
			e, ok := v.Lookup(raw)
			if !ok {
				if !seenUnknown[raw] {
					seenUnknown[raw] = true
					unknown = append(unknown, raw)
				}
				continue
			}
			resolved[i] = append(resolved[i], tags.Tag{Name: e.Slug, Kind: e.Kind})
		}
	}
	if len(unknown) > 0 {
		writeCoded(w, 400, "unknown_tags", "unknown tags: "+strings.Join(unknown, ", "))
		return
	}
	ctx := r.Context()
	updated := []int64{}
	skipped := []int64{}
	for i, it := range in {
		if err := s.Library.Editable(ctx, it.TrackID); err != nil {
			if errors.Is(err, library.ErrNotFound) {
				skipped = append(skipped, it.TrackID)
				continue
			}
			s.fail(w, err)
			return
		}
		if err := s.Tags.Replace(ctx, it.TrackID, "agent", resolved[i]); err != nil {
			s.fail(w, err)
			return
		}
		updated = append(updated, it.TrackID)
	}
	if err := s.Tags.MarkAgentDone(ctx, updated); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"updated": len(updated), "skipped": skipped})
}
