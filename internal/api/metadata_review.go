package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aaronsuns/lark-server/internal/metareview"
)

const maxReviewPage = 500

func (s *Server) metadataReviewRoutes(adm chi.Router) {
	adm.Get("/admin/metadata/review", s.metadataReviewList)
	adm.Get("/admin/metadata/review/count", s.metadataReviewCount)
	adm.Get("/admin/metadata/review/{id}", s.metadataReviewOne)
	adm.Put("/admin/metadata/review/{id}", s.metadataReviewDone)
}

func (s *Server) review() *metareview.Service {
	return &metareview.Service{DB: s.Library.DB, Library: s.Library, Lyrics: s.Lyrics}
}

type metadataReviewItem struct {
	ID             int64  `json:"id"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	Album          string `json:"album"`
	Year           *int64 `json:"year"`
	DurationS      int64  `json:"duration_s"`
	Folder         string `json:"folder"`
	Path           string `json:"path"`
	YouTubeTitle   string `json:"youtube_title,omitempty"`
	YouTubeChannel string `json:"youtube_channel,omitempty"`
	TitleEdited    bool   `json:"title_edited"`
	ArtistEdited   bool   `json:"artist_edited"`
	AlbumEdited    bool   `json:"album_edited"`
	YearEdited     bool   `json:"year_edited"`
	HasLyrics      bool   `json:"has_lyrics"`
	Source         string `json:"source"` // "download" (YouTube) or "library" (a library file)
}

func reviewItemJSON(it metareview.Item) metadataReviewItem {
	tr := it.Track
	src := "library"
	if it.FromDownload {
		src = "download"
	}
	return metadataReviewItem{ID: tr.ID, Title: tr.Title, Artist: tr.Artist, Album: tr.Album, Year: tr.Year,
		DurationS: (tr.DurationMS + 500) / 1000, Folder: it.Folder, Path: tr.Path,
		YouTubeTitle: it.YouTubeTitle, YouTubeChannel: it.YouTubeChannel,
		TitleEdited: it.TitleEdited, ArtistEdited: it.ArtistEdited, AlbumEdited: it.AlbumEdited, YearEdited: it.YearEdited,
		HasLyrics: it.HasLyrics, Source: src}
}

// metadataReviewOne serves one track as the review list would, reviewed or
// not (the names tool re-reads it before changing album or year).
func (s *Server) metadataReviewOne(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	it, err := s.review().Get(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, reviewItemJSON(it))
}

// reviewScope reads ?scope= (favorites | downloads | all, default all: both).
func reviewScope(w http.ResponseWriter, r *http.Request) (int, bool) {
	switch r.URL.Query().Get("scope") {
	case "", "all":
		return metareview.ScopeAll, true
	case "favorites":
		return metareview.ScopeFavorites, true
	case "downloads":
		return metareview.ScopeDownloads, true
	}
	writeError(w, 400, "scope must be favorites, downloads or all")
	return 0, false
}

// metadataReviewList is the names agent's work list: anyone's favorites,
// then download-library tracks, whose displayed names were never reviewed or
// changed since. ?after=<last id> pages.
func (s *Server) metadataReviewList(w http.ResponseWriter, r *http.Request) {
	scope, ok := reviewScope(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, after := 50, int64(0)
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxReviewPage {
			writeError(w, 400, "limit must be 1 to 500")
			return
		}
		limit = n
	}
	if v := q.Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeError(w, 400, "after must be a track id")
			return
		}
		after = n
	}
	items, err := s.review().List(r.Context(), scope, after, limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]metadataReviewItem, 0, len(items))
	for _, it := range items {
		out = append(out, reviewItemJSON(it))
	}
	writeJSON(w, 200, out)
}

func (s *Server) metadataReviewCount(w http.ResponseWriter, r *http.Request) {
	scope, ok := reviewScope(w, r)
	if !ok {
		return
	}
	n, err := s.review().Count(r.Context(), scope)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]int{"count": n})
}

// metadataReviewDone records {"outcome": "fixed"|"ok"|"skipped"} for the
// track as it is displayed now; the track returns to the list when its
// title, artist, album or year changes.
func (s *Server) metadataReviewDone(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	var in struct {
		Outcome string `json:"outcome"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	err := s.review().Done(r.Context(), id, in.Outcome)
	if errors.Is(err, metareview.ErrOutcome) {
		writeError(w, 400, err.Error())
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(204)
}
