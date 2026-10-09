import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../api/client";
import type { DownloadJob } from "../api/types";

export const ACTIVE_POLL_MS = 2000;
export const IDLE_POLL_MS = 15_000;
const ERROR_BASE_MS = 4000;
const ERROR_CAP_MS = 60_000;
const MAX_FAILURES = 5;

export function isActiveJob(j: DownloadJob): boolean {
  return j.status === "queued" || j.status === "downloading";
}

/**
 * Polls GET /downloads: every 2 s while anything is queued/downloading, then
 * every `idleMs` (or not at all when `idleMs` is null). The delay for the
 * next tick is decided from the response just received, not from state.
 *
 * - Nothing is fetched while the page is hidden (screen locked: background
 *   audio must not be disturbed); becoming visible again polls at once.
 * - Failures back off 4 s, 8 s, ... capped at 60 s, and after 5 in a row the
 *   loop stops until the page becomes visible again or `resume()` is called.
 *
 * Returns `resume`, to restart a stopped loop on a user action.
 */
export function useJobPoll(opts: {
  enabled: boolean;
  idleMs: number | null;
  onJobs: (jobs: DownloadJob[]) => void;
  onError?: (e: unknown) => void;
}): { resume: () => void } {
  const cb = useRef(opts);
  cb.current = opts;
  const { enabled, idleMs } = opts;
  const [nonce, setNonce] = useState(0);
  const resume = useCallback(() => setNonce((n) => n + 1), []);
  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    let inflight = false;
    let fails = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const clear = () => {
      if (timer) clearTimeout(timer);
      timer = undefined;
    };
    const schedule = (ms: number) => {
      clear();
      timer = setTimeout(tick, ms);
    };
    function tick() {
      timer = undefined;
      if (cancelled || inflight || document.hidden) return; // visibilitychange resumes
      inflight = true;
      api
        .downloads()
        .then((js) => {
          inflight = false;
          if (cancelled) return;
          fails = 0;
          cb.current.onJobs(js);
          const delay = js.some(isActiveJob) ? ACTIVE_POLL_MS : idleMs;
          if (delay != null) schedule(delay);
        })
        .catch((e) => {
          inflight = false;
          if (cancelled) return;
          cb.current.onError?.(e);
          fails++;
          if (fails < MAX_FAILURES) schedule(Math.min(ERROR_BASE_MS * 2 ** (fails - 1), ERROR_CAP_MS));
        });
    }
    const onVisibility = () => {
      clear();
      if (document.hidden) return;
      fails = 0;
      tick();
    };
    document.addEventListener("visibilitychange", onVisibility);
    tick();
    return () => {
      cancelled = true;
      clear();
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [enabled, idleMs, nonce]);
  return { resume };
}
