package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/channels"
	"github.com/aaronsuns/lark-server/internal/preview"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// mixSize is how many entries of a video's Mix "related" asks for.
const mixSize = 25

// videoRoutes is 视频: YouTube search, related videos, a
// thumbnail proxy, and each user's own history and 为你推荐. Every signed-in
// user; off (409 channels_off) with Channels.
func (s *Server) videoRoutes(a chi.Router) {
	a.Group(func(c chi.Router) {
		c.Use(s.channelsOn)
		c.Get("/videos/search", s.videoSearch)
		c.Get("/videos/{id}/related", s.videoRelated)
		c.Get("/videos/{id}/thumbnail", s.videoThumbnail)
		c.Post("/me/video-history/watches", s.recordWatch)
		c.Get("/me/video-history", s.videoHistory)
		c.Delete("/me/video-history", s.clearVideoHistory)
		c.Get("/me/video-recommendations", s.videoRecs)
	})
}

// forBrowser: what a 视频 client may load — no live streams, thumbnails
// through Lark, never skip (the video the list is related to).
func forBrowser(vs []ytdlp.Video, skip string) []ytdlp.Video {
	out := make([]ytdlp.Video, 0, len(vs))
	for _, v := range vs {
		if v.Live || !ytdlp.IsVideoID(v.ID) || v.ID == skip {
			continue
		}
		v.Thumbnail = "/api/v1/videos/" + v.ID + "/thumbnail"
		out = append(out, v)
	}
	return out
}

func (s *Server) videoSearch(w http.ResponseWriter, r *http.Request) {
	clean, err := ytdlp.CleanQuery(r.URL.Query().Get("q"))
	if err != nil {
		writeCoded(w, 400, "bad_query", "bad query")
		return
	}
	// The key starts with a space, which a (trimmed) query never does, so it
	// can't collide with /youtube/search's entries (videos + playlists).
	res, err := s.cachedSearch(r.Context(), " vsearch:"+strings.ToLower(clean), 1, func(ctx context.Context) (ytdlp.SearchResult, bool, error) {
		v, err := s.YT.Search(ctx, clean)
		return ytdlp.SearchResult{Videos: v}, err == nil, err
	})
	if err != nil {
		s.searchFail(w, r, clean, err)
		return
	}
	videos := forBrowser(res.Videos, "")
	// record=1 is a submitted search (not typing): it seeds 为你推荐 with the first result.
	if r.URL.Query().Get("record") == "1" {
		first := ""
		if len(videos) > 0 {
			first = videos[0].ID
		}
		if err := s.Channels.RecordVideoSearch(r.Context(), auth.UserFrom(r.Context()).ID, clean, first); err != nil {
			s.Log.Warn("video search history", "err", err)
		}
	}
	writeJSON(w, 200, map[string]any{"videos": videos})
}

// videoRelated is the video's YouTube Mix: the interactive search path,
// shared with 为你推荐 through the Mix cache.
func (s *Server) videoRelated(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !ytdlp.IsVideoID(id) {
		writeCoded(w, 400, "bad_video_id", "bad video id")
		return
	}
	vs, err := s.Channels.CachedMix(r.Context(), id, func(ctx context.Context) ([]ytdlp.Video, error) {
		res, err := s.searchLimiter.do(ctx, " mix:"+id, playlistTokens, func(ctx context.Context) (ytdlp.SearchResult, error) {
			v, err := s.YT.Mix(ctx, id, mixSize)
			return ytdlp.SearchResult{Videos: v}, err
		})
		return res.Videos, err
	})
	if err != nil {
		s.searchFail(w, r, "mix "+id, err)
		return
	}
	writeJSON(w, 200, map[string]any{"videos": forBrowser(vs, id)})
}

func (s *Server) videoThumbnail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !ytdlp.IsVideoID(id) {
		writeCoded(w, 400, "bad_video_id", "bad video id")
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if s.Thumbs == nil {
		noThumbnail(w)
		return
	}
	path, err := s.Thumbs.Path(r.Context(), id)
	switch {
	case errors.Is(err, preview.ErrBadVideo):
		writeCoded(w, 400, "bad_video_id", "bad video id")
		return
	case errors.Is(err, preview.ErrBusy) || r.Context().Err() != nil:
		w.Header().Set("Cache-Control", "no-store") // not a miss: ask again next view
		writeError(w, 404, "no thumbnail")
		return
	case err != nil:
		s.Log.Debug("video thumbnail", "video", id, "err", err)
		noThumbnail(w) // the client shows its own tile
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=604800")
	http.ServeFile(w, r, path)
}

// noThumbnail is the 404 for a thumbnail Lark has none of; the browser asks
// again after 10 minutes, as long as the proxy remembers a miss.
func noThumbnail(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, max-age=600")
	writeError(w, 404, "no thumbnail")
}

func (s *Server) recordWatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		VideoID   string `json:"video_id"`
		Title     string `json:"title"`
		Channel   string `json:"channel"`
		ChannelID string `json:"channel_id"`
		DurationS int    `json:"duration_s"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	err := s.Channels.RecordWatch(r.Context(), auth.UserFrom(r.Context()).ID, ytdlp.Video{
		ID: in.VideoID, Title: in.Title, Channel: in.Channel, ChannelID: in.ChannelID, DurationS: in.DurationS})
	switch {
	case errors.Is(err, channels.ErrBadID):
		writeCoded(w, 400, "bad_video_id", "bad video id")
	case err != nil:
		s.Log.Error("video watch", "err", err)
		writeError(w, 500, "internal error")
	default:
		w.WriteHeader(204)
	}
}

func (s *Server) videoHistory(w http.ResponseWriter, r *http.Request) {
	h, err := s.Channels.VideoHistory(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		s.Log.Error("video history", "err", err)
		writeError(w, 500, "internal error")
		return
	}
	writeJSON(w, 200, h)
}

func (s *Server) clearVideoHistory(w http.ResponseWriter, r *http.Request) {
	if err := s.Channels.ClearVideoHistory(r.Context(), auth.UserFrom(r.Context()).ID); err != nil {
		s.Log.Error("video history clear", "err", err)
		writeError(w, 500, "internal error")
		return
	}
	w.WriteHeader(204)
}

func (s *Server) videoRecs(w http.ResponseWriter, r *http.Request) {
	recs, err := s.Channels.VideoRecs(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		s.Log.Error("video recommendations", "err", err)
		writeError(w, 500, "internal error")
		return
	}
	writeJSON(w, 200, recs)
}
