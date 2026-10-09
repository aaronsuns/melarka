import type { Track } from "../api/types";
import { moveEntry, removeEntry } from "./dragReorder";

export interface QueueState {
  tracks: Track[];
  index: number;
  source: "list" | "radio" | "restored" | "shuffle" | "favorites";
  // How many entries right after the current one the user queued explicitly
  // (play next, add to queue): "add to queue" goes after them, ahead of the
  // rest of the list. Absent means none. Not saved with the queue.
  upNext?: number;
}

export type QueueAction =
  | { type: "playList"; tracks: Track[]; start: number; source?: "list" | "shuffle" | "favorites" }
  | { type: "enqueueNext"; track: Track }
  | { type: "append"; tracks: Track[]; source?: "radio" | "shuffle" | "favorites" }
  | { type: "next" }
  | { type: "prev" }
  | { type: "jump"; index: number }
  // Advance onto entry `to`, moving it to just after the current one (what it
  // skipped over follows it, to play later).
  | { type: "advance"; to: number; requeue?: Track }
  // Advance onto this track, inserted after the current one.
  | { type: "advanceInsert"; track: Track; requeue?: Track }
  | { type: "remove"; trackId: number }
  // `upNext`: the queued block stored on this device for this very queue (see storedUpNext).
  | { type: "restore"; tracks: Track[]; index: number; upNext?: number }
  // A new order (shuffle on/off, a repeat-all pass); unlike restore, keeps the source.
  // `upNext`: how many queued entries follow the current one in the new order (default: as before).
  | { type: "setOrder"; tracks: Track[]; index: number; upNext?: number }
  // To the end of the explicitly queued entries (after any play next / earlier adds); an upcoming copy
  // moves there; the current entry is untouched.
  | { type: "addToQueue"; track: Track }
  // Any entry, the current one too; `index` follows the current entry.
  | { type: "move"; from: number; to: number }
  // One entry; a no-op for the current one.
  | { type: "removeAt"; index: number }
  | { type: "updateTrack"; track: Track };

export const emptyQueue: QueueState = { tracks: [], index: 0, source: "list" };

const clamp = (i: number, len: number) => (len === 0 ? 0 : Math.min(Math.max(i, 0), len - 1));

export function queueReducer(s: QueueState, a: QueueAction): QueueState {
  const n = s.upNext ?? 0;
  switch (a.type) {
    case "playList":
      return { tracks: a.tracks, index: clamp(a.start, a.tracks.length), source: a.source ?? "list" };
    case "restore": {
      const index = clamp(a.index, a.tracks.length);
      return withUpNext({ tracks: a.tracks, index, source: "restored" }, Math.min(a.upNext ?? 0, a.tracks.length - index - 1));
    }
    case "setOrder": {
      const index = clamp(a.index, a.tracks.length);
      return withUpNext({ ...s, tracks: a.tracks, index }, Math.min(a.upNext ?? n, a.tracks.length - index - 1));
    }
    case "next":
    case "prev":
    case "jump": {
      const index = clamp(a.type === "jump" ? a.index : s.index + (a.type === "next" ? 1 : -1), s.tracks.length);
      return withUpNext({ ...s, index }, afterNav(n, s.index, index));
    }
    case "advance": {
      if (a.to <= s.index || a.to >= s.tracks.length) return s;
      const back = a.requeue ? [a.requeue] : [];
      const tracks = [...s.tracks.slice(0, s.index + 1), s.tracks[a.to], ...back, ...s.tracks.slice(s.index + 1, a.to), ...s.tracks.slice(a.to + 1)];
      // The requeued track is tried again before the queued ones, as one of them.
      return withUpNext({ ...s, tracks, index: s.index + 1 }, (a.to <= s.index + n ? n - 1 : n) + back.length);
    }
    case "advanceInsert": {
      if (s.tracks.length === 0) return { tracks: [a.track], index: 0, source: s.source };
      const back = a.requeue ? [a.requeue] : [];
      return withUpNext(
        { ...s, tracks: [...s.tracks.slice(0, s.index + 1), a.track, ...back, ...s.tracks.slice(s.index + 1)], index: s.index + 1 },
        n + back.length,
      );
    }
    case "enqueueNext": {
      if (s.tracks.length === 0) return { tracks: [a.track], index: 0, source: "list" };
      const cur = s.tracks[s.index];
      if (cur.id === a.track.id) return s;
      // Remove all occurrences of the track we're enqueueing (to move it)
      const rest = s.tracks.filter((t) => t.id !== a.track.id);
      // Adjust index: count how many removed entries were before current
      const removedBefore = s.tracks.slice(0, s.index).filter((t) => t.id === a.track.id).length;
      const newCurIndex = s.index - removedBefore;
      const removedQueued = s.tracks.slice(s.index + 1, s.index + 1 + n).filter((t) => t.id === a.track.id).length;
      // Insert after current position
      rest.splice(newCurIndex + 1, 0, a.track);
      return withUpNext({ ...s, tracks: rest, index: newCurIndex }, n - removedQueued + 1);
    }
    case "addToQueue": {
      if (s.tracks.length === 0) return { tracks: [a.track], index: 0, source: "list" };
      const head = s.tracks.slice(0, s.index + 1);
      const after = s.tracks.slice(s.index + 1);
      const queued = after.slice(0, n).filter((t) => t.id !== a.track.id);
      const others = after.slice(n).filter((t) => t.id !== a.track.id);
      return withUpNext({ ...s, tracks: [...head, ...queued, a.track, ...others] }, queued.length + 1);
    }
    case "move": {
      if (a.from === a.to) return s;
      const m = moveEntry(s.tracks, s.index, a.from, a.to);
      if (!m) return s;
      const flags = moveEntry(queuedFlags(s), s.index, a.from, a.to)!.list;
      // The moved entry is queued when dropped among queued ones (just before or after one).
      // Dropped right after the current entry with nothing queued, it is just reordered, not
      // queued: a later add to queue goes ahead of it, as it would have before the move.
      flags[a.to] = a.to > m.index && (flags[a.to + 1] === true || (a.to - 1 > m.index && flags[a.to - 1] === true));
      return withUpNext({ ...s, tracks: m.list, index: m.index }, countQueued(flags, m.index));
    }
    case "removeAt": {
      const r = removeEntry(s.tracks, s.index, a.index);
      if (!r) return s;
      return withUpNext({ ...s, tracks: r.list, index: r.index }, a.index > s.index && a.index <= s.index + n ? n - 1 : n);
    }
    case "append":
      return { ...s, tracks: [...s.tracks, ...appendable(s, a.tracks, a.source)], source: a.source ?? s.source };
    case "remove": {
      const i = s.tracks.findIndex((t) => t.id === a.trackId);
      if (i < 0) return s;
      const keep = (t: Track) => t.id !== a.trackId;
      const tracks = s.tracks.filter(keep);
      const flags = queuedFlags(s).filter((_, x) => keep(s.tracks[x]));
      // Count how many removed entries were before current index
      const removedBefore = s.tracks.slice(0, s.index).filter((t) => t.id === a.trackId).length;
      const index = clamp(s.index - removedBefore, tracks.length); // next entry slides into place if current was removed
      return withUpNext({ ...s, tracks, index }, countQueued(flags, index));
    }
    case "updateTrack": {
      if (!s.tracks.some((t) => t.id === a.track.id)) return s;
      return { ...s, tracks: s.tracks.map((t) => (t.id === a.track.id ? a.track : t)) };
    }
  }
}

function withUpNext(s: QueueState, n: number): QueueState {
  const { upNext: _, ...rest } = s;
  return n > 0 ? { ...rest, upNext: n } : rest;
}

// Moving forward plays through the queued entries; moving back puts what was
// skipped back in front of them (so later adds still go after them).
function afterNav(n: number, from: number, to: number): number {
  if (to >= from) return Math.max(0, n - (to - from));
  return n > 0 ? n + (from - to) : 0;
}

function queuedFlags(s: QueueState): boolean[] {
  const n = s.upNext ?? 0;
  return s.tracks.map((_, i) => i > s.index && i <= s.index + n);
}

function countQueued(flags: boolean[], index: number): number {
  let k = 0;
  while (flags[index + 1 + k]) k++;
  return k;
}

// The queued block (upNext) is kept per user on this device, with the queue it
// belongs to: the server's /queue doesn't carry it, so a reload restores it
// only for that very queue (same ids, same current index).
export function upNextKey(userId?: number): string {
  return userId === undefined ? "lark.upNext" : `lark.upNext.${userId}`;
}

export function saveUpNext(userId: number | undefined, q: QueueState): void {
  try {
    if (!q.upNext) localStorage.removeItem(upNextKey(userId));
    else localStorage.setItem(upNextKey(userId), JSON.stringify({ ids: q.tracks.map((t) => t.id), index: q.index, upNext: q.upNext }));
  } catch {
    // not remembered; this session still has it
  }
}

export function storedUpNext(userId: number | undefined, ids: number[], index: number): number {
  try {
    const v = JSON.parse(localStorage.getItem(upNextKey(userId)) ?? "null") as { ids?: unknown; index?: unknown; upNext?: unknown } | null;
    if (!v || !Array.isArray(v.ids) || v.index !== index || typeof v.upNext !== "number") return 0;
    if (v.ids.length !== ids.length || v.ids.some((id, i) => id !== ids[i])) return 0;
    return Math.max(0, Math.floor(v.upNext));
  } catch {
    return 0;
  }
}

// appendable is what an append of `tracks` actually adds: never a track twice
// within the batch, and never one the queue already holds — except for a
// favorites refill, which loops (a reshuffled pass once every favorite was
// heard), so it only skips what is still upcoming.
export function appendable(s: QueueState, tracks: Track[], source?: QueueState["source"]): Track[] {
  const have = new Set((source === "favorites" ? upcoming(s) : s.tracks).map((t) => t.id));
  return tracks.filter((t) => {
    if (have.has(t.id)) return false;
    have.add(t.id);
    return true;
  });
}

export function current(s: QueueState): Track | null {
  return s.tracks[s.index] ?? null;
}

export function upcoming(s: QueueState): Track[] {
  return s.tracks.slice(s.index + 1);
}

/** Two or fewer playable tracks left (ones that failed this session don't count). */
export function needsRefill(s: QueueState, failed: (id: number) => boolean = () => false): boolean {
  return upcoming(s).filter((t) => !failed(t.id)).length <= 2;
}

// shuffled returns a Fisher–Yates copy of xs.
export function shuffled<T>(xs: readonly T[], rand: () => number = Math.random): T[] {
  const out = [...xs];
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}
