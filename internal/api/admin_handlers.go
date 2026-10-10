package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/tags"
	"github.com/aaronsuns/lark-server/internal/trash"
)

// defaultMusicRoot is used when Server.MusicRoot is unset (e.g. in tests that
// don't care about the confinement check).
const defaultMusicRoot = "/music"

// musicRoot returns the directory admin-created library roots must live
// under. It never returns "" so callers can compare against it directly.
func (s *Server) musicRoot() string {
	if s.MusicRoot == "" {
		return defaultMusicRoot
	}
	return filepath.Clean(s.MusicRoot)
}

// withinMusicRoot reports whether path, once cleaned, is a strict
// subdirectory of root — not root itself, not outside it via a "../" escape,
// and not a relative path (which filepath.Clean can't confine at all).
// Without this, addLibrary would accept "/", "/etc", or the server's own data
// directory as a library root: os.Stat only confirms the path is *a*
// directory, not that it's a safe one, and trash.Service would then create
// "<root>/.lark-trash" right next to whatever lives there (e.g. the SQLite
// file, if root were the data dir).
func withinMusicRoot(root, path string) bool {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || clean == root {
		return false
	}
	return strings.HasPrefix(clean, root+string(filepath.Separator))
}

func (s *Server) tagRoutes(a chi.Router) {
	a.Get("/tracks/{id}/tags", s.getTrackTags)
	a.Get("/tags/vocabulary", s.listVocabulary)
}

func (s *Server) adminRoutes(adm chi.Router) {
	adm.Delete("/tracks/{id}", s.trashTrack)
	adm.Delete("/admin/broken-tracks", s.trashBrokenTracks)
	adm.Put("/tracks/{id}/status", s.setTrackStatus)
	adm.Patch("/tracks/{id}", s.patchTrack)
	adm.Put("/tracks/{id}/tags", s.putTrackTags)
	s.lyricsAdminRoutes(adm)
	s.metadataReviewRoutes(adm)
	adm.Get("/trash", s.listTrash)
	adm.Post("/trash/{id}/restore", s.restoreTrash)
	adm.Delete("/trash", s.emptyTrash)
	adm.Get("/users", s.listUsers)
	adm.Post("/users", s.createUser)
	adm.Delete("/users/{id}", s.deleteUser)
	adm.Put("/users/{id}/password", s.resetPassword)
	adm.Get("/admin/libraries", s.listLibraries)
	adm.Post("/admin/libraries", s.addLibrary)
	adm.Delete("/admin/libraries/{id}", s.deleteLibrary)
	adm.Post("/admin/scan", s.triggerScan)
	adm.Get("/admin/scan/status", s.scanStatus)
	adm.Get("/admin/tagging/pending", s.taggingPending)
	adm.Put("/admin/tagging/batch", s.taggingBatch)
	adm.Get("/admin/ytdlp", s.ytdlpInfo)
	adm.Post("/admin/ytdlp/update", s.ytdlpUpdate)
}

func (s *Server) withID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, 400, "bad id")
	}
	return id, ok
}

func (s *Server) getTrackTags(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	ts, err := s.Tags.ForTrack(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, ts)
}

func (s *Server) trashTrack(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	switch err := s.Trash.Move(r.Context(), id); {
	case err == nil:
		w.WriteHeader(204)
	case errors.Is(err, trash.ErrNotFound), errors.Is(err, library.ErrNotFound):
		writeError(w, 404, "not found")
	case errors.Is(err, trash.ErrCrossDevice):
		writeError(w, 409, err.Error())
	default:
		s.fail(w, err)
	}
}

// trashBrokenTracks moves every damaged file the admin console lists into
// the trash at once, as DELETE /tracks/{id} does one (restorable until it is
// purged). {"trashed": n}; with nothing left it is {"trashed": 0}. When some
// file could not be moved the rest still are, and the answer is a coded
// error saying how many failed.
func (s *Server) trashBrokenTracks(w http.ResponseWriter, r *http.Request) {
	moved, failed, err := s.Trash.MoveBroken(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	if failed > 0 {
		writeCoded(w, 500, "broken_trash_incomplete", fmt.Sprintf("%d of %d damaged files could not be moved to the trash", failed, moved+failed))
		return
	}
	writeJSON(w, 200, map[string]int{"trashed": moved})
}

func (s *Server) setTrackStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	var in struct{ Status string }
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if in.Status != "kept" && in.Status != "pending" {
		writeError(w, 400, "status must be kept or pending (use DELETE to trash)")
		return
	}
	if err := s.Library.SetStatus(r.Context(), id, in.Status); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) patchTrack(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	// "album": "" / "year": 0 clear the override (back to the file's tags);
	// "no_album" / "no_year": true show no album / no year at all.
	var in struct {
		Title, Artist, Album *string
		Year                 *int64
		NoAlbum              bool `json:"no_album"`
		NoYear               bool `json:"no_year"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if (in.NoAlbum && in.Album != nil) || (in.NoYear && in.Year != nil) {
		writeError(w, 400, "no_album and album (or no_year and year) cannot be combined")
		return
	}
	if err := s.Library.SetOverrides(r.Context(), id, library.Overrides{Title: in.Title, Artist: in.Artist, Album: in.Album, Year: in.Year,
		NoAlbum: in.NoAlbum, NoYear: in.NoYear}); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(204)
}

// putTrackTags refuses a trashed track before touching tags.Store: nothing in
// tags.Store knows about track status, so the check lives here rather than
// letting a trashed row silently accept manual tags it can never be
// retrieved through again (ForTrack itself isn't status-filtered).
func (s *Server) putTrackTags(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	if err := s.Library.Editable(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	var in struct {
		Source string     `json:"source"`
		Tags   []tags.Tag `json:"tags"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if in.Source == "" {
		in.Source = "manual"
	}
	if err := s.Tags.Replace(r.Context(), id, in.Source, in.Tags); err != nil {
		if errors.Is(err, tags.ErrUnknownSource) {
			writeError(w, 400, err.Error())
		} else {
			s.fail(w, err)
		}
		return
	}
	w.WriteHeader(204)
}

func (s *Server) listTrash(w http.ResponseWriter, r *http.Request) {
	items, err := s.Trash.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, items)
}

func (s *Server) restoreTrash(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	switch err := s.Trash.Restore(r.Context(), id); {
	case err == nil:
		w.WriteHeader(204)
	case errors.Is(err, trash.ErrNotFound):
		writeError(w, 404, "not in trash")
	case errors.Is(err, trash.ErrConflict):
		writeCoded(w, 409, "restore_conflict", err.Error())
	default:
		s.fail(w, err)
	}
}

func (s *Server) emptyTrash(w http.ResponseWriter, r *http.Request) {
	n, err := s.Trash.Purge(r.Context(), 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]int{"purged": n})
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	us, err := s.Auth.Users(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, us)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username, Password string
		Role               auth.Role
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	u, err := s.Auth.CreateUser(r.Context(), in.Username, in.Password, in.Role)
	switch {
	case errors.Is(err, auth.ErrExists):
		writeCoded(w, 409, "username_taken", "username taken")
	case errors.Is(err, auth.ErrInvalid):
		writeError(w, 400, err.Error())
	case err != nil:
		s.fail(w, err)
	default:
		writeJSON(w, 201, u)
	}
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	if id == auth.UserFrom(r.Context()).ID {
		writeCoded(w, 400, "cannot_delete_self", "you cannot delete your own account")
		return
	}
	if err := s.Auth.DeleteUser(r.Context(), id); err != nil {
		writeError(w, 404, "user not found")
		return
	}
	w.WriteHeader(204)
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	var in struct{ Password string }
	if err := decodeJSON(r, &in); err != nil || in.Password == "" {
		writeError(w, 400, "password required")
		return
	}
	if err := s.Auth.SetPassword(r.Context(), id, in.Password); err != nil {
		writeError(w, 404, "user not found")
		return
	}
	w.WriteHeader(204)
}

func (s *Server) addLibrary(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name           string `json:"name"`
		Root           string `json:"root"`
		DownloadTarget bool   `json:"download_target"`
	}
	if err := decodeJSON(r, &in); err != nil || in.Name == "" || in.Root == "" {
		writeError(w, 400, "name and root required")
		return
	}
	// Checked before EnsureLibrary, whose ON CONFLICT(name) upsert would
	// otherwise silently repoint an existing library's root at whatever
	// path this request supplies.
	if _, err := s.Library.LibraryByName(r.Context(), in.Name); err == nil {
		writeCoded(w, 409, "library_name_taken", "library name already exists")
		return
	} else if !errors.Is(err, library.ErrNotFound) {
		s.fail(w, err)
		return
	}
	root := s.musicRoot()
	if !withinMusicRoot(root, in.Root) {
		writeError(w, 400, "library root must be a directory under "+root)
		return
	}
	clean := filepath.Clean(in.Root)
	if st, err := os.Stat(clean); err != nil || !st.IsDir() {
		writeError(w, 400, "root is not a directory inside the container")
		return
	}
	lib, err := s.Library.EnsureLibrary(r.Context(), in.Name, clean, in.DownloadTarget)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.Scans.Trigger(lib.ID)
	writeJSON(w, 201, lib)
}

func (s *Server) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	id, ok := s.withID(w, r)
	if !ok {
		return
	}
	if err := s.Library.DeleteLibrary(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) triggerScan(w http.ResponseWriter, r *http.Request) {
	if id := qInt(r, "library"); id > 0 {
		s.Scans.Trigger(id)
	} else {
		s.Scans.TriggerAll(r.Context())
	}
	writeJSON(w, 202, map[string]int64{"queued_at": time.Now().Unix()})
}

func (s *Server) scanStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Scans.Status())
}
