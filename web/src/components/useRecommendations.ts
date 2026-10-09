import { useCallback, useEffect, useState } from "react";
import { api } from "../api/client";
import type { Recommendation, Recommendations } from "../api/types";
import { errorMessage } from "../i18n/errors";

export const RECS_POLL_MS = 3000;

/**
 * The signed-in user's recommendations. While the server says a refresh is
 * running it polls every 3 s — only while the page is visible (becoming
 * visible again polls at once) — and stops once the new list is in.
 */
export function useRecommendations() {
  const [data, setData] = useState<Recommendations | null>(null);
  const [loadError, setLoadError] = useState(false);
  const [refreshError, setRefreshError] = useState("");
  const [posting, setPosting] = useState(false);

  const load = useCallback(() => {
    return api
      .recommendations()
      .then((r) => {
        setData(r);
        setLoadError(false);
      })
      .catch(() => setLoadError(true));
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const refreshing = posting || (data?.refreshing ?? false);

  const polling = data?.refreshing ?? false;
  useEffect(() => {
    if (!polling) return;
    let cancelled = false;
    let fails = 0;
    let inflight = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const schedule = (ms: number) => {
      timer = setTimeout(tick, ms);
    };
    function tick() {
      timer = undefined;
      if (cancelled || inflight || document.hidden) return; // visibilitychange resumes
      inflight = true;
      api
        .recommendations()
        .then((r) => {
          fails = 0;
          if (!cancelled) setData(r); // refreshing false ends this effect
        })
        .catch(() => {
          fails++;
        })
        .finally(() => {
          inflight = false;
          // Failures back off (3 s, 6 s, … capped at a minute); never a tight loop.
          if (!cancelled && !timer) schedule(Math.min(RECS_POLL_MS * 2 ** fails, 60_000));
        });
    }
    schedule(RECS_POLL_MS);
    const onVisible = () => {
      if (!document.hidden && !timer && !inflight && !cancelled) schedule(0);
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [polling]);

  const refresh = useCallback(async () => {
    setRefreshError("");
    setPosting(true);
    try {
      await api.refreshRecommendations();
      await load();
    } catch (e) {
      setRefreshError(errorMessage(e, "recs.refreshFailed"));
    } finally {
      setPosting(false);
    }
  }, [load]);

  const setItems = useCallback((next: (cur: Recommendation[]) => Recommendation[]) => {
    setData((d) => (d ? { ...d, items: next(d.items) } : d));
  }, []);

  return { data, loadError, refreshing, refresh, refreshError, setItems };
}
