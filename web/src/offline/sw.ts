// Lark's service worker: offline copies of cached tracks only (see
// swHandler.ts for what it answers and what it leaves alone). Built as its
// own self-contained file, dist/sw.js, by vite.sw.config.ts.

import { CACHE_NAME, idsFromKeys } from "./keys";
import { handleFetch, type SwCache } from "./swHandler";

// Minimal service-worker types: the project compiles against the DOM lib,
// which doesn't have them.
interface ExtendableEvent extends Event {
  waitUntil(p: Promise<unknown>): void;
}
interface FetchEvent extends ExtendableEvent {
  request: Request;
  respondWith(r: Promise<Response>): void;
}
interface SwGlobal {
  location: Location;
  navigator: Navigator;
  caches: CacheStorage;
  skipWaiting(): Promise<void>;
  clients: { claim(): Promise<void> };
  addEventListener(type: "install" | "activate", fn: (e: ExtendableEvent) => void): void;
  addEventListener(type: "fetch", fn: (e: FetchEvent) => void): void;
  addEventListener(type: "message", fn: (e: MessageEvent) => void): void;
}

const sw = self as unknown as SwGlobal;
let ids: Set<number> | null = null;

function cache(): Promise<SwCache> {
  return sw.caches.open(CACHE_NAME) as unknown as Promise<SwCache>;
}

async function refreshIds(): Promise<void> {
  const c = await sw.caches.open(CACHE_NAME);
  ids = idsFromKeys((await c.keys()).map((r) => r.url));
}
// The first load after the worker woke, shared by every request waiting on it.
let firstLoad: Promise<void> | null = null;
function loadIds(): Promise<Set<number>> {
  firstLoad ??= refreshIds().catch(() => {
    firstLoad = null; // try again on the next request
  });
  return firstLoad.then(() => ids ?? new Set<number>());
}

sw.addEventListener("install", () => void sw.skipWaiting());
// Caches of an older layout (a bumped CACHE_NAME) are never read again.
async function dropOldCaches(): Promise<void> {
  const names = await sw.caches.keys();
  await Promise.all(names.filter((n) => n.startsWith("lark-offline-") && n !== CACHE_NAME).map((n) => sw.caches.delete(n)));
}

sw.addEventListener("activate", (e) => e.waitUntil(Promise.all([sw.clients.claim(), refreshIds(), dropOldCaches()])));
// The page tells the worker whenever it cached or evicted something.
sw.addEventListener("message", (e) => {
  if ((e.data as { type?: string } | null)?.type === "lark-offline-changed") void refreshIds().catch(() => {});
});
void loadIds().catch(() => {});

sw.addEventListener("fetch", (e) => {
  const res = handleFetch(e.request, {
    origin: sw.location.origin,
    cache,
    fetch: (r) => fetch(r),
    online: () => sw.navigator.onLine !== false,
    cachedIds: () => ids,
    loadIds,
  });
  if (res) e.respondWith(res);
});
