import type { Track } from "../api/types";

export interface QueueState {
  tracks: Track[];
  index: number;
  source: "list" | "radio" | "restored" | "shuffle" | "favorites";
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
  | { type: "restore"; tracks: Track[]; index: number }
  | { type: "updateTrack"; track: Track };

export const emptyQueue: QueueState = { tracks: [], index: 0, source: "list" };

const clamp = (i: number, len: number) => (len === 0 ? 0 : Math.min(Math.max(i, 0), len - 1));

export function queueReducer(s: QueueState, a: QueueAction): QueueState {
  switch (a.type) {
    case "playList":
      return { tracks: a.tracks, index: clamp(a.start, a.tracks.length), source: a.source ?? "list" };
    case "restore":
      return { tracks: a.tracks, index: clamp(a.index, a.tracks.length), source: "restored" };
    case "next":
      return { ...s, index: clamp(s.index + 1, s.tracks.length) };
    case "prev":
      return { ...s, index: clamp(s.index - 1, s.tracks.length) };
    case "jump":
      return { ...s, index: clamp(a.index, s.tracks.length) };
    case "advance": {
      if (a.to <= s.index || a.to >= s.tracks.length) return s;
      const back = a.requeue ? [a.requeue] : [];
      const tracks = [...s.tracks.slice(0, s.index + 1), s.tracks[a.to], ...back, ...s.tracks.slice(s.index + 1, a.to), ...s.tracks.slice(a.to + 1)];
      return { ...s, tracks, index: s.index + 1 };
    }
    case "advanceInsert": {
      if (s.tracks.length === 0) return { tracks: [a.track], index: 0, source: s.source };
      const back = a.requeue ? [a.requeue] : [];
      return { ...s, tracks: [...s.tracks.slice(0, s.index + 1), a.track, ...back, ...s.tracks.slice(s.index + 1)], index: s.index + 1 };
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
      // Insert after current position
      rest.splice(newCurIndex + 1, 0, a.track);
      return { ...s, tracks: rest, index: newCurIndex };
    }
    case "append":
      return { ...s, tracks: [...s.tracks, ...appendable(s, a.tracks, a.source)], source: a.source ?? s.source };
    case "remove": {
      const i = s.tracks.findIndex((t) => t.id === a.trackId);
      if (i < 0) return s;
      const tracks = s.tracks.filter((t) => t.id !== a.trackId);
      // Count how many removed entries were before current index
      const removedBefore = s.tracks.slice(0, s.index).filter((t) => t.id === a.trackId).length;
      const index = s.index - removedBefore; // next entry slides into place if current was removed
      return { ...s, tracks, index: clamp(index, tracks.length) };
    }
    case "updateTrack": {
      if (!s.tracks.some((t) => t.id === a.track.id)) return s;
      return { ...s, tracks: s.tracks.map((t) => (t.id === a.track.id ? a.track : t)) };
    }
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
