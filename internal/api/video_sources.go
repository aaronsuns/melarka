package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

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

// searchSource reads /youtube/search's source parameter: empty is the first
// enabled source. Writes 400 for an unknown source and 409 source_disabled
// for one that is off (or when none is on).
func (s *Server) searchSource(w http.ResponseWriter, r *http.Request) (ytdlp.Source, bool) {
	raw := r.URL.Query().Get("source")
	if raw == "" {
		on := s.enabledSources(r.Context())
		if len(on) == 0 {
			writeCoded(w, 409, "source_disabled", "no video source is turned on on this server")
			return "", false
		}
		return on[0], true
	}
	src := ytdlp.Source(raw)
	if !slices.Contains(ytdlp.Sources, src) {
		writeCoded(w, 400, "bad_request", "source must be youtube or bilibili")
		return "", false
	}
	return src, s.requireSource(w, r, src)
}

// bilibiliSearch is /youtube/search for Bilibili: videos only (Bilibili has no
// playlist search), Bilibili's own order, its own cache entries (the key
// starts with a space, which a trimmed query never does) and one limiter
// token (one yt-dlp process).
func (s *Server) bilibiliSearch(w http.ResponseWriter, r *http.Request, clean string) {
	n, ok := pageSize(r, searchPage, ytdlp.MaxBilibiliResults)
	if !ok {
		writeCoded(w, 400, "bad_request", "bad page size")
		return
	}
	key := " bili:" + strconv.Itoa(n) + ":" + strings.ToLower(clean)
	res, err := s.cachedSearch(r.Context(), key, 1, func(ctx context.Context) (ytdlp.SearchResult, bool, error) {
		videos, more, err := s.YT.SearchSource(ctx, ytdlp.SourceBilibili, clean, n)
		if err != nil {
			return ytdlp.SearchResult{}, false, err
		}
		return ytdlp.SearchResult{Videos: videos, Playlists: []ytdlp.Playlist{}, More: more}, true, nil
	})
	if err != nil {
		s.bilibiliFail(w, r, clean, err)
		return
	}
	writeJSON(w, 200, res)
}

// bilibiliFail maps a failed Bilibili lookup: 503 bilibili_throttled for
// Bilibili's anti-bot 412 (try again in a minute), else 502.
func (s *Server) bilibiliFail(w http.ResponseWriter, r *http.Request, label string, err error) {
	switch {
	case r.Context().Err() != nil:
		writeCoded(w, 504, "request_cancelled", "request cancelled")
	case errors.Is(err, ytdlp.ErrBilibiliThrottled):
		s.Log.Warn("bilibili search throttled", "query", label)
		writeCoded(w, 503, "bilibili_throttled", "Bilibili is busy, try again in a minute")
	default:
		s.Log.Error("bilibili search", "query", label, "err", err)
		writeCoded(w, 502, "bilibili_search_failed", "Bilibili search failed: "+ytdlp.LastLine(err.Error()))
	}
}
