// What the offline cache keeps when it is full. Pure: no storage here.

export interface Entry {
  trackId: number;
  bytes: number;
  // "favorite": currently one of the user's favorites (pinned). "recent":
  // cached because it was played; formerFavorite marks one that was cached
  // as a favorite and has since been unfavorited. "lookahead": downloaded
  // because it was about to play next in the queue (evicted first).
  kind: "favorite" | "recent" | "lookahead";
  formerFavorite?: boolean;
  lastPlayedAt: number; // ms; 0 = never played on this device
  cachedAt: number; // ms
  // The source file's version when cached (the stream's X-Lark-Version),
  // when it was last confirmed current, and what's needed to fetch it again.
  version?: string;
  checkedAt?: number;
  meta?: { title: string; duration_ms: number; bitrate: number; codec: string; artist?: string; album?: string };
}

// -1: lookahead copies, 0: plain recents, 1: unfavorited former favorites, 2: favorites.
function tier(e: Entry): number {
  if (e.kind === "favorite") return 2;
  if (e.kind === "lookahead") return -1;
  return e.formerFavorite ? 1 : 0;
}

/** Entries in the order they are evicted: first to go first. */
export function evictionOrder(entries: Entry[]): Entry[] {
  return [...entries].sort((a, b) => tier(a) - tier(b) || a.lastPlayedAt - b.lastPlayedAt || a.cachedAt - b.cachedAt);
}

export function usedBytes(entries: Entry[]): number {
  return entries.reduce((n, e) => n + e.bytes, 0);
}

/**
 * The track ids to evict so that `incoming` more bytes fit under cap, or null
 * when it can't fit without evicting a current favorite (favorites are only
 * ever evicted by lowering the cap — never to make room for something else).
 */
export function planFit(entries: Entry[], cap: number, incoming: number): number[] | null {
  let used = usedBytes(entries);
  const evict: number[] = [];
  for (const e of evictionOrder(entries)) {
    if (used + incoming <= cap) break;
    if (e.kind === "favorite") break;
    used -= e.bytes;
    evict.push(e.trackId);
  }
  return used + incoming <= cap ? evict : null;
}

/** The track ids to evict, in order, to get back under cap (after the cap was lowered). */
export function enforceCap(entries: Entry[], cap: number): number[] {
  let used = usedBytes(entries);
  const evict: number[] = [];
  for (const e of evictionOrder(entries)) {
    if (used <= cap) break;
    used -= e.bytes;
    evict.push(e.trackId);
  }
  return evict;
}

// Lookahead copies (the queue's next tracks) have their own small budget,
// outside the cap: they are deleted once played or passed anyway.
export const LOOKAHEAD_MAX_ENTRIES = 5;
export const LOOKAHEAD_MAX_BYTES = 100 * 1024 * 1024;

/**
 * The lookahead copies to evict so that one more of `incoming` bytes fits
 * the lookahead budget, or null. Played copies go first (least recently
 * played first), then the oldest unplayed; `keep` (the upcoming tracks and
 * the one playing) is never evicted.
 */
export function planLookahead(lookaheads: Entry[], incoming: number, keep: ReadonlySet<number>): number[] | null {
  if (incoming > LOOKAHEAD_MAX_BYTES) return null;
  let used = usedBytes(lookaheads);
  let count = lookaheads.length;
  const order = lookaheads
    .filter((e) => !keep.has(e.trackId))
    .sort((a, b) => Number(b.lastPlayedAt > 0) - Number(a.lastPlayedAt > 0) || a.lastPlayedAt - b.lastPlayedAt || a.cachedAt - b.cachedAt);
  const evict: number[] = [];
  for (const e of order) {
    if (count + 1 <= LOOKAHEAD_MAX_ENTRIES && used + incoming <= LOOKAHEAD_MAX_BYTES) break;
    used -= e.bytes;
    count -= 1;
    evict.push(e.trackId);
  }
  return count + 1 <= LOOKAHEAD_MAX_ENTRIES && used + incoming <= LOOKAHEAD_MAX_BYTES ? evict : null;
}
