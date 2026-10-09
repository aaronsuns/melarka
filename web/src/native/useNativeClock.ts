import { useCallback, useEffect, useMemo, useState } from "react";

// Where native's player is, between its `state` events: positionMs plus the
// time since the event (× rate) while playing, ticking every 250 ms only
// while playing and the page is visible.
const TICK_MS = 250;

export interface ClockBase {
  positionMs: number;
  durationMs: number;
  playing: boolean;
  rate: number;
  at: number; // Date.now() when it was received
}

export const stoppedClock: ClockBase = { positionMs: 0, durationMs: 0, playing: false, rate: 1, at: 0 };

export function clockPosition(b: ClockBase, now: number): number {
  let ms = b.positionMs + (b.playing ? Math.max(0, now - b.at) * (b.rate > 0 ? b.rate : 1) : 0);
  if (b.durationMs > 0) ms = Math.min(ms, b.durationMs);
  return Math.max(0, ms) / 1000;
}

const visible = () => typeof document === "undefined" || document.visibilityState !== "hidden";

/** The progress ({position, duration} in seconds) of a clock base. */
export function useNativeClock(base: ClockBase): { position: number; duration: number } {
  const compute = useCallback(() => clockPosition(base, Date.now()), [base]);
  const [position, setPosition] = useState(compute);
  const [shown, setShown] = useState(visible);
  useEffect(() => setPosition(compute()), [compute]);
  useEffect(() => {
    const on = () => setShown(visible());
    document.addEventListener("visibilitychange", on);
    return () => document.removeEventListener("visibilitychange", on);
  }, []);
  useEffect(() => {
    if (!base.playing || !shown) return;
    setPosition(compute());
    const t = setInterval(() => setPosition(compute()), TICK_MS);
    return () => clearInterval(t);
  }, [base.playing, shown, compute]);
  const duration = base.durationMs / 1000;
  return useMemo(() => ({ position, duration }), [position, duration]);
}
