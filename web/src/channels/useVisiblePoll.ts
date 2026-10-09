import { useEffect, useRef } from "react";

/**
 * Calls load now, then every `everyMs` while the page is visible: a hidden
 * page (a locked phone, a background tab) asks nothing, and coming back
 * asks at once. `key` restarts it (a new load right away); `enabled` false
 * asks nothing at all.
 */
export function useVisiblePoll(load: () => void, everyMs: number, key: unknown = null, enabled = true): void {
  const loadRef = useRef(load);
  loadRef.current = load;
  useEffect(() => {
    if (!enabled) return;
    const visible = () => document.visibilityState !== "hidden";
    const run = () => {
      if (visible()) loadRef.current();
    };
    run();
    const id = setInterval(run, everyMs);
    const onVis = () => run();
    document.addEventListener("visibilitychange", onVis);
    return () => {
      clearInterval(id);
      document.removeEventListener("visibilitychange", onVis);
    };
  }, [everyMs, key, enabled]);
}
