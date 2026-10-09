// The narrow seam between the player and the offline cache, so neither
// imports the other: the player reports buffering and plays, the cache
// answers "can this track play right now?". With no cache registered (tests,
// unsupported browsers) everything behaves exactly as without it.

import type { Track } from "../api/types";

let buffering = false;
const bufferingListeners = new Set<() => void>();
export type LocalKind = "favorite" | "recent" | "lookahead";
export interface CachedFavorite {
  track: Track;
  lastPlayedAt: number;
}
export interface OfflineHooks {
  played: (t: Track, finished: boolean) => void;
  playable: (id: number) => boolean;
  // Which kind of copy of this track is cached, if any.
  local?: (id: number) => LocalKind | null;
  favorites?: () => CachedFavorite[];
  // Download these (the queue's next tracks); `keep` (the playing and
  // previous tracks) stays cached. Returns the ids accepted.
  lookahead?: (tracks: Track[], keep: number[]) => number[];
}

let playedHandler: ((t: Track, finished: boolean) => void) | null = null;
let playableHandler: ((id: number) => boolean) | null = null;
let hooks: OfflineHooks | null = null;

export function setPlayerBuffering(b: boolean): void {
  if (b === buffering) return;
  buffering = b;
  bufferingListeners.forEach((fn) => fn());
}
export const isPlayerBuffering = () => buffering;
export function onPlayerBuffering(fn: () => void): () => void {
  bufferingListeners.add(fn);
  return () => void bufferingListeners.delete(fn);
}

export function notePlayed(t: Track, finished: boolean): void {
  playedHandler?.(t, finished);
}
export function playableNow(id: number): boolean {
  return playableHandler ? playableHandler(id) : true;
}

/** The kind of cached copy of this track, or null (none, or no offline cache). */
export function localKind(id: number): LocalKind | null {
  return hooks?.local?.(id) ?? null;
}
/** The cached favorites (empty without an offline cache). */
export function cachedFavorites(): CachedFavorite[] {
  return hooks?.favorites?.() ?? [];
}
/** Asks the offline cache to download the queue's next tracks; the ids it took on (none without one). */
export function requestLookahead(tracks: Track[], keep: number[]): number[] {
  return hooks?.lookahead?.(tracks, keep) ?? [];
}

/** Registered by the offline cache; returns the unregister function. */
export function connectOffline(h: OfflineHooks): () => void {
  playedHandler = h.played;
  playableHandler = h.playable;
  hooks = h;
  return () => {
    if (playedHandler === h.played) playedHandler = null;
    if (playableHandler === h.playable) playableHandler = null;
    if (hooks === h) hooks = null;
  };
}
