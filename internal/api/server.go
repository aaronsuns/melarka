// Package api exposes Lark's HTTP API under /api/v1 and the embedded web UI.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/aaronsuns/lark-server/internal/artwork"
	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/channels"
	"github.com/aaronsuns/lark-server/internal/download"
	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/lyrics"
	"github.com/aaronsuns/lark-server/internal/personal"
	"github.com/aaronsuns/lark-server/internal/prefs"
	"github.com/aaronsuns/lark-server/internal/preview"
	"github.com/aaronsuns/lark-server/internal/radio"
	"github.com/aaronsuns/lark-server/internal/recommend"
	"github.com/aaronsuns/lark-server/internal/stream"
	"github.com/aaronsuns/lark-server/internal/tags"
	"github.com/aaronsuns/lark-server/internal/trash"
	"github.com/aaronsuns/lark-server/internal/webui"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

type Server struct {
	Auth    *auth.Store
	Library *library.Store
	Stream  *stream.Service
	// Prepare pre-transcodes the tracks a player says come next; nil: off.
	Prepare   *stream.Preparer
	Personal  *personal.Store
	Prefs     *prefs.Store
	Language  string // server's default UI language, from config.Config.Language
	Radio     *radio.Radio
	Tags      *tags.Store
	Lyrics    *lyrics.Service
	Artwork   *artwork.Service
	Vocab     *tags.Vocab // tag vocabulary; nil means the embedded one
	Trash     *trash.Service
	Scans     *library.Service
	Downloads *download.Service
	YT        *ytdlp.Client
	// Recs serves "为你推荐"; nil when recommendations are switched off.
	Recs *recommend.Service
	// Channels serves 频道 (spec §17); nil when channels.enabled is false.
	Channels *channels.Service
	// Previews serves 试听 (spec §17.5); nil when channels are switched off.
	Previews *preview.Service
	// Thumbs proxies YouTube thumbnails for 视频 (spec §18.2); nil: none served.
	Thumbs *preview.Thumbs

	// OfflineCacheOff is the web offline cache's kill switch (config
	// offline_cache: false): /info says so and /sw.js removes itself.
	OfflineCacheOff bool

	// searchCache is the bounded YouTube-search cache and searchLimiter caps
	// concurrent searches and collapses identical in-flight ones; both are
	// zero-value-ready, so nothing constructs them explicitly.
	searchCache   searchCache
	searchLimiter searchLimiter
	prepareLimit  prepareLimiter
	MusicRoot     string // confines admin-created library roots; empty means "/music"
	// YtDlpBin is the data-dir copy of yt-dlp (<data>/bin/yt-dlp) that the
	// admin update endpoint writes to and reports on; YtDlpPath is the
	// configured fallback/copy source (cfg.YtDlpPath, e.g. "yt-dlp"), used
	// whenever the data-dir copy doesn't exist yet. Together they mirror the
	// resolution ExecRunner.Bin performs in main.go for the binary actually
	// used to run downloads. ytdlpMu serialises the admin update endpoint
	// (copy + "-U" + verify) so two concurrent update calls can't race.
	YtDlpBin  string
	YtDlpPath string
	ytdlpMu   sync.Mutex
	Log       *slog.Logger
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/info", s.info)
		r.Post("/auth/login", s.login)
		r.Group(func(a chi.Router) {
			a.Use(s.Auth.Middleware)
			s.authRoutes(a)
			s.browseRoutes(a)
			s.streamRoutes(a)
			s.personalRoutes(a)
			s.tagRoutes(a)
			s.lyricsRoutes(a)
			s.artworkRoutes(a)
			s.downloadRoutes(a)
			s.recommendRoutes(a)
			s.channelRoutes(a)
			s.previewRoutes(a)
			s.videoRoutes(a)
			a.Group(func(adm chi.Router) {
				adm.Use(auth.RequireAdmin)
				s.adminRoutes(adm)
			})
		})
		r.NotFound(func(w http.ResponseWriter, r *http.Request) { writeError(w, 404, "no such endpoint") })
	})
	r.Handle("/*", webui.HandlerWith(webui.Options{KillServiceWorker: s.OfflineCacheOff}))
	return r
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// codeForStatus derives a stable machine-readable code from an HTTP status,
// used whenever a call site doesn't pass its own (via writeCoded). The web
// client falls back to showing the English message for any code it doesn't
// recognise, so a generic status-derived code is always a safe default.
func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "too_large"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusBadGateway:
		return "upstream_failed"
	case http.StatusGatewayTimeout:
		return "timeout"
	default:
		if status >= 500 {
			return "server_error"
		}
		return "error"
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeCoded(w, code, codeForStatus(code), msg)
}

// writeCoded is like writeError but with an explicit stable "code" the web
// client can key a translation off (see error.<code> in web/src/i18n). Every
// API error body is {"error": "<English message>", "code": "<stable_code>"}.
func writeCoded(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

var errBody = errors.New("invalid JSON body")

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errBody
	}
	return nil
}

// decodeOptionalJSON is decodeJSON for a body that may be absent: an empty
// body leaves v untouched.
func decodeOptionalJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return errBody
	}
	return nil
}

func idParam(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return id, err == nil && id > 0
}
