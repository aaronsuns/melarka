package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/preview"
)

// previewRoutes: previews, for any signed-in user (they sit in
// the authenticated group); 409 channels_off while Channels are off.
func (s *Server) previewRoutes(a chi.Router) {
	a.Group(func(c chi.Router) {
		c.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if s.Previews == nil {
					writeCoded(w, 409, "channels_off", "previews are switched off on this server")
					return
				}
				next.ServeHTTP(w, r)
			})
		})
		c.Post("/previews", s.createPreview)
		c.Get("/previews/{id}", s.getPreview)
		c.Get("/previews/{id}/stream", s.streamPreview)
		c.Post("/previews/{id}/keep", s.keepPreview)
	})
}

func (s *Server) previewFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, preview.ErrNotFound):
		writeError(w, 404, "not found")
	case errors.Is(err, preview.ErrBadVideo):
		writeCoded(w, 400, "bad_video_id", "bad video id")
	case errors.Is(err, preview.ErrBadMedia):
		writeCoded(w, 400, "bad_media", "media must be audio, video or hd, and only an audio preview can be kept as music")
	case errors.Is(err, preview.ErrLimit):
		writeCoded(w, 429, "preview_limit", "too many previews at once; keep or wait for some to expire")
	case errors.Is(err, preview.ErrNotReady):
		writeCoded(w, 409, "preview_not_ready", "the preview is still downloading")
	case errors.Is(err, preview.ErrVideoUnavailable):
		writeCoded(w, 409, "video_preview_unavailable", "this video has no format that plays while downloading; try an audio preview")
	case errors.Is(err, preview.ErrTooLong):
		writeCoded(w, 409, "preview_too_long", "this video takes too long to download as a preview")
	case errors.Is(err, preview.ErrNoSpace):
		writeCoded(w, 507, "preview_no_space", "the server is short of disk space for previews")
	case errors.Is(err, preview.ErrHDUnavailable):
		writeCoded(w, 409, "hd_unavailable", "this video has no 720p (or lower) mp4")
	case errors.Is(err, preview.ErrFailed):
		writeCoded(w, 409, "preview_failed", "the preview could not be downloaded")
	case errors.Is(err, preview.ErrRetry):
		w.Header().Set("Retry-After", "30")
		writeCoded(w, 503, "preview_retry", "YouTube refused for now; try again in a moment")
	case errors.Is(err, preview.ErrNoChannel):
		writeCoded(w, 409, "channel_unknown", "this video's channel is unknown")
	default:
		s.downloadFail(w, err) // download.ErrNoTarget → no_download_target, else 500
	}
}

func (s *Server) createPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		VideoID   string `json:"video_id"`
		Media     string `json:"media"`
		Title     string `json:"title"`
		Channel   string `json:"channel"`
		DurationS int    `json:"duration_s"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	p, err := s.Previews.Start(r.Context(), auth.UserFrom(r.Context()).ID,
		preview.Request{VideoID: in.VideoID, Media: in.Media, Title: in.Title, Channel: in.Channel, DurationS: in.DurationS})
	if err != nil {
		s.previewFail(w, err)
		return
	}
	writeJSON(w, 201, p)
}

func (s *Server) getPreview(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, 400, "bad id")
		return
	}
	p, err := s.Previews.Get(r.Context(), id)
	if err != nil {
		s.previewFail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) streamPreview(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, 400, "bad id")
		return
	}
	if err := s.Previews.Serve(w, r, id); err != nil && r.Context().Err() == nil {
		s.previewFail(w, err)
	}
}

func (s *Server) keepPreview(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, 400, "bad id")
		return
	}
	var in struct {
		To string `json:"to"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	res, err := s.Previews.Keep(r.Context(), auth.UserFrom(r.Context()).ID, id, in.To)
	if err != nil {
		s.previewFail(w, err)
		return
	}
	writeJSON(w, 200, res)
}
