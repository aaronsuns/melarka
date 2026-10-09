package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aaronsuns/lark-server/internal/artwork"
)

// coverWait caps how long a cover request waits for a lookup; tests shorten it.
var coverWait = 10 * time.Second

func (s *Server) artworkRoutes(a chi.Router) {
	a.Get("/tracks/{id}/cover", func(w http.ResponseWriter, r *http.Request) { s.serveCover(w, r, s.Artwork.Get) })
	a.Get("/albums/{id}/cover", func(w http.ResponseWriter, r *http.Request) { s.serveCover(w, r, s.Artwork.AlbumCover) })
}

func (s *Server) serveCover(w http.ResponseWriter, r *http.Request, get func(context.Context, int64, int) (string, error)) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	size := 300
	if v := r.URL.Query().Get("size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || (n != 300 && n != 1000) {
			writeError(w, 400, "size must be 300 or 1000")
			return
		}
		size = n
	}
	ctx, cancel := context.WithTimeout(r.Context(), coverWait)
	defer cancel()
	path, err := get(ctx, id, size)
	if err == nil {
		var f *os.File
		if f, err = os.Open(path); err == nil {
			defer f.Close()
			var st os.FileInfo
			if st, err = f.Stat(); err == nil {
				w.Header().Set("Content-Type", "image/jpeg")
				w.Header().Set("Cache-Control", "private, max-age=86400")
				http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
				return
			} // a Stat failure is answered like any other failed lookup below
		}
	}
	switch {
	case r.Context().Err() != nil:
		return // the client went away
	case errors.Is(err, artwork.ErrNotFound):
		writeError(w, 404, "not found")
	default:
		if !errors.Is(err, artwork.ErrNoCover) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, os.ErrNotExist) {
			// "database is locked" and the like: no cover for now, ask again later.
			s.Log.Warn("cover lookup failed", "id", id, "err", err)
		}
		w.Header().Set("Cache-Control", "no-store")
		writeCoded(w, 404, "no_cover", "this song has no cover")
	}
}
