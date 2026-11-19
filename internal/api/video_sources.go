package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/aaronsuns/lark-server/internal/videosrc"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// videoSourcesBody is GET/PUT /admin/video-sources' answer.
type videoSourcesBody struct {
	Sources []ytdlp.Source `json:"sources"`
	SetBy   string         `json:"set_by"`
}

// enabledSources is what /info and the search default use. A nil
// VideoSources (tests, old wiring) means YouTube only.
func (s *Server) enabledSources(ctx context.Context) []ytdlp.Source {
	if s.VideoSources == nil {
		return []ytdlp.Source{ytdlp.SourceYouTube}
	}
	srcs, _, err := s.VideoSources.List(ctx)
	if err != nil {
		s.Log.Warn("video sources", "err", err)
		return []ytdlp.Source{}
	}
	return srcs
}

// sourceOn reports whether src is on.
func (s *Server) sourceOn(ctx context.Context, src ytdlp.Source) bool {
	return s.VideoSources.Enabled(ctx, src)
}

// requireSource writes 409 source_disabled and returns false when src is off.
func (s *Server) requireSource(w http.ResponseWriter, r *http.Request, src ytdlp.Source) bool {
	if s.sourceOn(r.Context(), src) {
		return true
	}
	writeCoded(w, 409, "source_disabled", string(src)+" is turned off on this server")
	return false
}

func (s *Server) videoSourcesGet(w http.ResponseWriter, r *http.Request) {
	srcs, by, err := s.VideoSources.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, videoSourcesBody{Sources: srcs, SetBy: by})
}

func (s *Server) videoSourcesPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Sources *[]ytdlp.Source `json:"sources"`
	}
	if err := decodeJSON(r, &body); err != nil || body.Sources == nil {
		writeCoded(w, 400, "bad_request", "sources (a list of youtube, bilibili) is required")
		return
	}
	if err := s.VideoSources.Set(r.Context(), *body.Sources); errors.Is(err, videosrc.ErrUnknown) {
		writeCoded(w, 400, "bad_request", "sources may only hold youtube and bilibili")
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	s.videoSourcesGet(w, r)
}
