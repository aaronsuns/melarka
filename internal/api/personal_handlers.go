package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/personal"
)

func (s *Server) personalRoutes(a chi.Router) {
	a.Put("/favorites/{id}", s.mark(true, true))
	a.Delete("/favorites/{id}", s.mark(true, false))
	a.Put("/dislikes/{id}", s.mark(false, true))
	a.Delete("/dislikes/{id}", s.mark(false, false))
	a.Get("/playlists", s.listPlaylists)
	a.Post("/playlists", s.createPlaylist)
	a.Get("/playlists/{id}", s.getPlaylist)
	a.Put("/playlists/{id}", s.updatePlaylist)
	a.Delete("/playlists/{id}", s.deletePlaylist)
	a.Post("/events/play", s.recordPlays)
	a.Get("/queue", s.getQueue)
	a.Put("/queue", s.putQueue)
	a.Get("/radio/next", s.radioNext)
	a.Get("/me/search-history", s.getSearchHistory)
	a.Post("/me/search-history", s.addSearchHistory)
	a.Delete("/me/search-history", s.clearSearchHistory)
}

func (s *Server) personalFail(w http.ResponseWriter, err error) {
	if errors.Is(err, personal.ErrNotFound) {
		writeError(w, 404, "not found")
		return
	}
	if errors.Is(err, personal.ErrInvalid) {
		writeError(w, 400, err.Error())
		return
	}
	s.fail(w, err)
}

func (s *Server) mark(favorite, on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := idParam(r, "id")
		if !ok {
			writeError(w, 400, "bad id")
			return
		}
		uid := auth.UserFrom(r.Context()).ID
		var err error
		if favorite {
			err = s.Personal.SetFavorite(r.Context(), uid, id, on)
		} else {
			err = s.Personal.SetDislike(r.Context(), uid, id, on)
		}
		if err != nil {
			s.personalFail(w, err)
			return
		}
		w.WriteHeader(204)
	}
}

func (s *Server) listPlaylists(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Personal.Playlists(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, ps)
}

func (s *Server) createPlaylist(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string  `json:"name"`
		TrackIDs []int64 `json:"track_ids"`
	}
	if err := decodeJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeError(w, 400, "name required")
		return
	}
	p, err := s.Personal.CreatePlaylist(r.Context(), auth.UserFrom(r.Context()).ID, in.Name, in.TrackIDs)
	if err != nil {
		s.personalFail(w, err)
		return
	}
	writeJSON(w, 201, p)
}

func (s *Server) getPlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, 400, "bad id")
		return
	}
	uid := auth.UserFrom(r.Context()).ID
	p, ids, err := s.Personal.Playlist(r.Context(), uid, id)
	if err != nil {
		s.personalFail(w, err)
		return
	}
	tracks, err := s.Library.TracksByIDs(r.Context(), uid, ids)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"playlist": p, "tracks": tracks})
}

func (s *Server) updatePlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, 400, "bad id")
		return
	}
	var in struct {
		Name     *string  `json:"name"`
		TrackIDs *[]int64 `json:"track_ids"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		writeError(w, 400, "name required")
		return
	}
	if err := s.Personal.UpdatePlaylist(r.Context(), auth.UserFrom(r.Context()).ID, id, in.Name, in.TrackIDs); err != nil {
		s.personalFail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, 400, "bad id")
		return
	}
	if err := s.Personal.DeletePlaylist(r.Context(), auth.UserFrom(r.Context()).ID, id); err != nil {
		s.personalFail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) recordPlays(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Events []personal.PlayEvent `json:"events"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	n, err := s.Personal.RecordPlays(r.Context(), auth.UserFrom(r.Context()).ID, auth.DeviceIDFrom(r.Context()), in.Events)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]int{"accepted": n})
}

func (s *Server) queueResponse(w http.ResponseWriter, r *http.Request, q personal.Queue) {
	tracks, err := s.Library.TracksByIDs(r.Context(), auth.UserFrom(r.Context()).ID, q.TrackIDs)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"queue": q, "tracks": tracks})
}

func (s *Server) getQueue(w http.ResponseWriter, r *http.Request) {
	q, err := s.Personal.Queue(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.queueResponse(w, r, q)
}

func (s *Server) putQueue(w http.ResponseWriter, r *http.Request) {
	var in personal.Queue
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	by := fmt.Sprintf("device:%d", auth.DeviceIDFrom(r.Context()))
	q, err := s.Personal.SaveQueue(r.Context(), auth.UserFrom(r.Context()).ID, in, by)
	if err != nil {
		s.personalFail(w, err)
		return
	}
	s.queueResponse(w, r, q)
}

func (s *Server) radioNext(w http.ResponseWriter, r *http.Request) {
	exclude := parseIDs(r.URL.Query().Get("exclude"))
	uid := auth.UserFrom(r.Context()).ID
	ids, err := s.Radio.Next(r.Context(), uid, int(qInt(r, "n")), exclude)
	if err != nil {
		s.fail(w, err)
		return
	}
	tracks, err := s.Library.TracksByIDs(r.Context(), uid, ids)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, tracks)
}

func (s *Server) getSearchHistory(w http.ResponseWriter, r *http.Request) {
	h, err := s.Personal.SearchHistory(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, h)
}

func (s *Server) addSearchHistory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Query string `json:"query"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	err := s.Personal.RecordSearch(r.Context(), auth.UserFrom(r.Context()).ID, in.Query)
	if errors.Is(err, personal.ErrInvalid) {
		writeError(w, 400, "query must be 1 to 100 characters")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) clearSearchHistory(w http.ResponseWriter, r *http.Request) {
	if err := s.Personal.ClearSearchHistory(r.Context(), auth.UserFrom(r.Context()).ID); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(204)
}
