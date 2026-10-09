// Which track plays next — decided synchronously, inside the audio
// element's own event handler. A locked iPhone suspends the page about 10 s
// after the music stops, so when the page is hidden the next track must be
// one whose bytes are already on the phone (offline cache or a preloaded
// blob); one that would have to come over a weak network is moved back
// instead, to play once it has been downloaded. With nothing like that in
// the queue, a cached favorite plays — the player should never just stop.

import type { Track } from "../api/types";
import type { QueueState } from "./queue";

export interface ChooseDeps {
  local(id: number): boolean; // its bytes are on the phone
  failed(id: number): boolean; // failed to play this session
  playable(id: number): boolean; // offline, only cached tracks can play
  favorites(): { track: Track; lastPlayedAt: number }[]; // the cached favorites
  now(): number;
  random(): number;
}

export interface ChooseOpts {
  // Only a track already on the phone will do, when there is one (the page
  // is hidden, or several tracks in a row failed).
  mustBeLocal: boolean;
  // Nothing usable left in the queue: play a cached favorite.
  favoritesAtEnd: boolean;
}

export type NextChoice =
  // Play queue entry `index`, moved to just after the current one.
  | { kind: "queue"; index: number; local: boolean; skippedOffline: boolean }
  // Put this cached favorite after the current one and play it.
  | { kind: "favorite"; track: Track }
  | { kind: "none" };

// A favorite played this recently (on this device), or this far back in the
// queue, is passed over while another one is available.
export const RECENT_MS = 3 * 3600_000;
const HISTORY = 30;

export function pickFavorite(q: QueueState, deps: ChooseDeps): Track | null {
  const cur = q.tracks[q.index]?.id;
  const history = new Set(q.tracks.slice(Math.max(0, q.index - HISTORY), q.index + 1).map((t) => t.id));
  const all = deps.favorites().filter((f) => f.track.id !== cur && !deps.failed(f.track.id));
  const fresh = all.filter((f) => !history.has(f.track.id) && deps.now() - f.lastPlayedAt > RECENT_MS);
  const pool = fresh.length > 0 ? fresh : all;
  if (pool.length === 0) return null;
  return pool[Math.min(pool.length - 1, Math.floor(deps.random() * pool.length))].track;
}

export function chooseNext(q: QueueState, opts: ChooseOpts, deps: ChooseDeps): NextChoice {
  const cands: number[] = [];
  for (let i = q.index + 1; i < q.tracks.length; i++) if (!deps.failed(q.tracks[i].id)) cands.push(i);
  // Offline: only what can play (with nothing playable, they're tried as before).
  const playable = cands.filter((i) => deps.playable(q.tracks[i].id));
  const pool = playable.length > 0 ? playable : cands;
  const skippedOffline = pool.length > 0 && pool[0] !== cands[0];
  const isLocal = (i: number) => deps.local(q.tracks[i].id);
  const first = pool[0];
  if (first !== undefined && (!opts.mustBeLocal || isLocal(first))) return { kind: "queue", index: first, local: isLocal(first), skippedOffline };
  const j = pool.find(isLocal);
  if (j !== undefined) return { kind: "queue", index: j, local: true, skippedOffline };
  if (opts.mustBeLocal || opts.favoritesAtEnd) {
    const fav = pickFavorite(q, deps);
    if (fav) return { kind: "favorite", track: fav };
  }
  if (first !== undefined) return { kind: "queue", index: first, local: false, skippedOffline };
  return { kind: "none" };
}

/** A choice that plays from the phone (not the network). */
export const isLocalChoice = (c: NextChoice) => c.kind === "favorite" || (c.kind === "queue" && c.local);
