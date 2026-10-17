import type { Track, YTVideo } from "../api/types";
import { coverUrl, type CoverSize } from "../components/Cover";
import { streamUrl } from "../api/client";
import type { Quality } from "../api/types";

// A YouTube video played straight from a YouTube playlist ("play all"), not
// downloaded: in the queue it is a Track with youtube_id set and a negative id
// derived from the video id (stable, and never a library track's). It streams
// from the caller's audio preview of the video, which the server starts on the
// first request; everything that needs a library track (favorites, lyrics,
// play events, the server queue) leaves it out.

export function isYouTube(t: Track | null | undefined): boolean {
  return !!t?.youtube_id;
}

function negativeId(videoId: string): number {
  let h = 2166136261;
  for (const ch of videoId) h = Math.imul(h ^ ch.codePointAt(0)!, 16777619);
  return -((h >>> 0) + 1); // −1 … −2³²: safe integers, never 0
}

export function youtubeTrack(v: YTVideo): Track {
  return {
    id: negativeId(v.id),
    youtube_id: v.id,
    title: v.title,
    artist: v.channel,
    artist_id: null,
    album: "YouTube",
    album_id: null,
    year: null,
    duration_ms: v.duration_s * 1000,
    codec: "aac",
    lossless: false,
    bitrate: 0,
    status: "pending",
    broken: false,
    broken_reason: "",
    favorite: false,
    disliked: false,
    library_id: 0,
    path: "",
    added_at: 0,
    track_no: null,
    disc_no: null,
  };
}

/** The audio of a track: the library stream at quality q, or a YouTube track's preview. */
export function trackStreamUrl(t: Track, q: Quality): string {
  if (!t.youtube_id) return streamUrl(t.id, q);
  const qs = new URLSearchParams({ title: t.title, channel: t.artist, duration_s: String(Math.round(t.duration_ms / 1000)) });
  return `/api/v1/previews/by-video/${encodeURIComponent(t.youtube_id)}/stream?${qs}`;
}

/** A track's cover: the library's artwork, or a YouTube track's thumbnail (through Lark). */
export function trackCoverUrl(t: Track, size: CoverSize = 300): string {
  return t.youtube_id ? `/api/v1/videos/${encodeURIComponent(t.youtube_id)}/thumbnail` : coverUrl("track", t.id, size);
}

/** Library track ids only (the server's queue, radio's exclusions, play events). */
export function libraryIds(ts: Track[]): number[] {
  return ts.filter((t) => !t.youtube_id).map((t) => t.id);
}

export const WatchURL = (videoId: string) => `https://www.youtube.com/watch?v=${videoId}`;
