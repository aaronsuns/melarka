import { api } from "../api/client";
import type { Lyrics } from "../api/types";

// Found lyrics are fetched once per track for the whole session: the Now
// Playing view and the "warm the next track" prefetch share them. A miss
// (found:false) or a failed request is forgotten once it settles, so the next
// look asks again (the server's background prefetch may have found them
// meanwhile); concurrent asks still share the one in-flight request.
const cache = new Map<number, Promise<Lyrics>>();
// Told about every answer a request brings back — so a holder of a track's
// lyrics (the car-lyrics title) hears about a reload someone else asked for.
const listeners = new Set<(id: number, l: Lyrics) => void>();

export function onLyrics(fn: (id: number, l: Lyrics) => void): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

export function getLyrics(id: number): Promise<Lyrics> {
  let p = cache.get(id);
  if (!p) {
    p = api.lyrics(id);
    cache.set(id, p);
    const mine = p;
    const forget = () => {
      if (cache.get(id) === mine) cache.delete(id);
    };
    p.then((l) => {
      if (!l.found) forget();
      listeners.forEach((fn) => fn(id, l));
    }, forget);
  }
  return p;
}

export function prefetchLyrics(id: number): void {
  getLyrics(id).catch(() => {});
}

export function forgetLyrics(id: number): void {
  cache.delete(id);
}

// Replaces what is known about a track's lyrics (a changed offset, or the
// next version after "wrong lyrics") and tells every holder at once. Like an
// answer from the server: a miss is not kept.
export function setLyrics(id: number, l: Lyrics): void {
  if (l.found) cache.set(id, Promise.resolve(l));
  else cache.delete(id);
  listeners.forEach((fn) => fn(id, l));
}

// The last line whose time has come (lines are in time order), or -1 before
// the first one. offsetMs shifts every line: a line shows at t_ms + offsetMs.
export function activeLine(lines: { t_ms: number }[], positionS: number, offsetMs = 0): number {
  const ms = positionS * 1000 - offsetMs;
  let lo = 0;
  let hi = lines.length; // first index with t_ms > ms
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (lines[mid].t_ms <= ms) lo = mid + 1;
    else hi = mid;
  }
  return lo - 1;
}

// A line with nothing to read on a car display: empty, only music symbols or
// punctuation, or an "(Instrumental)"-style marker (also 间奏/間奏, 纯音乐/純音樂,
// 前奏, 尾奏 — escaped: they are matched, never shown).
const MARKER =
  /^[\s(\uff08[\u3010]*(instrumental|music|interlude|\u95f4\u594f|\u9593\u594f|\u7eaf\u97f3\u4e50|\u7d14\u97f3\u6a02|\u524d\u594f|\u5c3e\u594f)[\s)\uff09\]\u3011]*$/i;
export function isBlankLine(text: string): boolean {
  const s = text.trim();
  return !s || !/[\p{L}\p{N}]/u.test(s) || MARKER.test(s);
}

// A lyrics offset for display: "+1.5s", "−0.5s" (a real minus sign), "0.0s".
export function formatOffset(ms: number): string {
  const s = (Math.abs(ms) / 1000).toFixed(1);
  return ms > 0 ? `+${s}s` : ms < 0 ? `\u2212${s}s` : `${s}s`;
}

export const MAX_OFFSET_MS = 30000;
export const clampOffset = (ms: number) => Math.max(-MAX_OFFSET_MS, Math.min(MAX_OFFSET_MS, ms));
