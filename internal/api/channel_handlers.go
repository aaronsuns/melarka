package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/channels"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func (s *Server) channelRoutes(a chi.Router) {
	a.Group(func(c chi.Router) {
		c.Use(s.channelsOn)
		c.Get("/channels", s.myChannels)
		c.Get("/channels/search", s.searchChannels)
		c.Post("/channels/resolve", s.resolveChannel)
		c.Post("/channels/follow", s.followChannel)
		c.Get("/channels/{id}", s.channelPage)
		c.Put("/channels/{id}/settings", s.channelSettings)
		c.Delete("/channels/{id}/follow", s.unfollowChannel)
		c.Get("/episodes/latest", s.latestEpisodes)
		c.Get("/episodes/kept", s.keptEpisodes)
		c.Get("/episodes/by-channel", s.episodesByChannel)
		c.Get("/episodes/{id}", s.getEpisode)
		c.Put("/episodes/{id}/progress", s.episodeProgress)
		c.Put("/episodes/{id}/keep", s.episodeFlag(true, (*channels.Service).SetKeep))
		c.Delete("/episodes/{id}/keep", s.episodeFlag(false, (*channels.Service).SetKeep))
		c.Put("/episodes/{id}/hide", s.episodeFlag(true, (*channels.Service).SetHidden))
		c.Delete("/episodes/{id}/hide", s.episodeFlag(false, (*channels.Service).SetHidden))
		c.Get("/episodes/{id}/stream", s.streamEpisode)
		c.Get("/episodes/{id}/thumbnail", s.episodeThumbnail)
		c.Get("/me/channel-suggestions", s.channelSuggestions)
		c.Post("/me/channel-suggestions/refresh", s.refreshChannelSuggestions)
		c.Put("/me/channel-suggestions/{kind:channels|videos}/{id}/dismiss", s.dismissChannelSuggestion)
	})
}

func (s *Server) channelsOn(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Channels == nil {
			writeCoded(w, 409, "channels_off", "channels are switched off on this server")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) channelFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, channels.ErrBadID):
		writeError(w, 400, "bad id")
	case errors.Is(err, channels.ErrNotFound):
		writeError(w, 404, "not found")
	case errors.Is(err, channels.ErrNotFollowing):
		writeCoded(w, 404, "not_following", "you don't follow this channel")
	case errors.Is(err, channels.ErrFollowLimit):
		writeCoded(w, 409, "follow_limit", fmt.Sprintf("you can follow at most %d channels", channels.MaxFollows))
	case errors.Is(err, channels.ErrBadSettings):
		writeCoded(w, 400, "bad_settings", "media must be audio or video, keep_days 1 to 3650")
	case errors.Is(err, channels.ErrNotReady):
		writeCoded(w, 409, "episode_not_ready", "this episode is not downloaded yet")
	case errors.Is(err, channels.ErrExpired):
		writeCoded(w, 410, "episode_expired", "this episode's file was deleted")
	case errors.Is(err, channels.ErrTooSoon):
		writeCoded(w, 429, "too_soon", "suggestions were refreshed too often today; try again tomorrow")
	default:
		s.fail(w, err)
	}
}

func (s *Server) channelSuggestions(w http.ResponseWriter, r *http.Request) {
	sg, err := s.Channels.Suggestions(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		s.channelFail(w, err)
		return
	}
	writeJSON(w, 200, sg)
}

func (s *Server) refreshChannelSuggestions(w http.ResponseWriter, r *http.Request) {
	if err := s.Channels.RequestDiscovery(r.Context(), auth.UserFrom(r.Context()).ID); err != nil {
		s.channelFail(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) dismissChannelSuggestion(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimSuffix(chi.URLParam(r, "kind"), "s") // channels -> channel, videos -> video
	if err := s.Channels.DismissSuggestion(r.Context(), auth.UserFrom(r.Context()).ID, kind, chi.URLParam(r, "id")); err != nil {
		s.channelFail(w, err)
		return
	}
	w.WriteHeader(204)
}

// channelLookup resolves a channel page or video link through the search
// cache and limiter (interactive priority), like a YouTube search.
func (s *Server) channelLookup(ctx context.Context, link string) (ytdlp.Channel, error) {
	key := " chan:" + link
	if res, ok := s.searchCache.get(key, time.Now()); ok && len(res.Channels) == 1 {
		return res.Channels[0], nil
	}
	res, err := s.searchLimiter.do(ctx, key, playlistTokens, func(ctx context.Context) (ytdlp.SearchResult, error) {
		ch, err := s.YT.ResolveChannel(ctx, link)
		if err != nil {
			return ytdlp.SearchResult{}, err
		}
		res := ytdlp.SearchResult{Channels: []ytdlp.Channel{ch}}
		s.searchCache.set(key, res, time.Now())
		return res, nil
	})
	if err != nil {
		return ytdlp.Channel{}, err
	}
	return res.Channels[0], nil
}

func (s *Server) channelLookupFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case r.Context().Err() != nil:
		writeCoded(w, 504, "request_cancelled", "request cancelled")
	case errors.Is(err, ytdlp.ErrBadURL):
		writeCoded(w, 400, "youtube_only", "only YouTube links are supported")
	case errors.Is(err, ytdlp.ErrNoChannel):
		writeCoded(w, 404, "channel_not_found", "no YouTube channel at that link")
	default:
		s.Log.Error("channel lookup", "err", err)
		writeCoded(w, 502, "youtube_search_failed", "YouTube search failed: "+ytdlp.LastLine(err.Error()))
	}
}

// channelLink validates a pasted link (a channel page or a video).
func channelLink(w http.ResponseWriter, raw string) (string, bool) {
	u, err := ytdlp.ValidURL(raw)
	if err != nil {
		writeCoded(w, 400, "youtube_only", "only YouTube links are supported")
		return "", false
	}
	if ytdlp.ChannelPageURL(u) == "" && ytdlp.VideoIDFromURL(u) == "" {
		writeCoded(w, 400, "not_a_channel_link", "that link names neither a channel nor a video")
		return "", false
	}
	return u, true
}

func (s *Server) myChannels(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserFrom(r.Context()).ID
	list, err := s.Channels.MyChannels(r.Context(), uid)
	if err != nil {
		s.channelFail(w, err)
		return
	}
	u, err := s.Channels.Usage(r.Context())
	if err != nil {
		s.channelFail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"channels": list, "default_keep_days": s.Channels.DefaultKeepDays(),
		"usage": map[string]int64{"bytes": u.Bytes, "files": int64(u.Files), "max_bytes": s.Channels.MaxBytes}})
}

func (s *Server) searchChannels(w http.ResponseWriter, r *http.Request) {
	clean, err := ytdlp.CleanQuery(r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, 400, "bad query")
		return
	}
	uid := auth.UserFrom(r.Context()).ID
	// The leading space keeps these keys apart from music searches (always trimmed).
	s.serveSearch(w, r, " channels:"+strings.ToLower(clean), clean, playlistTokens, func(ctx context.Context) (ytdlp.SearchResult, bool, error) {
		chans, err := s.YT.SearchChannels(ctx, clean)
		if err != nil {
			return ytdlp.SearchResult{}, false, err
		}
		return ytdlp.SearchResult{Videos: []ytdlp.Video{}, Playlists: []ytdlp.Playlist{}, Channels: chans}, true, nil
	}, func(res ytdlp.SearchResult) any {
		return map[string]any{"channels": s.Channels.MarkFollowing(r.Context(), uid, res.Channels)}
	})
}

func (s *Server) resolveChannel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	link, ok := channelLink(w, in.URL)
	if !ok {
		return
	}
	ch, err := s.channelLookup(r.Context(), link)
	if err != nil {
		s.channelLookupFail(w, r, err)
		return
	}
	writeJSON(w, 200, s.Channels.MarkFollowing(r.Context(), auth.UserFrom(r.Context()).ID, []ytdlp.Channel{ch})[0])
}

func (s *Server) followChannel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := decodeOptionalJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var ch ytdlp.Channel
	switch {
	case in.ID != "":
		if !ytdlp.IsChannelID(in.ID) {
			writeError(w, 400, "bad channel id")
			return
		}
		known, err := s.Channels.Channel(r.Context(), in.ID)
		if err == nil {
			ch = known
			break
		}
		if !errors.Is(err, channels.ErrNotFound) {
			s.channelFail(w, err)
			return
		}
		if ch, err = s.channelLookup(r.Context(), ytdlp.ChannelURL(in.ID)); err != nil {
			s.channelLookupFail(w, r, err)
			return
		}
	default:
		link, ok := channelLink(w, in.URL)
		if !ok {
			return
		}
		var err error
		if ch, err = s.channelLookup(r.Context(), link); err != nil {
			s.channelLookupFail(w, r, err)
			return
		}
	}
	mc, err := s.Channels.Follow(r.Context(), auth.UserFrom(r.Context()).ID, ch)
	if err != nil {
		s.channelFail(w, err)
		return
	}
	writeJSON(w, 201, mc)
}

func (s *Server) channelPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	uid := auth.UserFrom(r.Context()).ID
	page, err := s.Channels.ChannelPage(r.Context(), uid, id)
	if errors.Is(err, channels.ErrNotFound) {
		ch, lerr := s.channelLookup(r.Context(), ytdlp.ChannelURL(id))
		if lerr != nil {
			s.channelLookupFail(w, r, lerr)
			return
		}
		if err = s.Channels.EnsureChannel(r.Context(), ch); err == nil {
			page, err = s.Channels.ChannelPage(r.Context(), uid, id)
		}
	}
	if err != nil {
		s.channelFail(w, err)
		return
	}
	writeJSON(w, 200, page)
}

func (s *Server) channelSettings(w http.ResponseWriter, r *http.Request) {
	var in channels.Settings
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	st, err := s.Channels.UpdateSettings(r.Context(), auth.UserFrom(r.Context()).ID, chi.URLParam(r, "id"), in)
	if err != nil {
		s.channelFail(w, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) unfollowChannel(w http.ResponseWriter, r *http.Request) {
	if err := s.Channels.Unfollow(r.Context(), auth.UserFrom(r.Context()).ID, chi.URLParam(r, "id")); err != nil {
		s.channelFail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) latestEpisodes(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserFrom(r.Context()).ID
	q := channels.LatestQuery{Before: qInt(r, "before"), After: qInt(r, "after"), Limit: int(qInt(r, "limit")),
		Unplayed: r.URL.Query().Get("unplayed") == "1"}
	switch r.URL.Query().Get("order") {
	case "", "desc":
	case "asc":
		q.Asc = true
	default:
		writeError(w, 400, "order must be asc or desc")
		return
	}
	items, err := s.Channels.LatestPage(r.Context(), uid, q)
	if err != nil {
		s.channelFail(w, err)
		return
	}
	n, err := s.Channels.Unplayed(r.Context(), uid)
	if err != nil {
		s.channelFail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "unplayed": n})
}

// episodesByChannel: 按频道 — each followed channel with its latest episodes (per, default 10, at most 50).
func (s *Server) episodesByChannel(w http.ResponseWriter, r *http.Request) {
	per := int(qInt(r, "per"))
	if per <= 0 || per > 50 {
		per = 10
	}
	groups, err := s.Channels.ByChannel(r.Context(), auth.UserFrom(r.Context()).ID, per)
	if err != nil {
		s.channelFail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"groups": groups})
}

func (s *Server) keptEpisodes(w http.ResponseWriter, r *http.Request) {
	items, err := s.Channels.Kept(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		s.channelFail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) getEpisode(w http.ResponseWriter, r *http.Request) {
	ep, err := s.Channels.Episode(r.Context(), auth.UserFrom(r.Context()).ID, chi.URLParam(r, "id"))
	if err != nil {
		s.channelFail(w, err)
		return
	}
	writeJSON(w, 200, ep)
}

func (s *Server) episodeProgress(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PositionS float64 `json:"position_s"`
		Played    *bool   `json:"played"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.Channels.SetProgress(r.Context(), auth.UserFrom(r.Context()).ID, chi.URLParam(r, "id"), in.PositionS, in.Played); err != nil {
		s.channelFail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) episodeFlag(on bool, set func(*channels.Service, context.Context, int64, string, bool) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := set(s.Channels, r.Context(), auth.UserFrom(r.Context()).ID, chi.URLParam(r, "id"), on); err != nil {
			s.channelFail(w, err)
			return
		}
		w.WriteHeader(204)
	}
}

func (s *Server) streamEpisode(w http.ResponseWriter, r *http.Request) {
	id, kind := chi.URLParam(r, "id"), r.URL.Query().Get("kind")
	if kind == "" {
		kind = "audio"
	}
	if !ytdlp.IsVideoID(id) || (kind != "audio" && kind != "video") {
		writeError(w, 400, "bad id or kind")
		return
	}
	path, err := s.Channels.MediaFile(r.Context(), id, kind)
	if err != nil {
		s.channelFail(w, err)
		return
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		s.channelFail(w, channels.ErrExpired)
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
	if !st.Mode().IsRegular() {
		s.channelFail(w, channels.ErrNotFound)
		return
	}
	ctype := "audio/mp4"
	if kind == "video" {
		ctype = "video/mp4"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
}

func (s *Server) episodeThumbnail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !ytdlp.IsVideoID(id) {
		writeError(w, 400, "bad id")
		return
	}
	if p, ok := s.Channels.ThumbnailPath(r.Context(), id); ok {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeFile(w, r, p)
		return
	}
	http.Redirect(w, r, ytdlp.ThumbnailURL(id), http.StatusTemporaryRedirect)
}
