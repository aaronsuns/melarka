// Registers the offline cache's service worker — or, when the server's
// kill switch is on (GET /info offline_cache:false), removes it: every
// registration unregistered, the offline caches deleted, the cache manager
// switched off. The server's /sw.js removes itself too then, so either half
// is enough on its own.

import { hasNative } from "../native/bridge";
import { disableOffline } from "./index";

export interface RegisterDeps {
  sw: Pick<ServiceWorkerContainer, "register" | "getRegistrations"> | undefined;
  caches: Pick<CacheStorage, "keys" | "delete"> | undefined;
  register: boolean; // production builds only: dev has no sw.js
  now(): number;
}

// A Home Screen app resumed from memory doesn't navigate, so it never checks
// for a new worker by itself: check when it comes back to the front.
const UPDATE_EVERY_MS = 60_000;

function realDeps(): RegisterDeps {
  return {
    sw: typeof navigator !== "undefined" && "serviceWorker" in navigator ? navigator.serviceWorker : undefined,
    caches: typeof caches !== "undefined" ? caches : undefined,
    register: import.meta.env.PROD,
    now: () => Date.now(),
  };
}

/**
 * Whether the offline cache should be on, from GET /info: the server's
 * switch — and never inside the Lark iPhone app, whose native engine keeps
 * its own cache (the worker would only download the same songs twice).
 */
export function offlineOnFrom(info: { offline_cache?: boolean }): boolean {
  return info.offline_cache !== false && !hasNative();
}

/** Applies the server's offline-cache switch; returns a cleanup (for tests). */
export async function applyOfflineSwitch(on: boolean, deps: RegisterDeps = realDeps()): Promise<() => void> {
  if (!on) {
    disableOffline();
    try {
      for (const r of (await deps.sw?.getRegistrations()) ?? []) await r.unregister();
    } catch {
      /* nothing registered */
    }
    try {
      const names = (await deps.caches?.keys()) ?? [];
      await Promise.all(names.filter((n) => n.startsWith("lark-offline")).map((n) => deps.caches!.delete(n)));
    } catch {
      /* storage gone */
    }
    return () => {};
  }
  if (!deps.sw || !deps.register) return () => {};
  let reg: ServiceWorkerRegistration;
  try {
    reg = await deps.sw.register("/sw.js", { scope: "/" });
  } catch {
    return () => {};
  }
  let last = -Infinity;
  const onVisible = () => {
    if (document.visibilityState !== "visible" || deps.now() - last < UPDATE_EVERY_MS) return;
    last = deps.now();
    void reg.update().catch(() => {});
  };
  document.addEventListener("visibilitychange", onVisible);
  return () => document.removeEventListener("visibilitychange", onVisible);
}
