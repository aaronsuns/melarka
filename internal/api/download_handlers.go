package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/download"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func (s *Server) downloadRoutes(a chi.Router) {
	a.Get("/youtube/search", s.youtubeSearch)
	a.Get("/youtube/playlist", s.youtubePlaylist)
	a.Get("/youtube/playlist/entries", s.youtubePlaylistEntries)
	a.Post("/downloads", s.createDownload)
	a.Get("/downloads", s.listDownloads)
	a.Get("/downloads/tracks", s.downloadTracks)
	a.Delete("/downloads/{id}", s.cancelDownload)
	a.Post("/downloads/{id}/retry", s.retryDownload)
}

const (
	searchCacheTTL = 10 * time.Minute
	searchCacheCap = 200
	// searchTokens bounds concurrent yt-dlp search processes: a /youtube/search
	// query (video + playlist searches, run together) holds searchQueryTokens,
	// a /youtube/playlist lookup holds playlistTokens.
	searchTokens      = 4
	searchQueryTokens = 2
	playlistTokens    = 1
	searchTimeout     = 30 * time.Second
	// searchPage is the first /youtube/search page and the step "show more"
	// grows it by (up to ytdlp.MaxSearchResults); entriesPage and entriesCap
	// do the same for a playlist's entries (the cap is the download cap).
	searchPage  = 10
	entriesPage = 50
	entriesCap  = 200
)

// pageSize reads the optional n parameter: absent is step, otherwise a
// positive integer rounded up to a whole number of steps (so each page is
// one cache entry) and clamped to limit. ok is false for anything else.
func pageSize(r *http.Request, step, limit int) (n int, ok bool) {
	raw := r.URL.Query().Get("n")
	if raw == "" {
		return step, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, false
	}
	return min((n+step-1)/step*step, limit), true
}

// searchCacheEntry is one cached YouTube search result.
type searchCacheEntry struct {
	result  ytdlp.SearchResult
	expires time.Time
}

// searchCache is a small in-memory cache for YouTube search results, keyed
// by normalised query. Its zero value is ready to use. Bounded at
// searchCacheCap entries (oldest evicted first) so a stream of distinct
// queries can't grow it without limit, and safe for concurrent requests.
type searchCache struct {
	mu    sync.Mutex
	items map[string]searchCacheEntry
	order []string // insertion order, oldest first
}

func (c *searchCache) get(key string, now time.Time) (ytdlp.SearchResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok || now.After(e.expires) {
		return ytdlp.SearchResult{}, false
	}
	return e.result, true
}

func (c *searchCache) set(key string, result ytdlp.SearchResult, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]searchCacheEntry{}
	}
	if _, exists := c.items[key]; !exists {
		if len(c.order) >= searchCacheCap {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.items, oldest)
		}
		c.order = append(c.order, key)
	}
	c.items[key] = searchCacheEntry{result: result, expires: now.Add(searchCacheTTL)}
}

func (s *Server) youtubeSearch(w http.ResponseWriter, r *http.Request) {
	clean, err := ytdlp.CleanQuery(r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, 400, "bad query")
		return
	}
	n, ok := pageSize(r, searchPage, ytdlp.MaxSearchResults)
	if !ok {
		writeCoded(w, 400, "bad_request", "bad page size")
		return
	}
	key := strings.ToLower(clean)
	if n > searchPage {
		// "Show more": a larger ytsearch for the videos alone (one process,
		// one token; the client keeps the first page's playlists and drops
		// the videos it already has). The key starts with a space, which a
		// trimmed query never does.
		s.serveSearch(w, r, " more:"+strconv.Itoa(n)+":"+key, clean, 1, func(ctx context.Context) (ytdlp.SearchResult, bool, error) {
			videos, more, err := s.YT.SearchMore(ctx, clean, n)
			if err != nil {
				return ytdlp.SearchResult{}, false, err
			}
			if videos == nil {
				videos = []ytdlp.Video{}
			}
			return ytdlp.SearchResult{Videos: videos, Playlists: []ytdlp.Playlist{}, More: more}, true, nil
		}, func(res ytdlp.SearchResult) any { return res })
		return
	}
	s.serveSearch(w, r, key, clean, searchQueryTokens, func(ctx context.Context) (ytdlp.SearchResult, bool, error) {
		// Videos and playlists are searched at the same time: one limiter
		// slot, two yt-dlp processes. A playlist failure is only logged.
		var pls []ytdlp.Playlist
		var plErr error
		done := make(chan struct{})
		go func() {
			defer close(done)
			pls, plErr = s.YT.SearchPlaylists(ctx, clean)
		}()
		videos, more, err := s.YT.SearchMore(ctx, clean, searchPage)
		<-done
		if err != nil {
			return ytdlp.SearchResult{}, false, err
		}
		if plErr != nil {
			s.Log.Warn("youtube playlist search", "query", clean, "err", plErr)
			pls = nil
		}
		if videos == nil {
			videos = []ytdlp.Video{}
		}
		if pls == nil {
			pls = []ytdlp.Playlist{}
		}
		// A failed playlist search is not cached: one yt-dlp blip must not
		// hide playlists for this query for the whole TTL.
		return ytdlp.SearchResult{Videos: videos, Playlists: pls, More: more}, plErr == nil, nil
	}, func(res ytdlp.SearchResult) any { return res })
}

func (s *Server) youtubePlaylist(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("list")
	if id == "" || ytdlp.ListIDFromURL(ytdlp.PlaylistURL(id)) != id || ytdlp.IsMixID(id) {
		writeCoded(w, 400, "bad_request", "bad playlist id")
		return
	}
	// The key starts with a space, which a (trimmed) search query never does,
	// so a search for "list:<id>" can't be served as this playlist.
	s.serveSearch(w, r, " list:"+id, id, playlistTokens, func(ctx context.Context) (ytdlp.SearchResult, bool, error) {
		p, err := s.YT.PlaylistInfo(ctx, id)
		if err != nil {
			return ytdlp.SearchResult{}, false, err
		}
		return ytdlp.SearchResult{Playlists: []ytdlp.Playlist{p}}, true, nil
	}, func(res ytdlp.SearchResult) any {
		if len(res.Playlists) == 0 { // never cached under this key; don't panic if it ever is
			return ytdlp.Playlist{}
		}
		return res.Playlists[0]
	})
}

// youtubePlaylistEntries lists a playlist's first n videos (title, channel,
// duration) for a search result expanded in place; n grows by entriesPage
// up to entriesCap. Same cache and limiter as the other lookups.
func (s *Server) youtubePlaylistEntries(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("list")
	if id == "" || ytdlp.ListIDFromURL(ytdlp.PlaylistURL(id)) != id || ytdlp.IsMixID(id) {
		writeCoded(w, 400, "bad_request", "bad playlist id")
		return
	}
	n, ok := pageSize(r, entriesPage, entriesCap)
	if !ok {
		writeCoded(w, 400, "bad_request", "bad page size")
		return
	}
	key := " entries:" + strconv.Itoa(n) + ":" + id
	s.serveSearch(w, r, key, id, playlistTokens, func(ctx context.Context) (ytdlp.SearchResult, bool, error) {
		l, err := s.YT.ResolveList(ctx, ytdlp.PlaylistURL(id), n)
		if err != nil {
			return ytdlp.SearchResult{}, false, err
		}
		videos := make([]ytdlp.Video, 0, len(l.Videos))
		for _, v := range l.Videos {
			if !v.Live {
				videos = append(videos, v)
			}
		}
		// YouTube's own length says whether there is more; without it, a
		// full page might have.
		more := n < entriesCap && (l.Count > n || (l.Count == 0 && len(l.Videos) >= n))
		return ytdlp.SearchResult{Videos: videos, Playlists: []ytdlp.Playlist{{ID: id, Count: l.Count}}, More: more}, true, nil
	}, func(res ytdlp.SearchResult) any {
		count := 0
		if len(res.Playlists) > 0 {
			count = res.Playlists[0].Count
		}
		return map[string]any{"videos": res.Videos, "count": count, "more": res.More}
	})
}

// serveSearch is the cache + limiter + error mapping shared by the search and
// playlist endpoints. fn caches its own result, so one that finishes after
// every caller gave up still serves the next request for the same key.
func (s *Server) serveSearch(w http.ResponseWriter, r *http.Request, key, label string, tokens int64,
	fn func(ctx context.Context) (ytdlp.SearchResult, bool, error), body func(ytdlp.SearchResult) any) {
	res, err := s.cachedSearch(r.Context(), key, tokens, fn)
	if err != nil {
		s.searchFail(w, r, label, err)
		return
	}
	writeJSON(w, 200, body(res))
}

// cachedSearch answers key from the search cache, else runs fn through the
// search limiter (tokens: how many yt-dlp processes it runs); fn's result is
// cached when it says it may be.
func (s *Server) cachedSearch(ctx context.Context, key string, tokens int64,
	fn func(ctx context.Context) (ytdlp.SearchResult, bool, error)) (ytdlp.SearchResult, error) {
	if res, ok := s.searchCache.get(key, time.Now()); ok {
		return res, nil
	}
	return s.searchLimiter.do(ctx, key, tokens, func(ctx context.Context) (ytdlp.SearchResult, error) {
		res, cacheable, err := fn(ctx)
		if err == nil && cacheable {
			s.searchCache.set(key, res, time.Now())
		}
		return res, err
	})
}

// searchFail maps a failed YouTube lookup: 504 when the client went away,
// else 502 with yt-dlp's last line.
func (s *Server) searchFail(w http.ResponseWriter, r *http.Request, label string, err error) {
	if r.Context().Err() != nil {
		writeCoded(w, 504, "request_cancelled", "request cancelled")
		return
	}
	s.Log.Error("youtube search", "query", label, "err", err)
	writeCoded(w, 502, "youtube_search_failed", "YouTube search failed: "+ytdlp.LastLine(err.Error()))
}

// videoInput mirrors a ytdlp.Video as posted back from a search result, so a
// client can queue a download without Lark re-resolving the URL.
type videoInput struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Channel   string `json:"channel"`
	URL       string `json:"url"`
	Thumbnail string `json:"thumbnail"`
	DurationS int    `json:"duration_s"`
	ChannelID string `json:"channel_id"`
}

func (s *Server) createDownload(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL   string      `json:"url"`
		Video *videoInput `json:"video"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	u := auth.UserFrom(r.Context())
	isAdmin := u.Role == auth.RoleAdmin

	var jobs []download.Job
	var playlist *download.ListRef
	if in.Video != nil {
		validURL, err := ytdlp.ValidURL(in.Video.URL)
		if err != nil {
			s.downloadFail(w, err)
			return
		}
		// A search result's id and url always agree (both come straight from
		// yt-dlp); this only fires for a client-constructed body, where a
		// mismatched id could otherwise attach the wrong metadata (title,
		// channel, thumbnail) to whatever video the url actually points at.
		id := ytdlp.VideoIDFromURL(validURL)
		if id == "" || id != strings.TrimSpace(in.Video.ID) {
			writeCoded(w, 400, "video_id_mismatch", "video id does not match the link")
			return
		}
		// Queued under the canonical watch URL whatever shape was posted
		// (a /shorts/ or /live/ link, say), like Search and Resolve do.
		channelID := ""
		if ytdlp.IsChannelID(in.Video.ChannelID) {
			channelID = in.Video.ChannelID
		}
		v := ytdlp.Video{
			ChannelID: channelID,
			ID:        id, Title: in.Video.Title, Channel: in.Video.Channel,
			URL: ytdlp.WatchURL(id), Thumbnail: in.Video.Thumbnail, DurationS: in.Video.DurationS,
		}
		j, err := s.Downloads.EnqueueVideo(r.Context(), u.ID, v)
		if err != nil {
			s.downloadFail(w, err)
			return
		}
		jobs = []download.Job{j}
	} else {
		res, err := s.Downloads.EnqueueURL(r.Context(), u.ID, in.URL)
		if err != nil {
			s.downloadFail(w, err)
			return
		}
		jobs, playlist = res.Jobs, res.Playlist
	}
	for i := range jobs {
		// Enqueue/EnqueueVideo dedupe to an existing job that may belong to
		// someone else: a member must never learn that another user's job
		// exists, so strip ownership before it leaves this process. Admins
		// keep full visibility (they can already see every job via ?all=1).
		if !isAdmin && jobs[i].UserID != u.ID {
			jobs[i].UserID = 0
			jobs[i].Username = ""
		}
	}
	writeJSON(w, 201, download.Enqueued{Jobs: jobs, Playlist: playlist})
}

func (s *Server) listDownloads(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	all := r.URL.Query().Get("all") == "1" && u.Role == auth.RoleAdmin
	jobs, err := s.Downloads.List(r.Context(), u.ID, all, int(qInt(r, "limit")))
	if err != nil {
		s.downloadFail(w, err)
		return
	}
	writeJSON(w, 200, jobs)
}

func (s *Server) cancelDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, 400, "bad id")
		return
	}
	u := auth.UserFrom(r.Context())
	if err := s.Downloads.Cancel(r.Context(), u.ID, u.Role == auth.RoleAdmin, id); err != nil {
		s.downloadFail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) retryDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, 400, "bad id")
		return
	}
	u := auth.UserFrom(r.Context())
	if err := s.Downloads.Retry(r.Context(), u.ID, u.Role == auth.RoleAdmin, id); err != nil {
		s.downloadFail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) downloadFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, download.ErrNotFound):
		writeError(w, 404, "not found")
	case errors.Is(err, download.ErrNoTarget):
		writeCoded(w, 409, "no_download_target", "no library is set as the download target")
	case errors.Is(err, download.ErrAlreadyQueued):
		writeCoded(w, 409, "already_queued", "this video is already in the download queue")
	case errors.Is(err, ytdlp.ErrBadURL):
		writeCoded(w, 400, "youtube_only", "only YouTube links are supported")
	case errors.Is(err, ytdlp.ErrBadQuery):
		writeError(w, 400, "bad query")
	default:
		s.fail(w, err)
	}
}

// downloadTracks is "My downloads": the usable songs of a user's finished
// downloads, newest first. Members only ever see their own.
func (s *Server) downloadTracks(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	target, all := u.ID, false
	switch who := r.URL.Query().Get("user"); who {
	case "":
	case "all":
		all = true
	default:
		id, err := strconv.ParseInt(who, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, 400, "user must be a user id or all")
			return
		}
		target = id
	}
	if u.Role != auth.RoleAdmin && (all || target != u.ID) {
		writeCoded(w, 403, "own_downloads_only", "members can only list their own downloads")
		return
	}
	ids, err := s.Downloads.TrackIDs(r.Context(), target, all, 1000)
	if err != nil {
		s.downloadFail(w, err)
		return
	}
	ts, err := s.Library.TracksByIDs(r.Context(), u.ID, ids)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, ts)
}
