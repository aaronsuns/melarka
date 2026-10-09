import type { Locale } from "../i18n/i18n";

export type Role = "admin" | "member";

export interface User {
  id: number;
  username: string;
  role: Role;
}

export interface Track {
  id: number;
  title: string;
  artist: string;
  artist_id: number | null;
  album: string;
  album_id: number | null;
  year: number | null;
  duration_ms: number;
  codec: string;
  lossless: boolean;
  bitrate: number;
  status: "pending" | "kept" | "trashed";
  broken: boolean;
  broken_reason: string;
  favorite: boolean;
  disliked: boolean;
  library_id: number;
  path: string;
  added_at: number;
  track_no: number | null;
  disc_no: number | null;
  // Loudness normalization: the attenuation (dB, never positive) that brings
  // this track to a common level; null until measured, absent from an older
  // server (or an older offline copy) — both play at unity.
  gain_db?: number | null;
}

export interface Album {
  id: number;
  name: string;
  artist: string;
  artist_id: number | null;
  year: number | null;
  track_count: number;
  library_id: number;
}

export interface Artist {
  id: number;
  name: string;
  track_count: number;
}

export interface Page<T> {
  items: T[];
  next_cursor: string;
}

export interface Playlist {
  id: number;
  name: string;
  track_count: number;
  updated_at: number;
}

export interface ServerQueue {
  track_ids: number[];
  current_index: number;
  position_ms: number;
  version: number;
  updated_by: string;
  updated_at: number;
}

export interface PlayEvent {
  client_event_id: string;
  track_id: number;
  started_at: number;
  played_seconds: number;
  skipped: boolean;
  quality: string;
}

export type Quality = "lossless" | "high" | "saver";

export interface RecommendationReason {
  track_id: number;
  title: string;
  artist: string;
  kind: "played" | "favorite";
}

// One "为你推荐" row: a YouTube video picked from the user's own seeds.
export interface Recommendation {
  video_id: string;
  title: string;
  channel: string;
  duration_s: number;
  thumbnail: string;
  url: string;
  reason: RecommendationReason | null;
  score: number;
}

export interface Recommendations {
  items: Recommendation[];
  refreshed_at: number | null;
  refreshing: boolean;
  enabled: boolean;
}

export interface YTVideo {
  id: string;
  title: string;
  channel: string;
  url: string;
  thumbnail: string;
  duration_s: number;
  channel_id?: string;
}

export interface YTPlaylist {
  id: string;
  title: string;
  channel: string;
  url: string;
  thumbnail: string;
  count: number; // 0 = unknown (search results); youtubePlaylist fills it
}

export interface YTSearchResult {
  videos: YTVideo[];
  playlists: YTPlaylist[];
}

export interface ListRef {
  id: number; // the Lark playlist
  name: string;
  list_id: string;
}

export interface Enqueued {
  jobs: DownloadJob[];
  playlist?: ListRef; // set when the link named a whole YouTube list
}

export type JobStatus = "queued" | "downloading" | "done" | "failed" | "cancelled";

export interface DownloadJob {
  id: number;
  user_id: number;
  username: string;
  url: string;
  video_id: string;
  title: string;
  channel: string;
  duration_s: number;
  thumbnail: string;
  status: JobStatus;
  progress: number;
  error: string;
  track_id: number | null;
  track_available: boolean; // false once the downloaded song is deleted
  created_at: number;
  updated_at: number;
}

export interface Library {
  id: number;
  name: string;
  root: string;
  download_target: boolean;
  last_scan_at: number | null;
}

// Counts from a library's last scan (Go library.ScanResult).
export interface ScanResult {
  added: number;
  updated: number;
  moved: number;
  missing: number;
  broken: number;
  unchanged: number;
}

export interface ScanStatus {
  library_id: number;
  running: boolean;
  last: ScanResult;
  last_error: string;
  finished_at: number;
}

export interface TrashItem {
  track_id: number;
  path: string;
  trashed_at: number;
  purge_at: number;
}

export type OnOpen = "shuffle_favorites" | "resume" | "nothing";

// GET /tracks/random?source=favorites
export interface RandomFavorites {
  source: "favorites" | "all";
  tracks: Track[];
}

export interface Prefs {
  language: Locale | null;
  on_open: OnOpen;
  // The current lyric line as the Bluetooth/lock-screen title. Absent from
  // an older server's answer: treat that as on (the default).
  car_lyrics?: boolean;
  // Turn loud tracks down by their gain_db. Absent from an older server's
  // answer: treat that as on (the default).
  normalize_loudness?: boolean;
}

export type TagKind = "genre" | "mood" | "scene" | "era" | "language" | "other";
export interface TagCount { name: string; kind: TagKind; count: number }
export interface VocabEntry { slug: string; kind: TagKind }
export interface TrackTag { name: string; kind: TagKind }

export interface Lyrics {
  found: boolean;
  // The shown candidate's id: offset changes and "wrong lyrics" name it.
  id?: number;
  instrumental?: boolean;
  source?: string;
  synced: boolean;
  lines?: { t_ms: number; text: string }[];
  text?: string;
  // Shared shift of the synced lines: a line shows at t_ms + offset_ms.
  // Absent from an older server: 0.
  offset_ms?: number;
}

export interface LyricsRejected {
  id: number;
  source: string;
  preview: string; // "" for rejections older than previews
  reported_by: string | null; // null: deleted by an admin
  rejected_at: number;
  restorable: boolean;
}

export interface LyricsCandidate {
  id: number;
  source: string;
  synced: boolean;
  selected: boolean;
  preview: string;
}

export interface EpisodeFile {
  status: "queued" | "downloading" | "done" | "failed" | "expired";
  progress: number;
  bytes: number;
  error: string;
}

export interface Episode {
  video_id: string;
  channel_id: string;
  channel_title: string;
  title: string;
  description?: string;
  published_at: number;
  duration_s: number;
  kind: string;
  thumbnail: string;
  audio: EpisodeFile | null;
  video: EpisodeFile | null;
  position_s: number;
  played: boolean;
  kept: boolean;
}

export interface ChannelInfo { id: string; title: string; handle: string; avatar: string; description: string; polled_at: number | null; last_error: string }
export interface ChannelSettings { media: "audio" | "video"; keep_days: number | null; paused: boolean; include_shorts: boolean; include_live: boolean }
export interface MyChannel { channel: ChannelInfo; settings: ChannelSettings; followed_at: number; unplayed: number; latest_published_at: number | null }
export interface MyChannels { channels: MyChannel[]; usage: { bytes: number; files: number; max_bytes: number }; default_keep_days: number }
export interface ChannelHit { id: string; title: string; handle: string; avatar: string; description: string; followers: number; following: boolean }
export interface ChannelGroup { channel: ChannelInfo; unplayed: number; latest_published_at: number | null; episodes: Episode[] }
export interface ChannelPageData { channel: ChannelInfo; following: ChannelSettings | null; followers: number; episodes: Episode[] }

// Channel discovery (推荐) and previews (▶ 试听).
export interface SuggestedChannel { id: string; title: string; avatar: string; score: number; sample_video_id: string }
export interface SuggestedVideo { video_id: string; title: string; channel: string; channel_id: string; duration_s: number; thumbnail: string; url: string; score: number }
export interface ChannelSuggestions { channels: SuggestedChannel[]; videos: SuggestedVideo[]; refreshed_at: number | null; refreshing: boolean }
// progress: 0–100 while an hd preview downloads; queued: still waiting for a
// download slot (a second 高清); description: the video's (untrusted text).
// merged: a video preview YouTube only had as separate streams (no format 18):
// playable once done, with progress meanwhile (live state; false once done).
export interface PreviewInfo { id: number; video_id: string; media: "audio" | "video" | "hd"; status: "downloading" | "done" | "failed"; title: string; channel: string; channel_id: string; duration_s: number; error: string; progress: number; queued: boolean; merged: boolean; description: string; stream_url: string }
export interface KeepResult { job?: DownloadJob; episode_id?: string }

// 视频: mirrors internal/channels VideoHistory / VideoRecs. Titles,
// channels, queries and reasons are YouTube's or users' text: render as text only.
// thumbnail is Lark's proxy (/api/v1/videos/<id>/thumbnail). Empty lists are [] (never null).
export interface WatchedVideo { video_id: string; title: string; channel: string; channel_id: string; duration_s: number; thumbnail: string; last_at: number }
export interface VideoHistory { watches: WatchedVideo[]; searches: string[] }
export interface VideoRec {
  video_id: string; title: string; channel: string; channel_id: string; duration_s: number; thumbnail: string; score: number;
  reason_kind: "watch" | "search"; reason: string;
}
export interface VideoRecs { items: VideoRec[]; refreshed_at: number | null; refreshing: boolean }
