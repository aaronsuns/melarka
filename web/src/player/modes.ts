// Shuffle and repeat: pure queue transforms and their per-device storage.
// With both off the player behaves exactly as without them (never stops:
// radio and favorites refill the queue at its end).

import { shuffled, type QueueState } from "./queue";

export type RepeatMode = "off" | "all" | "one";
export interface PlayModes {
  shuffle: boolean;
  repeat: RepeatMode;
}
// `original`: the upcoming track ids, in order, when shuffle was turned on (to restore on unshuffle).
export interface StoredModes extends PlayModes {
  original: number[] | null;
}
export const defaultModes: StoredModes = { shuffle: false, repeat: "off", original: null };

export const nextRepeat = (r: RepeatMode): RepeatMode => (r === "off" ? "all" : r === "all" ? "one" : "off");

// Saved per device and user, not with the server-side queue.
export function modesKey(userId?: number): string {
  return userId === undefined ? "lark.modes" : `lark.modes.${userId}`;
}

const isRepeat = (v: unknown): v is RepeatMode => v === "off" || v === "all" || v === "one";

export function loadModes(userId?: number): StoredModes {
  try {
    const raw = localStorage.getItem(modesKey(userId));
    if (!raw) return defaultModes;
    const v = JSON.parse(raw) as Partial<Record<keyof StoredModes, unknown>> | null;
    if (!v || typeof v !== "object") return defaultModes;
    const original = Array.isArray(v.original) && v.original.every((x) => typeof x === "number") ? (v.original as number[]) : null;
    return {
      shuffle: v.shuffle === true,
      repeat: isRepeat(v.repeat) ? v.repeat : "off",
      original,
    };
  } catch {
    return defaultModes; // storage unavailable or unreadable: modes off
  }
}

export function saveModes(userId: number | undefined, m: StoredModes): void {
  try {
    localStorage.setItem(modesKey(userId), JSON.stringify(m));
  } catch {
    // not remembered; this session still uses them
  }
}

/** Current stays put; everything after it is shuffled. */
export function shuffleUpcoming(q: QueueState, rand: () => number = Math.random): { queue: QueueState; original: number[] } {
  const head = q.tracks.slice(0, q.index + 1);
  const rest = q.tracks.slice(q.index + 1);
  return { queue: { ...q, tracks: [...head, ...shuffled(rest, rand)] }, original: rest.map((t) => t.id) };
}

/**
 * Restores the pre-shuffle order of the remaining tracks. Tracks added while shuffled keep where the user put
 * them: those ahead of the first pre-shuffle track (play next) stay in front; the rest (add to queue, refills)
 * follow, in their current order. Ids may repeat: matched as a multiset.
 */
export function unshuffleUpcoming(q: QueueState, original: number[]): QueueState {
  const head = q.tracks.slice(0, q.index + 1);
  const rest = q.tracks.slice(q.index + 1);
  const wanted = new Map<number, number>();
  for (const id of original) wanted.set(id, (wanted.get(id) ?? 0) + 1);
  const firstOriginal = rest.findIndex((t) => wanted.has(t.id));
  if (firstOriginal < 0) return q; // none of the shuffled tracks is left
  const front = rest.slice(0, firstOriginal);
  const tail = rest.slice(firstOriginal);
  // How many of each id are still upcoming (played and removed ones are gone).
  const left = new Map<number, number>();
  for (const t of tail) left.set(t.id, (left.get(t.id) ?? 0) + 1);
  const byId = new Map(tail.map((t) => [t.id, t]));
  const restored = [];
  for (const id of original) {
    const n = left.get(id) ?? 0;
    if (n === 0) continue;
    left.set(id, n - 1);
    restored.push(byId.get(id)!);
  }
  // Whatever wasn't matched was added while shuffled: it follows, in its order.
  const used = new Map<number, number>();
  for (const t of restored) used.set(t.id, (used.get(t.id) ?? 0) + 1);
  const added = tail.filter((t) => {
    const n = used.get(t.id) ?? 0;
    if (n === 0) return true;
    used.set(t.id, n - 1);
    return false;
  });
  return { ...q, tracks: [...head, ...front, ...restored, ...added] };
}

/** Repeat all at the end: the whole queue again from its first track; reshuffled when shuffle is on. */
export function newPass(q: QueueState, shuffle: boolean, rand: () => number = Math.random): QueueState {
  return { ...q, tracks: shuffle ? shuffled(q.tracks, rand) : q.tracks, index: 0 };
}
