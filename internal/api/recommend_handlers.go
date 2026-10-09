package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/recommend"
)

// recommendRoutes are per-user ("/me/…"): every handler acts on the
// signed-in user's own recommendations only.
func (s *Server) recommendRoutes(a chi.Router) {
	a.Get("/me/recommendations", s.getRecommendations)
	a.Post("/me/recommendations/refresh", s.refreshRecommendations)
	a.Put("/me/recommendations/{video}/dismiss", s.dismissRecommendation)
}

// searchGate runs recommendation yt-dlp work through the search limiter at
// low priority.
type searchGate struct{ l *searchLimiter }

func (g searchGate) Low(ctx context.Context, fn func(ctx context.Context) error) error {
	return g.l.low(ctx, fn)
}

// SearchGate is the low-priority path into the YouTube search limiter, for
// the recommendation worker (one token, only while no member search waits).
func (s *Server) SearchGate() recommend.Gate { return searchGate{&s.searchLimiter} }

func (s *Server) getRecommendations(w http.ResponseWriter, r *http.Request) {
	type body struct {
		recommend.Result
		Enabled bool `json:"enabled"`
	}
	if s.Recs == nil {
		writeJSON(w, 200, body{Result: recommend.Result{Items: []recommend.Item{}}})
		return
	}
	res, err := s.Recs.List(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		s.Log.Error("recommendations", "err", err)
		writeError(w, 500, "could not load recommendations")
		return
	}
	writeJSON(w, 200, body{Result: res, Enabled: true})
}

func (s *Server) refreshRecommendations(w http.ResponseWriter, r *http.Request) {
	if s.Recs == nil {
		writeCoded(w, 409, "recommendations_off", "recommendations are switched off on this server")
		return
	}
	// 202 whether this starts a refresh or one is already pending/running.
	if err := s.Recs.Request(r.Context(), auth.UserFrom(r.Context()).ID); err != nil {
		s.Log.Error("recommendations refresh", "err", err)
		writeError(w, 500, "could not start a refresh")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) dismissRecommendation(w http.ResponseWriter, r *http.Request) {
	if s.Recs == nil {
		writeCoded(w, 409, "recommendations_off", "recommendations are switched off on this server")
		return
	}
	err := s.Recs.Dismiss(r.Context(), auth.UserFrom(r.Context()).ID, chi.URLParam(r, "video"))
	switch {
	case errors.Is(err, recommend.ErrBadVideo):
		writeError(w, 400, "bad video id")
	case err != nil:
		s.Log.Error("recommendations dismiss", "err", err)
		writeError(w, 500, "could not dismiss")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
