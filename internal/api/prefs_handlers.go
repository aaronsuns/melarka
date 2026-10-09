package api

import (
	"errors"
	"net/http"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/buildinfo"
	"github.com/aaronsuns/lark-server/internal/config"
	"github.com/aaronsuns/lark-server/internal/prefs"
)

// info answers GET /api/v1/info, which is unauthenticated: the web app
// needs the server's default language before a user has logged in (or ever
// will — a login page itself needs to be in the right language).
func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	lang := s.Language
	if lang == "" {
		lang = "en"
	}
	writeJSON(w, 200, map[string]any{
		"name":      "Melarka",
		"version":   buildinfo.Version,
		"language":  lang,
		"languages": config.Languages,
		// false: the web app unregisters its service worker and deletes its offline cache.
		"offline_cache": !s.OfflineCacheOff,
		// false (channels.enabled off): the web app hides 频道.
		"channels": s.Channels != nil,
	})
}

func (s *Server) getPrefs(w http.ResponseWriter, r *http.Request) {
	p, err := s.Prefs.Get(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) putPrefs(w http.ResponseWriter, r *http.Request) {
	var in prefs.Prefs
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	p, err := s.Prefs.Put(r.Context(), auth.UserFrom(r.Context()).ID, in)
	if errors.Is(err, prefs.ErrInvalid) {
		writeError(w, 400, "language must be en, zh-Hans, zh-Hant, sv or null; on_open must be shuffle_favorites, resume or nothing")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}
