// Web objects ↔ native queue items (lark-ios Sources/Bridge/Item.swift).
import type { Episode, Track } from "../api/types";
import { t } from "../i18n/i18n";
import type { NativeItem } from "./bridge";

export function trackItem(tr: Track): NativeItem {
  return { kind: "track", id: String(tr.id), title: tr.title, artist: tr.artist, album: tr.album, durationMs: tr.duration_ms, meta: tr };
}

export function episodeItem(ep: Episode): NativeItem {
  // Not the description: native echoes every item back on each queue
  // event, and the queue never shows it (as the stored queue, episodeQueue.ts).
  const { description: _d, ...meta } = ep;
  return {
    kind: "episode",
    id: ep.video_id,
    title: ep.title,
    artist: ep.channel_title,
    album: t("channels.title"),
    durationMs: Math.round(ep.duration_s * 1000),
    meta,
  };
}

const isObject = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object" && !Array.isArray(v);

/**
 * The web object of a native item: its `meta` (native echoes back what it
 * was given, and native-made items carry the server's JSON). An item that
 * somehow has none gets a minimal object from the item's own fields.
 */
export function fromItem(i: NativeItem & { kind: "track" }): Track;
export function fromItem(i: NativeItem & { kind: "episode" }): Episode;
export function fromItem(i: NativeItem): Track | Episode;
export function fromItem(i: NativeItem): Track | Episode {
  if (isObject(i.meta)) return i.meta as Track | Episode;
  if (i.kind === "track") {
    const base: Track = {
      id: Number(i.id), title: i.title ?? "", artist: i.artist ?? "", artist_id: null, album: i.album ?? "", album_id: null, year: null,
      duration_ms: i.durationMs ?? 0, codec: "", lossless: false, bitrate: 0, status: "kept", broken: false, broken_reason: "",
      favorite: false, disliked: false, library_id: 0, path: "", added_at: 0, track_no: null, disc_no: null,
    };
    return base;
  }
  const base: Episode = {
    video_id: i.id, channel_id: "", channel_title: i.artist ?? "", title: i.title ?? "", published_at: 0, duration_s: (i.durationMs ?? 0) / 1000,
    kind: "", thumbnail: "", audio: null, video: null, position_s: 0, played: false, kept: false,
  };
  return base;
}
