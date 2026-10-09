// Shared by the page (cache manager) and the service worker: where things
// live in Cache Storage. Keys are same-origin paths; responses stored under
// them carry only content headers.

export const CACHE_NAME = "lark-offline-v1";
// Offline copies are always the "high" tier (AAC 256k): good on a phone, a
// fraction of lossless.
export const OFFLINE_QUALITY = "high";
export const INDEX_KEY = "/__lark_offline/index.json";
export const COVER_SIZES = [300, 1000] as const;
// The cache manager's own requests carry this so the worker never answers
// them from the cache (a download, or a check that a copy is still current).
export const BYPASS = "lark_sw=bypass";
export const bypass = (key: string) => key + (key.includes("?") ? "&" : "?") + BYPASS;

export const streamKey = (id: number) => `/api/v1/tracks/${id}/stream?quality=${OFFLINE_QUALITY}`;
export const lyricsKey = (id: number) => `/api/v1/tracks/${id}/lyrics`;
export const coverKey = (id: number, size: number) => `/api/v1/tracks/${id}/cover?size=${size}`;

export type Route =
  | { kind: "stream"; id: number; quality: string }
  | { kind: "lyrics"; id: number }
  | { kind: "cover"; id: number; size: number };

/** Which offline-cacheable resource a same-origin URL is, if any. */
export function routeOf(url: URL): Route | null {
  const m = /^\/api\/v1\/tracks\/(\d+)\/(stream|lyrics|cover)$/.exec(url.pathname);
  if (!m) return null;
  const id = Number(m[1]);
  if (m[2] === "stream") return { kind: "stream", id, quality: url.searchParams.get("quality") ?? "" };
  if (m[2] === "lyrics") return url.search === "" ? { kind: "lyrics", id } : null;
  const size = Number(url.searchParams.get("size"));
  return (COVER_SIZES as readonly number[]).includes(size) ? { kind: "cover", id, size } : null;
}

/** The track ids whose audio is in the cache, from the cache's keys. */
export function idsFromKeys(keys: string[]): Set<number> {
  const ids = new Set<number>();
  for (const k of keys) {
    const m = /\/api\/v1\/tracks\/(\d+)\/stream\?quality=high$/.exec(k);
    if (m) ids.add(Number(m[1]));
  }
  return ids;
}
