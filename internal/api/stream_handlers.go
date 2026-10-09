package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/stream"
)

func (s *Server) streamRoutes(a chi.Router) {
	a.Get("/tracks/{id}/stream", s.streamTrack)
	a.Post("/tracks/prepare", s.prepareTracks)
}

// maxPrepare bounds how many ids one prepare request may name.
const maxPrepare = 5

// prepareTracks queues a background transcode of the tracks the player will
// play next (soonest first), so their streams start at once when their turn
// comes. Answers at once; the work happens in s.Prepare's one worker.
func (s *Server) prepareTracks(w http.ResponseWriter, r *http.Request) {
	if !s.prepareLimit.allow(auth.UserFrom(r.Context()).ID, time.Now()) {
		writeError(w, 429, "too many prepare requests")
		return
	}
	var in struct {
		IDs     []int64 `json:"ids"`
		Quality string  `json:"quality"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	tier, ok := stream.ParseTier(in.Quality)
	if !ok {
		writeError(w, 400, "quality must be lossless, high or saver")
		return
	}
	ids := make([]int64, 0, maxPrepare)
	seen := map[int64]bool{}
	for _, id := range in.IDs {
		if id > 0 && !seen[id] && len(ids) < maxPrepare {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	n := 0
	if s.Prepare != nil {
		n = s.Prepare.Enqueue(ids, tier)
	}
	writeJSON(w, 202, map[string]int{"queued": n})
}

func (s *Server) streamTrack(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, 400, "bad id")
		return
	}
	tier, ok := stream.ParseTier(r.URL.Query().Get("quality"))
	if !ok {
		writeError(w, 400, "quality must be lossless, high or saver")
		return
	}
	path, ctype, err := s.Stream.Resolve(r.Context(), id, tier)
	if errors.Is(err, stream.ErrUnplayable) {
		writeError(w, 415, err.Error())
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// The client is already gone (e.g. skipped to the next track); there's
		// no one to send a response to and nothing went wrong worth logging.
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, 404, "not found")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		s.fail(w, err)
		return
	}
	// Which version of the source file this is (its fingerprint): the web
	// offline cache compares it to know when its copy went stale. Not an
	// ETag on purpose: a transcode isn't promised byte-identical across
	// re-encodes, and an ETag would also change how iOS's If-Range resumes.
	if si, err := s.Library.StreamInfo(r.Context(), id); err == nil && si.Fingerprint != "" {
		fp := si.Fingerprint
		if len(fp) > 16 {
			fp = fp[:16]
		}
		w.Header().Set("X-Lark-Version", fp)
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
}
