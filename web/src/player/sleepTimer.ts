// The sleep timer's pure parts. The timer itself lives in SleepTimerProvider.

export type SleepChoice = { minutes: 15 | 30 | 45 | 60 } | { endOfTrack: true };
export const SLEEP_MINUTES = [15, 30, 45, 60] as const;
export const FADE_MS = 10_000,
  FADE_STEP_MS = 250;

/** Volume multipliers for the fade, 1 → 0 in FADE_MS (linear in amplitude). */
export function fadeSteps(ms = FADE_MS, step = FADE_STEP_MS): number[] {
  const n = Math.max(1, Math.round(ms / step));
  return Array.from({ length: n }, (_, i) => (n - 1 - i) / n);
}

/** "m:ss" for the chip. */
export function formatRemaining(ms: number): string {
  const s = Math.max(0, Math.ceil(ms / 1000));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}
