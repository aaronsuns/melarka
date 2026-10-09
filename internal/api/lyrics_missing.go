package api

import (
	"net/http"
	"path"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/lyrics"
)

const maxMissingPage = 500

func (s *Server) lyricsMissingRoutes(adm chi.Router) {
	adm.Get("/admin/lyrics/missing", s.lyricsMissing)
	adm.Get("/admin/lyrics/missing/count", s.lyricsMissingCount)
}

type missingLyricsItem struct {
	ID             int64  `json:"id"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	Album          string `json:"album"`
	Folder         string `json:"folder"`
	Path           string `json:"path"`
	DurationS      int64  `json:"duration_s"`
	YouTubeTitle   string `json:"youtube_title,omitempty"`
	YouTubeChannel string `json:"youtube_channel,omitempty"`
	LastLookupAt   *int64 `json:"last_lookup_at,omitempty"`
	Found          *bool  `json:"found,omitempty"`
	ReportedWrong  bool   `json:"reported_wrong"`
	TitleEdited    bool   `json:"title_edited"`
	ArtistEdited   bool   `json:"artist_edited"`
}

// missingScope reads ?scope= (favorites | downloads | all, default all).
func missingScope(w http.ResponseWriter, r *http.Request) (int, bool) {
	switch r.URL.Query().Get("scope") {
	case "", "all":
		return lyrics.MissingAll, true
	case "favorites":
		return lyrics.MissingFavorites, true
	case "downloads":
		return lyrics.MissingDownloads, true
	}
	writeError(w, 400, "scope must be favorites, downloads or all")
	return 0, false
}

// missingReported reads ?reported= (1: only tracks whose lyrics were
// reported wrong, across the groups; 0 or absent: every missing track).
func missingReported(w http.ResponseWriter, r *http.Request) (bool, bool) {
	switch r.URL.Query().Get("reported") {
	case "", "0":
		return false, true
	case "1":
		return true, true
	}
	writeError(w, 400, "reported must be 0 or 1")
	return false, false
}

// lyricsMissing is the lyrics agent's work list: tracks with no selected
// lyrics (and no instrumental tag), anyone's favorites first, then
// downloads, then the rest, with the YouTube title/channel a download came
// from. ?after=<last id> pages.
func (s *Server) lyricsMissing(w http.ResponseWriter, r *http.Request) {
	group, ok := missingScope(w, r)
	if !ok {
		return
	}
	reported, ok := missingReported(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, after := 100, int64(0)
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxMissingPage {
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
	ms, err := s.Lyrics.MissingListFiltered(r.Context(), group, reported, after, limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	ids := make([]int64, len(ms))
	for i, m := range ms {
		ids[i] = m.ID
	}
	trs, err := s.Library.TracksByIDs(r.Context(), auth.UserFrom(r.Context()).ID, ids)
	if err != nil {
		s.fail(w, err)
		return
	}
	byID := make(map[int64]lyrics.Missing, len(ms))
	for _, m := range ms {
		byID[m.ID] = m
	}
	out := make([]missingLyricsItem, 0, len(trs))
	for _, tr := range trs {
		m := byID[tr.ID]
		folder := path.Dir(tr.Path)
		if folder == "." {
			folder = ""
		}
		out = append(out, missingLyricsItem{ID: tr.ID, Title: tr.Title, Artist: tr.Artist, Album: tr.Album,
			Folder: folder, Path: tr.Path, DurationS: (tr.DurationMS + 500) / 1000,
			YouTubeTitle: m.YouTubeTitle, YouTubeChannel: m.YouTubeChannel, LastLookupAt: m.LastLookupAt, Found: m.Found, ReportedWrong: m.ReportedWrong,
			TitleEdited: m.TitleEdited, ArtistEdited: m.ArtistEdited})
	}
	writeJSON(w, 200, out)
}

func (s *Server) lyricsMissingCount(w http.ResponseWriter, r *http.Request) {
	group, ok := missingScope(w, r)
	if !ok {
		return
	}
	reported, ok := missingReported(w, r)
	if !ok {
		return
	}
	n, err := s.Lyrics.MissingCountFiltered(r.Context(), group, reported)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]int{"count": n})
}
