import type {
  Album,
  Artist,
  ChannelHit,
  ChannelGroup,
  ChannelPageData,
  ChannelSettings,
  ChannelSuggestions,
  DownloadJob,
  Enqueued,
  Episode,
  KeepResult,
  Library,
  Lyrics,
  LyricsCandidate,
  LyricsRejected,
  MyChannel,
  MyChannels,
  Page,
  PlayEvent,
  Playlist,
  Prefs,
  PreviewInfo,
  Quality,
  RandomFavorites,
  Recommendations,
  Role,
  ScanStatus,
  ServerQueue,
  TagCount,
  Track,
  TrackTag,
  TrashItem,
  User,
  VideoHistory,
  VideoRecs,
  VocabEntry,
  YTPlaylist,
  YTPlaylistEntries,
  YTSearchResult,
  YTVideo,
} from "./types";

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public code?: string,
    // Retry-After, in seconds, when the server sent one (503 preview_retry).
    public retryAfter?: number,
  ) {
    super(message);
  }
}

// Told about every successful favorite toggle (the offline cache pins and
// unpins on it), wherever in the app it happened.
const favoriteListeners = new Set<(id: number, on: boolean) => void>();
export function onFavoriteSet(fn: (id: number, on: boolean) => void): () => void {
  favoriteListeners.add(fn);
  return () => void favoriteListeners.delete(fn);
}

let onUnauthorized: () => void = () => {};
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

type Params = Record<string, string | number | undefined>;

function qs(params: Params): string {
  const parts = Object.entries(params)
    .filter(([, v]) => v !== undefined && v !== "")
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`);
  return parts.length ? `?${parts.join("&")}` : "";
}

async function req<T>(method: string, path: string, body?: unknown, opts: { noAuthRedirect?: boolean; keepalive?: boolean } = {}): Promise<T> {
  const init: RequestInit = { method, credentials: "same-origin" };
  // keepalive lets the request outlive the page (pagehide/app switch).
  if (opts.keepalive) init.keepalive = true;
  if (body !== undefined) {
    init.body = JSON.stringify(body);
    init.headers = { "Content-Type": "application/json" };
  }
  const res = await fetch(`/api/v1${path}`, init);
  if (res.status === 401 && !opts.noAuthRedirect) onUnauthorized();
  if (!res.ok) {
    let msg = res.statusText || `HTTP ${res.status}`;
    let code: string | undefined;
    try {
      const j = await res.json();
      if (j && typeof j.error === "string") msg = j.error;
      if (j && typeof j.code === "string") code = j.code;
    } catch {
      /* non-JSON error body */
    }
    const ra = Number(res.headers?.get("Retry-After") ?? "");
    throw new ApiError(res.status, msg, code, Number.isFinite(ra) && ra > 0 ? ra : undefined);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

export const api = {
  // offline_cache false is the server's kill switch for the web offline cache;
  // channels false: Channels is switched off on the server.
  info: () => req<{ name: string; version?: string; language: string; languages: string[]; offline_cache?: boolean; channels?: boolean }>("GET", "/info", undefined, { noAuthRedirect: true }),
  login: async (username: string, password: string) =>
    (await req<{ user: User }>("POST", "/auth/login", { username, password, device_name: deviceName() }, { noAuthRedirect: true })).user,
  logout: () => req<void>("POST", "/auth/logout"),
  me: () => req<User>("GET", "/me"),
  preferences: () => req<Prefs>("GET", "/me/preferences"),
  savePreferences: (p: Prefs) => req<Prefs>("PUT", "/me/preferences", p),
  tracks: (p: Params) => req<Page<Track>>("GET", `/tracks${qs(p)}`),
  track: (id: number) => req<Track>("GET", `/tracks/${id}`),
  albums: (cursor?: string) => req<Page<Album>>("GET", `/albums${qs({ cursor, limit: 100 })}`),
  album: (id: number) => req<{ album: Album; tracks: Track[] }>("GET", `/albums/${id}`),
  artists: (cursor?: string) => req<Page<Artist>>("GET", `/artists${qs({ cursor, limit: 100 })}`),
  artist: (id: number) => req<{ artist: Artist; albums: Album[] }>("GET", `/artists/${id}`),
  search: (q: string) => req<{ tracks: Track[]; albums: Album[]; artists: Artist[] }>("GET", `/search${qs({ q })}`),
  searchHistory: () => req<string[]>("GET", "/me/search-history"),
  recordSearch: (query: string) => req<void>("POST", "/me/search-history", { query }),
  clearSearchHistory: () => req<void>("DELETE", "/me/search-history"),
  setFavorite: async (id: number, on: boolean) => {
    await req<void>(on ? "PUT" : "DELETE", `/favorites/${id}`);
    favoriteListeners.forEach((fn) => fn(id, on));
  },
  setDislike: (id: number, on: boolean) => req<void>(on ? "PUT" : "DELETE", `/dislikes/${id}`),
  playlists: () => req<Playlist[]>("GET", "/playlists"),
  createPlaylist: (name: string, track_ids: number[]) => req<Playlist>("POST", "/playlists", { name, track_ids }),
  playlist: (id: number) => req<{ playlist: Playlist; tracks: Track[] }>("GET", `/playlists/${id}`),
  updatePlaylist: (id: number, patch: { name?: string; track_ids?: number[] }) => req<void>("PUT", `/playlists/${id}`, patch),
  deletePlaylist: (id: number) => req<void>("DELETE", `/playlists/${id}`),
  postEvents: async (events: PlayEvent[]) => (await req<{ accepted: number }>("POST", "/events/play", { events })).accepted,
  queue: () => req<{ queue: ServerQueue; tracks: Track[] }>("GET", "/queue"),
  saveQueue: (q: { track_ids: number[]; current_index: number; position_ms: number }, opts: { keepalive?: boolean } = {}) =>
    req<void>("PUT", "/queue", q, { keepalive: opts.keepalive }),
  // Ask the server to transcode the next queued tracks ahead of time.
  prepareTracks: (ids: number[], quality: Quality) => req<{ queued: number }>("POST", "/tracks/prepare", { ids, quality }),
  episodeProgress: (id: string, body: { position_s: number; played?: boolean }, opts: { keepalive?: boolean } = {}) =>
    req<void>("PUT", `/episodes/${encodeURIComponent(id)}/progress`, body, { keepalive: opts.keepalive }),
  myChannels: () => req<MyChannels>("GET", "/channels"),
  searchChannels: async (q: string) => (await req<{ channels: ChannelHit[] }>("GET", `/channels/search${qs({ q })}`)).channels,
  resolveChannel: (url: string) => req<ChannelHit>("POST", "/channels/resolve", { url }),
  followChannel: (by: { id: string } | { url: string }) => req<MyChannel>("POST", "/channels/follow", by),
  unfollowChannel: (id: string) => req<void>("DELETE", `/channels/${encodeURIComponent(id)}/follow`),
  channelSettings: (id: string, s: ChannelSettings) => req<ChannelSettings>("PUT", `/channels/${encodeURIComponent(id)}/settings`, s),
  channelPage: (id: string) => req<ChannelPageData>("GET", `/channels/${encodeURIComponent(id)}`),
  channelSuggestions: () => req<ChannelSuggestions>("GET", "/me/channel-suggestions"),
  refreshChannelSuggestions: () => req<void>("POST", "/me/channel-suggestions/refresh"),
  dismissSuggestion: (kind: "channels" | "videos", id: string) =>
    req<void>("PUT", `/me/channel-suggestions/${kind}/${encodeURIComponent(id)}/dismiss`),
  startPreview: (p: { video_id: string; media: "audio" | "video" | "hd"; title?: string; channel?: string; duration_s?: number }) =>
    req<PreviewInfo>("POST", "/previews", p),
  preview: (id: number) => req<PreviewInfo>("GET", `/previews/${id}`),
  keepPreview: (id: number, to: "music" | "channel") => req<KeepResult>("POST", `/previews/${id}/keep`, { to }),
  latestEpisodes: (p: { before?: number; after?: number; limit?: number; order?: "asc" | "desc"; unplayed?: 1 } = {}) =>
    req<{ items: Episode[]; unplayed: number }>("GET", `/episodes/latest${qs(p)}`),
  episodesByChannel: async (per?: number) => (await req<{ groups: ChannelGroup[] }>("GET", `/episodes/by-channel${qs({ per })}`)).groups,
  keptEpisodes: async () => (await req<{ items: Episode[] }>("GET", "/episodes/kept")).items,
  episode: (id: string) => req<Episode>("GET", `/episodes/${encodeURIComponent(id)}`),
  setEpisodeKeep: (id: string, on: boolean) => req<void>(on ? "PUT" : "DELETE", `/episodes/${encodeURIComponent(id)}/keep`),
  setEpisodeHidden: (id: string, on: boolean) => req<void>(on ? "PUT" : "DELETE", `/episodes/${encodeURIComponent(id)}/hide`),
  radio: (n: number, exclude: number[]) => req<Track[]>("GET", `/radio/next${qs({ n, exclude: exclude.join(",") || undefined })}`),
  trashTrack: (id: number) => req<void>("DELETE", `/tracks/${id}`),
  // The admin console's broken (damaged) files: up to 500, and how many there are in all.
  brokenTracks: () => req<{ items: Track[]; total: number }>("GET", "/admin/broken-tracks"),
  // Every broken file to the trash at once; how many went. expect: the total
  // the admin confirmed (409 broken_count_changed when it is no longer so).
  trashBroken: async (expect: number) => (await req<{ trashed: number }>("DELETE", `/admin/broken-tracks${qs({ expect })}`)).trashed,
  setStatus: (id: number, status: "kept" | "pending") => req<void>("PUT", `/tracks/${id}/status`, { status }),
  randomTracks: (n: number, exclude: number[], opts: { tag?: string } = {}) =>
    req<Track[]>("GET", `/tracks/random${qs({ n, exclude: exclude.join(",") || undefined, tag: opts.tag })}`),
  // The user's favorites (minus exclude); source "all" means they had none
  // playable and the server fell back to the whole library.
  randomFavorites: (n: number, exclude: number[]) =>
    req<RandomFavorites>("GET", `/tracks/random${qs({ source: "favorites", n, exclude: exclude.join(",") || undefined })}`),
  editTrack: (id: number, patch: { title?: string; artist?: string; album?: string; year?: number }) =>
    req<void>("PATCH", `/tracks/${id}`, patch),
  tags: () => req<TagCount[]>("GET", "/tags"),
  vocabulary: () => req<VocabEntry[]>("GET", "/tags/vocabulary"),
  trackTags: (id: number) => req<TrackTag[]>("GET", `/tracks/${id}/tags`),
  setTrackTags: (id: number, tags: TrackTag[]) => req<void>("PUT", `/tracks/${id}/tags`, { source: "manual", tags }),
  lyrics: (id: number) => req<Lyrics>("GET", `/tracks/${id}/lyrics`),
  // Anyone signed in, for everyone: shift the shown lyrics; report them wrong.
  // Both name the shown lyrics (409 lyrics_changed when others are shown now).
  setLyricsOffset: (id: number, offsetMs: number, lyricsId: number) =>
    req<void>("PUT", `/tracks/${id}/lyrics/offset`, { offset_ms: offsetMs, lyrics_id: lyricsId }),
  reportWrongLyrics: (id: number, lyricsId: number) => req<Lyrics & { report_id: number }>("POST", `/tracks/${id}/lyrics/wrong`, { lyrics_id: lyricsId }),
  // The reporter (or an admin) takes a report back.
  undoWrongLyrics: (id: number, reportId: number) => req<Lyrics>("POST", `/tracks/${id}/lyrics/wrong/undo`, { report_id: reportId }),
  lyricsRejected: (id: number) => req<LyricsRejected[]>("GET", `/tracks/${id}/lyrics/rejected`),
  unrejectLyrics: (id: number, rejectedId: number) => req<void>("DELETE", `/tracks/${id}/lyrics/rejected/${rejectedId}`),
  lyricsCandidates: (id: number) => req<LyricsCandidate[]>("GET", `/tracks/${id}/lyrics/candidates`),
  selectLyrics: (id: number, candidateId: number) => req<void>("PUT", `/tracks/${id}/lyrics`, { candidate_id: candidateId }),
  // override: search with this title and/or artist instead of the server's cleaned ones.
  // broad: match loosely (any singer, looser title/duration) and keep up to 10 candidates.
  refreshLyrics: (id: number, override?: { title?: string; artist?: string; broad?: boolean }) => req<Lyrics>("POST", `/tracks/${id}/lyrics/refresh`, override),
  // "This song has no lyrics": nothing shown, and automatic lookups stop.
  noLyrics: (id: number) => req<void>("PUT", `/tracks/${id}/lyrics`, { none: true }),
  deleteLyricsCandidate: (id: number, candidateId: number) => req<void>("DELETE", `/tracks/${id}/lyrics/candidates/${candidateId}`),

  recommendations: () => req<Recommendations>("GET", "/me/recommendations"),
  refreshRecommendations: () => req<void>("POST", "/me/recommendations/refresh"),
  dismissRecommendation: (videoId: string) => req<void>("PUT", `/me/recommendations/${encodeURIComponent(videoId)}/dismiss`),

  // 视频 (video). record: a submitted search (not typing) seeds 为你推荐.
  videoSearch: (q: string, record: boolean) => req<{ videos: YTVideo[] }>("GET", `/videos/search${qs({ q, record: record ? "1" : undefined })}`),
  videoRelated: (id: string) => req<{ videos: YTVideo[] }>("GET", `/videos/${encodeURIComponent(id)}/related`),
  // The server's decoder is strict (an unknown field is a 400): only these five go.
  recordWatch: (v: { video_id: string; title: string; channel: string; channel_id?: string; duration_s: number }) =>
    req<void>("POST", "/me/video-history/watches", {
      video_id: v.video_id, title: v.title, channel: v.channel, channel_id: v.channel_id, duration_s: v.duration_s,
    }),
  videoHistory: () => req<VideoHistory>("GET", "/me/video-history"),
  clearVideoHistory: () => req<void>("DELETE", "/me/video-history"),
  videoRecommendations: () => req<VideoRecs>("GET", "/me/video-recommendations"),

  youtubeSearch: (q: string, n?: number) => req<YTSearchResult>("GET", `/youtube/search${qs({ q, n })}`),
  youtubePlaylistEntries: (list: string, n?: number) => req<YTPlaylistEntries>("GET", `/youtube/playlist/entries${qs({ list, n })}`),
  youtubePlaylist: (list: string) => req<YTPlaylist>("GET", `/youtube/playlist${qs({ list })}`),
  enqueue: (input: { url: string } | { video: YTVideo }) => req<Enqueued>("POST", "/downloads", input),
  createDownload: async (input: { url: string } | { video: YTVideo }) => (await req<Enqueued>("POST", "/downloads", input)).jobs,
  downloads: (all?: boolean) => req<DownloadJob[]>("GET", `/downloads${qs({ all: all ? 1 : undefined })}`),
  downloadTracks: (user?: number | "all") => req<Track[]>("GET", `/downloads/tracks${qs({ user })}`),
  cancelDownload: (id: number) => req<void>("DELETE", `/downloads/${id}`),
  retryDownload: (id: number) => req<void>("POST", `/downloads/${id}/retry`),

  changePassword: (current: string, next: string) => req<void>("PUT", "/me/password", { current, new: next }),

  users: () => req<User[]>("GET", "/users"),
  createUser: (u: { username: string; password: string; role: Role }) => req<User>("POST", "/users", u),
  deleteUser: (id: number) => req<void>("DELETE", `/users/${id}`),
  resetPassword: (id: number, password: string) => req<void>("PUT", `/users/${id}/password`, { password }),

  libraries: () => req<Library[]>("GET", "/admin/libraries"),
  scanStatus: () => req<ScanStatus[]>("GET", "/admin/scan/status"),
  triggerScan: (libraryId?: number) => req<void>("POST", `/admin/scan${qs({ library: libraryId })}`),

  trash: () => req<TrashItem[]>("GET", "/trash"),
  restoreTrash: (trackId: number) => req<void>("POST", `/trash/${trackId}/restore`),
  emptyTrash: async () => (await req<{ purged: number }>("DELETE", "/trash")).purged,

  ytdlpInfo: () => req<{ version: string; path: string }>("GET", "/admin/ytdlp"),
  ytdlpUpdate: () => req<{ version: string; path: string }>("POST", "/admin/ytdlp/update"),
};

export function streamUrl(id: number, q: Quality): string {
  return `/api/v1/tracks/${id}/stream?quality=${q}`;
}

export function episodeStreamUrl(id: string, kind: "audio" | "video"): string {
  return `/api/v1/episodes/${encodeURIComponent(id)}/stream?kind=${kind}`;
}

function deviceName(): string {
  const ua = navigator.userAgent;
  const kind = /iPhone/.test(ua) ? "iPhone" : /iPad/.test(ua) ? "iPad" : /Android/.test(ua) ? "Android" : "Browser";
  return `${kind} (web)`;
}
