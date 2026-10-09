// The app's one OfflineCache, wired to the real browser, plus the React
// hook. Null where offline caching can't work (no Cache Storage or service
// worker — e.g. plain http on a LAN address, or jsdom).

import { useSyncExternalStore } from "react";
import { api, onFavoriteSet } from "../api/client";
import type { Track } from "../api/types";
import { connectOffline, isPlayerBuffering, onPlayerBuffering } from "./bridge";
import { OfflineCache, type ManagerDeps, type OfflineSnapshot } from "./manager";

export { CAP_CHOICES } from "./manager";
export type { OfflineSnapshot } from "./manager";

function supported(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof caches !== "undefined" &&
    typeof navigator !== "undefined" &&
    "serviceWorker" in navigator &&
    window.isSecureContext !== false
  );
}

async function listFavorites(): Promise<Track[]> {
  const out: Track[] = [];
  let cursor: string | undefined;
  do {
    const page = await api.tracks({ favorite: 1, sort: "favorited", limit: 500, cursor });
    out.push(...page.items);
    cursor = page.next_cursor || undefined;
  } while (cursor);
  return out;
}

function realDeps(): ManagerDeps {
  return {
    caches: caches as unknown as ManagerDeps["caches"],
    fetch: (url, init) => fetch(url, init),
    storage: localStorage,
    listFavorites,
    online: () => navigator.onLine !== false,
    busy: isPlayerBuffering,
    now: () => Date.now(),
    notifyWorker: () => {
      const msg = { type: "lark-offline-changed" };
      navigator.serviceWorker.controller?.postMessage(msg);
      void navigator.serviceWorker.getRegistration().then((r) => r?.active?.postMessage(msg), () => {});
    },
    controlled: () => !!navigator.serviceWorker.controller,
    persist: async () => (navigator.storage?.persist ? navigator.storage.persist() : false),
    estimate: async () => {
      if (!navigator.storage?.estimate) return null;
      const e = await navigator.storage.estimate();
      return { usage: e.usage ?? 0, quota: e.quota ?? 0 };
    },
  };
}

let instance: OfflineCache | null | undefined;
let killed = false; // the server's kill switch: off for the rest of this page's life
let stopSession: (() => void) | null = null;
const moduleListeners = new Set<() => void>();

export function getOffline(): OfflineCache | null {
  if (killed) return null;
  if (instance === undefined) instance = supported() ? new OfflineCache(realDeps()) : null;
  return instance;
}

/** Tests: use this cache (or none) instead of the browser's. */
export function setOfflineForTests(m: OfflineCache | null | undefined): void {
  instance = m;
  killed = false;
}

/** The kill switch: stops the cache and hides it everywhere. */
export function disableOffline(): void {
  stopSession?.();
  instance?.endSession();
  killed = true;
  moduleListeners.forEach((fn) => fn());
}

/** Signed in: connect the cache to the player, the network and favorites. Returns the cleanup. */
export function startOffline(): () => void {
  const m = getOffline();
  if (!m) return () => {};
  const offs = [
    connectOffline({
      played: (t, finished) => m.notePlayed(t, finished),
      playable: (id) => m.playableOffline(id),
      local: (id) => m.localKind(id),
      favorites: () => m.cachedFavorites(),
      lookahead: (tracks, keep) => m.lookahead(tracks, keep),
    }),
    onPlayerBuffering(() => m.bufferingChanged()),
    onFavoriteSet((id, on) => m.favoriteChanged(id, on)),
  ];
  const online = () => m.wentOnline();
  window.addEventListener("online", online);
  void m.start();
  let stopped = false;
  const stop = () => {
    if (stopped) return;
    stopped = true;
    offs.forEach((off) => off());
    window.removeEventListener("online", online);
    // Signed out or another user: this session's tap and queue don't carry over.
    m.endSession();
    if (stopSession === stop) stopSession = null;
  };
  stopSession = stop;
  return stop;
}

function subscribe(fn: () => void): () => void {
  moduleListeners.add(fn);
  const off = getOffline()?.subscribe(fn);
  return () => {
    moduleListeners.delete(fn);
    off?.();
  };
}
const snapshot = () => getOffline()?.snapshot() ?? null;

export function useOffline(): OfflineSnapshot | null {
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}
