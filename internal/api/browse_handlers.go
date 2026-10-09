package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/library"
)

func (s *Server) browseRoutes(a chi.Router) {
	a.Get("/tracks", s.listTracks)
	a.Get("/tracks/random", s.randomTracks)
	a.Get("/tracks/{id}", s.getTrack)
	a.Get("/albums", s.listAlbums)
	a.Get("/albums/{id}", s.getAlbum)
	a.Get("/artists", s.listArtists)
	a.Get("/artists/{id}", s.getArtist)
	a.Get("/tags", s.listTags)
	a.Get("/libraries", s.listLibraries)
	a.Get("/search", s.search)
}

func qInt(r *http.Request, k string) int64 {
	n, _ := strconv.ParseInt(r.URL.Query().Get(k), 10, 64)
	return n
}

// parseIDs parses a comma-separated list of ints, skipping anything that
// doesn't parse.
func parseIDs(s string) []int64 {
	var ids []int64
	for _, p := range strings.Split(s, ",") {
		if id, err := strconv.ParseInt(p, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, library.ErrNotFound) {
		writeError(w, 404, "not found")
		return
	}
	s.Log.Error("request failed", "err", err)
	writeError(w, 500, "internal error")
}

func (s *Server) listTracks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, err := s.Library.Tracks(r.Context(), auth.UserFrom(r.Context()).ID, library.TrackFilter{
		LibraryID: qInt(r, "library"), ArtistID: qInt(r, "artist"), AlbumID: qInt(r, "album"),
		Tag: q.Get("tag"), Status: q.Get("status"),
		FavoritesOnly: q.Get("favorite") == "1", DislikedOnly: q.Get("disliked") == "1",
		Broken: q.Get("broken") == "1",
		Sort:   q.Get("sort"), Limit: int(qInt(r, "limit")), Cursor: q.Get("cursor"),
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) randomTracks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	uid, n, exclude := auth.UserFrom(r.Context()).ID, int(qInt(r, "n")), parseIDs(q.Get("exclude"))
	// source=favorites is the B2 "start with favorites" form; it answers an
	// object so the client knows whether it fell back to the whole library.
	// Without source the answer stays a bare array (B1 clients).
	if q.Get("source") == "favorites" {
		ts, src, err := s.Library.RandomFavorites(r.Context(), uid, n, exclude)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"source": src, "tracks": ts})
		return
	}
	ts, err := s.Library.Random(r.Context(), uid, library.RandomOpts{N: n, Exclude: exclude, Tag: q.Get("tag")})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, ts)
}

func (s *Server) getTrack(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, 400, "bad id")
		return
	}
	t, err := s.Library.Track(r.Context(), auth.UserFrom(r.Context()).ID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) listAlbums(w http.ResponseWriter, r *http.Request) {
	p, err := s.Library.Albums(r.Context(), qInt(r, "artist"), int(qInt(r, "limit")), r.URL.Query().Get("cursor"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) getAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, 400, "bad id")
		return
	}
	a, tracks, err := s.Library.Album(r.Context(), auth.UserFrom(r.Context()).ID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"album": a, "tracks": tracks})
}

func (s *Server) listArtists(w http.ResponseWriter, r *http.Request) {
	p, err := s.Library.Artists(r.Context(), int(qInt(r, "limit")), r.URL.Query().Get("cursor"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) getArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, 400, "bad id")
		return
	}
	a, albums, err := s.Library.Artist(r.Context(), auth.UserFrom(r.Context()).ID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"artist": a, "albums": albums})
}

func (s *Server) listTags(w http.ResponseWriter, r *http.Request) {
	ts, err := s.Library.Tags(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, ts)
}

func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request) {
	ls, err := s.Library.Libraries(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, ls)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	res, err := s.Library.Search(r.Context(), auth.UserFrom(r.Context()).ID, r.URL.Query().Get("q"), int(qInt(r, "limit")))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}
