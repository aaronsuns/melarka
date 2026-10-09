import type { Track } from "./api/types";
import { getLocale } from "./i18n/i18n";

export function duration(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const ss = String(s).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${ss}` : `${m}:${ss}`;
}

const codecNames: Record<string, string> = { flac: "FLAC", alac: "ALAC", mp3: "MP3", aac: "AAC", opus: "OPUS", vorbis: "OGG", ape: "APE" };

export function qualityLabel(t: Track): string {
  if (t.codec.startsWith("pcm_")) return "WAV";
  if (t.codec.startsWith("wma")) return t.lossless ? "WMA Lossless" : withRate(t.bitrate, "WMA");
  const name = codecNames[t.codec] ?? t.codec.toUpperCase();
  return t.lossless ? name : withRate(t.bitrate, name);
}

function withRate(kbps: number, name: string) {
  return kbps > 0 ? `${kbps}k ${name}` : name;
}

function hash(s: string): number {
  let h = 2166136261;
  for (const ch of s) h = Math.imul(h ^ ch.codePointAt(0)!, 16777619);
  return h >>> 0;
}

export function coverStyle(seed: string): { background: string } {
  const h = hash(seed);
  const a = h % 360;
  const b = (a + 40 + ((h >> 9) % 80)) % 360;
  return { background: `linear-gradient(135deg, hsl(${a} 55% 38%), hsl(${b} 60% 22%))` };
}

export function initials(s: string): string {
  const first = [...s.trim()][0];
  return first ? first.toUpperCase() : "♪";
}

// YouTube/Google thumbnail CDNs. Anything else (a video or job could in
// principle carry an arbitrary string) falls back to the Cover placeholder
// instead of being handed to <img src> as-is.
const THUMBNAIL_HOSTS = new Set(["i.ytimg.com"]);
const THUMBNAIL_SUFFIXES = [".ggpht.com", ".googleusercontent.com"];

export function formatDateTime(epochSec: number): string {
  return new Date(epochSec * 1000).toLocaleString(getLocale());
}

// Whole days remaining until epochSec, never negative (a purge job may run
// a little later than exactly on time).
export function daysUntil(epochSec: number): number {
  return Math.max(0, Math.ceil((epochSec - Date.now() / 1000) / 86400));
}

export function isSafeThumbnailUrl(url: string): boolean {
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return false;
  }
  if (u.protocol !== "https:") return false;
  const host = u.hostname.toLowerCase();
  if (THUMBNAIL_HOSTS.has(host)) return true;
  return THUMBNAIL_SUFFIXES.some((suf) => host === suf.slice(1) || host.endsWith(suf));
}

/** Bytes for people: "6 MB", "1.5 GB" (binary units, like the cap choices). */
export function formatSize(bytes: number): string {
  const units = ["B", "KB", "MB", "GB"];
  let n = Math.max(0, bytes);
  let u = 0;
  while (n >= 1024 && u < units.length - 1) {
    n /= 1024;
    u += 1;
  }
  const shown = u === 3 ? Math.round(n * 10) / 10 : Math.round(n);
  return `${shown} ${units[u]}`;
}

/** A day for people: "2026/10/3" in zh, "10/3/2026" in en (the UI locale). */
export function formatDate(epochSec: number): string {
  return new Date(epochSec * 1000).toLocaleDateString(getLocale());
}
