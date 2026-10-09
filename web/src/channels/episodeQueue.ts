import type { Episode } from "../api/types";

// The episode queue (§18.1): built from the list the user tapped in, in one
// of three orders, normally without the episodes already played.
export type EpisodeOrder = "newest" | "oldest" | "shuffle";
export const EPISODE_ORDERS: readonly EpisodeOrder[] = ["newest", "oldest", "shuffle"];

export interface BuiltQueue {
  list: Episode[];
  index: number;
}

/**
 * The queue for source: played episodes dropped unless includePlayed (the
 * tapped one, startId, always stays), sorted by publish time or shuffled
 * with the tapped one first. index is the tapped one, else the first.
 */
export function buildQueue(source: Episode[], startId: string | null, order: EpisodeOrder, includePlayed: boolean, rand: () => number = Math.random): BuiltQueue {
  const seen = new Set<string>();
  const kept = source.filter((e) => {
    if (seen.has(e.video_id)) return false;
    seen.add(e.video_id);
    return includePlayed || !e.played || e.video_id === startId;
  });
  let list: Episode[];
  if (order === "shuffle") {
    const start = kept.find((e) => e.video_id === startId);
    const rest = kept.filter((e) => e !== start);
    for (let i = rest.length - 1; i > 0; i--) {
      const j = Math.floor(rand() * (i + 1));
      [rest[i], rest[j]] = [rest[j], rest[i]];
    }
    list = start ? [start, ...rest] : rest;
  } else {
    const dir = order === "oldest" ? 1 : -1;
    list = [...kept].sort((a, b) => (a.published_at - b.published_at) * dir || a.video_id.localeCompare(b.video_id));
  }
  const at = startId === null ? -1 : list.findIndex((e) => e.video_id === startId);
  return { list, index: Math.max(0, at) };
}

// Stored per device and user, so a reload brings the queue back (paused)
// and another account on the same phone never sees it.
const key = (userId: number) => `lark.episodeQueue.${userId}`;

export interface StoredQueue {
  source: Episode[];
  ids: string[];
  index: number;
  order: EpisodeOrder;
  includePlayed: boolean;
  active: boolean;
}

export function saveStoredQueue(userId: number, q: StoredQueue): void {
  try {
    const source = q.source.map(({ description: _d, ...rest }) => rest);
    localStorage.setItem(key(userId), JSON.stringify({ v: 1, ...q, source }));
  } catch {
    /* private mode or full: not remembered */
  }
}

export function loadStoredQueue(userId: number): StoredQueue | null {
  try {
    const raw = localStorage.getItem(key(userId));
    if (!raw) return null;
    const q = JSON.parse(raw) as Partial<StoredQueue> & { v?: number };
    if (q.v !== 1 || !Array.isArray(q.source) || !Array.isArray(q.ids) || typeof q.index !== "number") return null;
    if (!EPISODE_ORDERS.includes(q.order as EpisodeOrder)) return null;
    return { source: q.source, ids: q.ids, index: q.index, order: q.order as EpisodeOrder, includePlayed: q.includePlayed === true, active: q.active === true };
  } catch {
    return null;
  }
}

export function clearStoredQueue(userId: number): void {
  try {
    localStorage.removeItem(key(userId));
  } catch {
    /* nothing stored */
  }
}
