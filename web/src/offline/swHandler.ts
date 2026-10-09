// The service worker's fetch logic, kept pure so it can be unit tested
// (sw.ts only wires it to the real FetchEvent/caches). It answers ONLY for
// tracks that are in the offline cache — audio, lyrics, covers — and returns
// null for everything else, which the worker then leaves to the browser
// untouched (no respondWith): the app shell, JS/CSS and API JSON are never
// cached here, so an app update is never served stale.

import { coverKey, lyricsKey, OFFLINE_QUALITY, routeOf, streamKey } from "./keys";
import { rangeResponse } from "./range";

export interface SwCache {
  match(key: string): Promise<Response | undefined>;
  put(key: string, res: Response): Promise<void>;
}

export interface SwDeps {
  origin: string;
  cache(): Promise<SwCache>;
  fetch(req: Request): Promise<Response>;
  online(): boolean;
  // Track ids with cached audio; null while not yet known (worker just started).
  cachedIds(): Set<number> | null;
  // Loads that list (the worker woke for this request): a stream request
  // waits for it rather than going to the network for a cached track — at a
  // locked iPhone's track change there is no time for a slow network.
  loadIds?(): Promise<Set<number>>;
}

const LYRICS_TIMEOUT_MS = 8000;
// Another quality than the cached one: how long the network gets before the
// cached copy plays instead (in a tunnel iOS still says it's online).
const STREAM_TIMEOUT_MS = 4000;

/** A copy with only the content type: never cookies or anything auth-related. */
export async function sanitized(res: Response): Promise<Response> {
  const headers: Record<string, string> = {};
  const ct = res.headers.get("Content-Type");
  if (ct) headers["Content-Type"] = ct;
  return new Response(await res.blob(), { status: 200, headers });
}

async function serveAudio(hit: Response, req: Request): Promise<Response> {
  const body = await hit.blob();
  return rangeResponse(body, hit.headers.get("Content-Type") || body.type || "audio/mp4", req.headers.get("Range"));
}

function withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
  return new Promise((resolve, reject) => {
    const t = setTimeout(() => reject(new Error("timeout")), ms);
    p.then(
      (v) => (clearTimeout(t), resolve(v)),
      (e) => (clearTimeout(t), reject(e)),
    );
  });
}

export function handleFetch(req: Request, deps: SwDeps): Promise<Response> | null {
  if (req.method !== "GET") return null;
  const url = new URL(req.url);
  if (url.origin !== deps.origin) return null;
  if (url.searchParams.get("lark_sw") === "bypass") return null;
  const route = routeOf(url);
  if (!route) return null;
  const ids = deps.cachedIds();
  if (ids === null && deps.online() && route.kind === "stream" && deps.loadIds) {
    const load = deps.loadIds;
    return (async () => {
      const known = await load().catch(() => new Set<number>());
      return (handleFetch(req, { ...deps, cachedIds: () => known, loadIds: undefined }) ?? deps.fetch(req)) as Promise<Response>;
    })();
  }
  // Online, only a track known to be cached is answered here; everything
  // else — including every request right after the worker woke, before the
  // list is loaded — goes to the browser untouched. Offline, the list may
  // be stale or not loaded yet, so the cache is asked.
  if ((ids === null || !ids.has(route.id)) && deps.online()) return null;

  switch (route.kind) {
    case "stream":
      return (async () => {
        const cache = await deps.cache();
        const hit = await cache.match(streamKey(route.id));
        if (!hit) return deps.fetch(req);
        if (route.quality === OFFLINE_QUALITY || !deps.online()) return serveAudio(hit, req);
        // A different quality was chosen: honor it while there is a network.
        try {
          return await withTimeout(deps.fetch(req), STREAM_TIMEOUT_MS);
        } catch {
          return serveAudio(hit, req);
        }
      })();
    case "lyrics":
      return (async () => {
        const cache = await deps.cache();
        const key = lyricsKey(route.id);
        if (!deps.online()) return (await cache.match(key)) ?? deps.fetch(req);
        try {
          const res = await withTimeout(deps.fetch(req), LYRICS_TIMEOUT_MS);
          if (res.ok && (ids?.has(route.id) ?? false)) await cache.put(key, await sanitized(res.clone()));
          return res;
        } catch (e) {
          const hit = await cache.match(key);
          if (hit) return hit;
          throw e;
        }
      })();
    case "cover":
      return (async () => {
        const cache = await deps.cache();
        const key = coverKey(route.id, route.size);
        const hit = await cache.match(key);
        if (hit) return hit;
        const res = await deps.fetch(req);
        if (res.ok && (ids?.has(route.id) ?? false)) await cache.put(key, await sanitized(res.clone()));
        return res;
      })();
  }
}
