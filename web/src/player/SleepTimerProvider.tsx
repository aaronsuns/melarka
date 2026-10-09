import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useEpisodes, useEpisodesProgress } from "../channels/EpisodesProvider";
import { hasNative } from "../native/bridge";
import { usePlayer, usePlayerProgress } from "./PlayerProvider";
import { FADE_MS, FADE_STEP_MS, fadeSteps, type SleepChoice } from "./sleepTimer";

export interface SleepTimer {
  choice: SleepChoice | null;
  /** Until the pause, for a minutes timer; null otherwise (end of track: see useTrackRemainingMs). */
  remainingMs: number | null;
  start(c: SleepChoice): void;
  cancel(): void;
  /** False in the iPhone app: its engine gets its own timer (the web one would stop when locked). */
  available: boolean;
}

const Ctx = createContext<SleepTimer | null>(null);

/**
 * The sleep timer of a browser tab: a JavaScript timer, which a browser may
 * delay while the page is in the background. At the deadline the volume of
 * both players (music and episodes; only one sounds at a time) fades to 0
 * over FADE_MS, then both pause and the volume is restored for the next play.
 * "End of this track" fades over the track's last FADE_MS and stops there.
 */
export function SleepTimerProvider({ children }: { children: ReactNode }) {
  const [native] = useState(hasNative);
  const player = usePlayer();
  const eps = useEpisodes();
  const playerRef = useRef(player);
  playerRef.current = player;
  const epsRef = useRef(eps);
  epsRef.current = eps;

  const [choice, setChoice] = useState<SleepChoice | null>(null);
  const [deadline, setDeadline] = useState<number | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const timers = useRef<ReturnType<typeof setTimeout>[]>([]);

  const setFadeAll = useCallback((f: number) => {
    playerRef.current.setFade(f);
    epsRef.current.setFade(f);
  }, []);
  const stopAfterAll = useCallback((on: boolean) => {
    playerRef.current.stopAfterCurrent(on);
    epsRef.current.stopAfterCurrent(on);
  }, []);
  const clearTimers = useCallback(() => {
    timers.current.forEach((t) => {
      clearTimeout(t);
      clearInterval(t);
    });
    timers.current = [];
  }, []);
  // Back to no timer, at full volume.
  const reset = useCallback(() => {
    clearTimers();
    setFadeAll(1);
    stopAfterAll(false);
    setChoice(null);
    setDeadline(null);
  }, [clearTimers, setFadeAll, stopAfterAll]);

  const cancel = useCallback(() => reset(), [reset]);
  // A natural end consumed the players' stopAfterCurrent (see EndOfTrackWatcher).
  const finished = useCallback(() => {
    clearTimers();
    setFadeAll(1);
    setChoice(null);
    setDeadline(null);
  }, [clearTimers, setFadeAll]);

  const start = useCallback(
    (c: SleepChoice) => {
      reset(); // one timer: a new choice replaces the old one (and undoes its fade)
      setChoice(c);
      if (!("minutes" in c)) return; // the EndOfTrackWatcher takes it from here
      const ms = c.minutes * 60_000;
      const t0 = Date.now();
      setDeadline(t0 + ms);
      setNow(t0);
      timers.current.push(setInterval(() => setNow(Date.now()), 1000));
      timers.current.push(
        setTimeout(() => {
          const steps = fadeSteps();
          let i = 0;
          timers.current.push(
            setInterval(() => {
              setFadeAll(steps[i++]);
              if (i < steps.length) return;
              playerRef.current.pause();
              epsRef.current.pause();
              reset();
            }, FADE_STEP_MS),
          );
        }, ms - FADE_MS),
      );
    },
    [reset, setFadeAll],
  );

  useEffect(() => clearTimers, [clearTimers]);

  const remainingMs = deadline === null ? null : Math.max(0, deadline - now);
  const value = useMemo<SleepTimer>(
    () => ({ choice, remainingMs, start, cancel, available: !native }),
    [choice, remainingMs, start, cancel, native],
  );
  return (
    <Ctx.Provider value={value}>
      {children}
      {choice && "endOfTrack" in choice && !native && <EndOfTrackWatcher onDone={finished} />}
    </Ctx.Provider>
  );
}

export function useSleepTimer(): SleepTimer {
  const v = useContext(Ctx);
  if (!v) throw new Error("useSleepTimer outside SleepTimerProvider");
  return v;
}

/** Which player is sounding (or would be): an active episode, else music. */
function useActiveKind(): "episode" | "music" {
  const eps = useEpisodes();
  return eps.active && eps.current ? "episode" : "music";
}

/** What is left of the current track or episode, in ms (null: nothing loaded). */
export function useTrackRemainingMs(): number | null {
  const kind = useActiveKind();
  const music = usePlayerProgress();
  const episode = useEpisodesProgress();
  const p = kind === "episode" ? episode : music;
  if (!(p.duration > 0)) return null;
  return Math.max(0, (p.duration - p.position) * 1000);
}

// Stopped this close to the end: the track (or episode) has finished.
const END_MS = 2 * FADE_STEP_MS;

/**
 * "End of this track". Alone reads the progress (so the ticks don't re-render
 * the provider's subtree): it fades over the last FADE_MS, and once the
 * player has stopped at the end it clears the timer. Moving to another track
 * or episode before that carries the timer over to it.
 */
function EndOfTrackWatcher({ onDone }: { onDone: () => void }) {
  const player = usePlayer();
  const eps = useEpisodes();
  const kind = useActiveKind();
  const remaining = useTrackRemainingMs();
  const key = kind === "episode" ? `e:${eps.index}:${eps.current?.video_id}` : `m:${player.queue.index}:${player.current?.id}`;
  const playing = kind === "episode" ? eps.playing : player.playing;
  const armed = useRef<string | null>(null);
  // What was left the last time the armed item reported progress.
  const lastRemaining = useRef(Infinity);
  // It has played since it was armed (arming a paused track at its very end isn't its end).
  const heard = useRef(false);

  useEffect(() => {
    const changed = armed.current !== key;
    if (!changed && remaining !== null) lastRemaining.current = remaining;
    // At its end the element is paused, then "ended" moves music on to the
    // next track (loaded paused) or keeps the finished episode. The two can
    // reach React in separate renders, so the player's own stopAfterCurrent
    // is left for that "ended" to consume, never cleared here.
    if (armed.current !== null && heard.current && !playing && lastRemaining.current <= END_MS) return onDone();
    if (changed) {
      armed.current = key;
      lastRemaining.current = remaining ?? Infinity;
      heard.current = false;
      player.setFade(1);
      eps.setFade(1);
      player.stopAfterCurrent(kind === "music");
      eps.stopAfterCurrent(kind === "episode");
    }
    if (playing) heard.current = true;
    (kind === "episode" ? eps : player).setFade(remaining === null ? 1 : Math.min(1, remaining / FADE_MS));
  });
  return null;
}
