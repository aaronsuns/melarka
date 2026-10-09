import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation } from "react-router";
import { api } from "../api/client";
import { useVisiblePoll } from "./useVisiblePoll";

const EVERY_MS = 5 * 60_000;

// Pages where what you do changes the count (play, mark played, follow).
const isChannelsRoute = (p: string) => p === "/channels" || p.startsWith("/channels/") || p.startsWith("/episodes/");

/**
 * The 频道 tab's badge: downloaded, unplayed episodes. Read every 5 minutes
 * while the page is visible (and at once on coming back), and again on each
 * navigation within the channel pages — not on every music route. Off
 * (the server has Channels switched off) it reads nothing and stays 0.
 */
export function useUnplayed(enabled = true): number {
  const [n, setN] = useState(0);
  const { pathname } = useLocation();
  const live = useRef(true);
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
    };
  }, []);
  const load = useCallback(() => {
    api.latestEpisodes({ limit: 1 }).then((r) => live.current && setN(r.unplayed), () => {});
  }, []);
  useVisiblePoll(load, EVERY_MS, isChannelsRoute(pathname) ? pathname : null, enabled);
  return enabled ? n : 0;
}
