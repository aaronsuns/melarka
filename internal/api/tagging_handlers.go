package api

import (
	"context"
	"database/sql"
	"encoding/json"
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
	ID       int64      `json:"id"`
	Title    string     `json:"title"`
	Artist   string     `json:"artist"`
	Album    string     `json:"album"`
	Folder   string     `json:"folder"`
	Path     string     `json:"path"`
	Status   string     `json:"status"`
	Tags     []tags.Tag `json:"tags"`
	Keywords []string   `json:"keywords"`
	// A song downloaded from YouTube: the video as it was uploaded (absent otherwise).
	VideoID       string   `json:"video_id,omitempty"`
	YTTitle       string   `json:"yt_title,omitempty"`
	YTChannel     string   `json:"yt_channel,omitempty"`
	YTTags        []string `json:"yt_tags,omitempty"`
	YTCategories  []string `json:"yt_categories,omitempty"`
	YTDescription string   `json:"yt_description,omitempty"` // its first maxPendingDescription runes
}

// maxPendingDescription: how much of a video's description the agent sees.
const maxPendingDescription = 500

// youtubeContext fills in what YouTube says about the video a downloaded track came from.
func (s *Server) youtubeContext(ctx context.Context, it *pendingItem) error {
	var tagsJSON, catsJSON, desc sql.NullString
	err := s.Library.DB.QueryRowContext(ctx, `SELECT d.video_id, d.title, d.channel, m.tags, m.categories, m.description
		FROM downloads d LEFT JOIN video_meta m ON m.video_id=d.video_id
		WHERE d.track_id=? AND d.status='done' ORDER BY d.id DESC LIMIT 1`, it.ID).
		Scan(&it.VideoID, &it.YTTitle, &it.YTChannel, &tagsJSON, &catsJSON, &desc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_ = json.Unmarshal([]byte(tagsJSON.String), &it.YTTags)
	_ = json.Unmarshal([]byte(catsJSON.String), &it.YTCategories)
	it.YTDescription = desc.String
	if r := []rune(it.YTDescription); len(r) > maxPendingDescription {
		it.YTDescription = string(r[:maxPendingDescription])
	}
	return nil
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
		kw, err := s.Tags.Keywords(r.Context(), tr.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		it := pendingItem{ID: tr.ID, Title: tr.Title, Artist: tr.Artist, Album: tr.Album,
			Folder: folder, Path: tr.Path, Status: tr.Status, Tags: ts, Keywords: kw}
		if err := s.youtubeContext(r.Context(), &it); err != nil {
			s.fail(w, err)
			return
		}
		out = append(out, it)
	}
	writeJSON(w, 200, out)
}

// taggingBatch stores agent-chosen tags. Vocabulary slugs only: one unknown
// tag rejects the whole batch before anything is written.
func (s *Server) taggingBatch(w http.ResponseWriter, r *http.Request) {
	var in []struct {
		TrackID int64    `json:"track_id"`
		Tags    []string `json:"tags"`
		// Keywords, when given, replace the agent's free-form search words for the track.
		Keywords *[]string `json:"keywords"`
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
		if it.Keywords != nil {
			if err := s.Tags.ReplaceKeywords(ctx, it.TrackID, "agent", *it.Keywords); err != nil {
				s.fail(w, err)
				return
			}
		}
		updated = append(updated, it.TrackID)
	}
	if err := s.Tags.MarkAgentDone(ctx, updated); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"updated": len(updated), "skipped": skipped})
}
