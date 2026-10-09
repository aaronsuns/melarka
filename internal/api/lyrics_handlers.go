package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/lyrics"
)

func (s *Server) lyricsRoutes(a chi.Router) {
	a.Get("/tracks/{id}/lyrics", s.getLyrics)
	// Anyone signed in: they act on the track's lyrics for everyone.
	a.Put("/tracks/{id}/lyrics/offset", s.setLyricsOffset)
	a.Post("/tracks/{id}/lyrics/wrong", s.reportWrongLyrics)
	a.Post("/tracks/{id}/lyrics/wrong/undo", s.undoWrongLyrics) // the reporter, or an admin
}

func (s *Server) lyricsAdminRoutes(adm chi.Router) {
	adm.Get("/tracks/{id}/lyrics/candidates", s.lyricsCandidates)
	adm.Put("/tracks/{id}/lyrics", s.selectLyrics)
	adm.Post("/tracks/{id}/lyrics/refresh", s.refreshLyrics)
	adm.Delete("/tracks/{id}/lyrics/candidates/{cid}", s.deleteLyricsCandidate)
	adm.Get("/tracks/{id}/lyrics/rejected", s.lyricsRejected)
	adm.Delete("/tracks/{id}/lyrics/rejected/{rid}", s.unrejectLyrics)
	s.lyricsMissingRoutes(adm)
}

func (s *Server) lyricsFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, lyrics.ErrNotFound):
		writeError(w, 404, "not found")
	case errors.Is(err, lyrics.ErrNoLyrics):
		writeCoded(w, 409, "no_lyrics", "the track has no lyrics selected")
	case errors.Is(err, lyrics.ErrLyricsChanged):
		writeCoded(w, 409, "lyrics_changed", "other lyrics are shown for this track now")
	case errors.Is(err, lyrics.ErrForbidden):
		writeCoded(w, 403, "not_your_report", "only the reporter or an admin can undo this report")
	default:
		s.fail(w, err)
	}
}

// setLyricsOffset takes {"offset_ms": n, "lyrics_id"?: id}: the selected
// lyrics show n ms later (negative: earlier) for everyone, clamped to
// ±30000. 204; 409 lyrics_changed when lyrics_id is no longer the selected
// one, no_lyrics when nothing is selected.
func (s *Server) setLyricsOffset(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	var in struct {
		OffsetMS *int  `json:"offset_ms"`
		LyricsID int64 `json:"lyrics_id"`
	}
	if err := decodeJSON(r, &in); err != nil || in.OffsetMS == nil || in.LyricsID < 0 {
		writeError(w, 400, "offset_ms (an integer) required")
		return
	}
	if err := s.Lyrics.SetOffset(r.Context(), id, in.LyricsID, *in.OffsetMS); err != nil {
		s.lyricsFail(w, err)
		return
	}
	w.WriteHeader(204)
}

// reportWrongLyrics is "wrong lyrics" for {"lyrics_id"} (the shown lyrics'
// id): those words are rejected for good and the next best candidate is
// selected; with none left the track goes back on the lyrics agent's list
// (reported_wrong). Answers the new lyrics (or {"found": false}) plus
// report_id for the undo; 409 lyrics_changed when other lyrics are shown
// now, no_lyrics when none are.
func (s *Server) reportWrongLyrics(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	var in struct {
		LyricsID int64 `json:"lyrics_id"`
	}
	if err := decodeJSON(r, &in); err != nil || in.LyricsID <= 0 {
		writeError(w, 400, "lyrics_id required")
		return
	}
	res, report, err := s.Lyrics.ReportWrong(r.Context(), id, in.LyricsID, auth.UserFrom(r.Context()).ID)
	if err != nil {
		s.lyricsFail(w, err)
		return
	}
	writeJSON(w, 200, struct {
		lyrics.Result
		ReportID int64 `json:"report_id"`
	}{res, report})
}

// undoWrongLyrics takes {"report_id"} back: the reported lyrics are shown
// again (with their offset). Only the reporter or an admin; 404 for an
// unknown or already undone report.
func (s *Server) undoWrongLyrics(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	var in struct {
		ReportID int64 `json:"report_id"`
	}
	if err := decodeJSON(r, &in); err != nil || in.ReportID <= 0 {
		writeError(w, 400, "report_id required")
		return
	}
	u := auth.UserFrom(r.Context())
	res, err := s.Lyrics.UndoWrong(r.Context(), id, in.ReportID, u.ID, u.Role == auth.RoleAdmin)
	if err != nil {
		s.lyricsFail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

// lyricsRejected lists the track's rejected lyrics (reported wrong, or
// deleted by an admin), newest first.
func (s *Server) lyricsRejected(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	rs, err := s.Lyrics.Rejected(r.Context(), id)
	if err != nil {
		s.lyricsFail(w, err)
		return
	}
	writeJSON(w, 200, rs)
}

// unrejectLyrics lifts a rejection: kept words come back as a candidate, and
// lookups may store them again.
func (s *Server) unrejectLyrics(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	rid, ok := idParam(r, "rid")
	if !ok {
		writeError(w, 400, "bad rejection id")
		return
	}
	if err := s.Lyrics.Unreject(r.Context(), id, rid); err != nil {
		s.lyricsFail(w, err)
		return
	}
	w.WriteHeader(204)
}

// getLyrics answers 200 {"found":false} when nothing is known (the lookup is
// bounded by the service's budget), 404 only for a missing/trashed track.
func (s *Server) getLyrics(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	res, err := s.Lyrics.Get(r.Context(), id)
	if err != nil {
		s.lyricsFail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) lyricsCandidates(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	cs, err := s.Lyrics.Candidates(r.Context(), id)
	if err != nil {
		s.lyricsFail(w, err)
		return
	}
	writeJSON(w, 200, cs)
}

// selectLyrics takes {"candidate_id"} (show that one) or {"none": true}
// (this song has no lyrics: nothing shown, and automatic lookups stop).
func (s *Server) selectLyrics(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	var in struct {
		CandidateID int64 `json:"candidate_id"`
		None        bool  `json:"none"`
	}
	if err := decodeJSON(r, &in); err != nil || (in.CandidateID != 0) == in.None {
		writeError(w, 400, "candidate_id or none required")
		return
	}
	var err error
	if in.None {
		err = s.Lyrics.MarkNone(r.Context(), id)
	} else {
		err = s.Lyrics.Select(r.Context(), id, in.CandidateID)
	}
	if err != nil {
		s.lyricsFail(w, err)
		return
	}
	w.WriteHeader(204)
}

// deleteLyricsCandidate removes one stored candidate for good (the next one
// is selected if it was shown; with none left the track has no lyrics).
func (s *Server) deleteLyricsCandidate(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	cid, ok := idParam(r, "cid")
	if !ok {
		writeError(w, 400, "bad candidate id")
		return
	}
	if err := s.Lyrics.DeleteCandidate(r.Context(), id, cid); err != nil {
		s.lyricsFail(w, err)
		return
	}
	w.WriteHeader(204)
}

// refreshLyrics looks the track up again; an optional body {"title"?,
// "artist"?} searches with those instead of the cleaned display values, and
// "broad": true matches loosely (any artist, a looser title and duration)
// and keeps up to lyrics.BroadKeep candidates for the picker.
func (s *Server) refreshLyrics(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	var in struct {
		Title  *string `json:"title"`
		Artist *string `json:"artist"`
		Broad  bool    `json:"broad"`
	}
	if err := decodeOptionalJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if in.Title != nil && strings.TrimSpace(*in.Title) == "" {
		writeError(w, 400, "title must not be empty")
		return
	}
	res, err := s.Lyrics.LookupWith(r.Context(), id, lyrics.Override{Title: in.Title, Artist: in.Artist, Broad: in.Broad})
	if err != nil {
		s.lyricsFail(w, err)
		return
	}
	writeJSON(w, 200, res)
}
