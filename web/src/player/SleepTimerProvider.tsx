import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useEpisodes, useEpisodesProgress } from "../channels/EpisodesProvider";
import { hasNative, nativePost, onNative, type ItemKind, type NativeState } from "../native/bridge";
import { usePlayer, usePlayerProgress } from "./PlayerProvider";
import { FADE_MS, FADE_STEP_MS, fadeSteps, type SleepChoice } from "./sleepTimer";

export interface SleepTimer {
  choice: SleepChoice | null;
  /**
   * Until the pause, for a minutes timer; in the app also for end of track
   * (native's figure). Null otherwise (end of track: see useTrackRemainingMs).
   */
  remainingMs: number | null;
  start(c: SleepChoice): void;
  cancel(): void;
  /** False in an iPhone app without the native timer (a JS timer would stop when locked). */
  available: boolean;
}

const Ctx = createContext<SleepTimer | null>(null);

// A start or cancel native hasn't confirmed yet: states that disagree with it
// are taken as sent before native handled it, for at most this long.
const PENDING_MS = 2000;
// A page (re)opened while native's timer runs can't know which kind it is:
// time left that matches what is left of the item is "end of this track".
const SAME_MS = 1500;

/** What native's latest state said about its timer. */
interface NativeSleep {
  ms: number | null;
  at: number;
}

// The time left a state carries; undefined: an app without the native timer.
function stateSleep(m: NativeState): number | null | undefined {
  const v = m.sleepRemainingMs;
  if (v === undefined) return undefined;
  return typeof v === "number" && Number.isFinite(v) ? Math.max(0, v) : null;
}

/**
 * The sleep timer of a browser tab: a JavaScript timer, which a browser may
 * delay while the page is in the background. At the deadline the volume of
 * both players (music and episodes; only one sounds at a time) fades to 0
 * over FADE_MS, then both pause and the volume is restored for the next play.
 * "End of this track" fades over the track's last FADE_MS and stops there.
 *
 * In the iPhone app the native engine runs the timer, fade included, so it
 * keeps time with the phone locked: this only posts `sleepTimer` and shows
 * the `sleepRemainingMs` of native's states (counting a deadline down
 * between them). An app without it sends no such key, and the timer stays
 * hidden there.
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
  // The app: native's timer, once a state has carried it (null until then: not available).
  const [nativeSleep, setNativeSleep] = useState<NativeSleep | null>(null);
  const pending = useRef<{ armed: boolean; at: number } | null>(null);
  // Native's latest state of each kind (a reloaded page reads the playing kind's).
  const lastStates = useRef<Partial<Record<ItemKind, NativeState>>>({});

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

  const cancel = useCallback(() => {
    if (!native) return reset();
    pending.current = { armed: false, at: Date.now() };
    setChoice(null);
    setNativeSleep((s) => s && { ms: null, at: Date.now() });
    nativePost({ type: "sleepTimer", cancel: true });
  }, [native, reset]);
  // A natural end consumed the players' stopAfterCurrent (see EndOfTrackWatcher).
  const finished = useCallback(() => {
    clearTimers();
    setFadeAll(1);
    setChoice(null);
    setDeadline(null);
  }, [clearTimers, setFadeAll]);

  const start = useCallback(
    (c: SleepChoice) => {
      if (native) {
        // Native replaces any timer it runs; until its state says, the full time is shown.
        pending.current = { armed: true, at: Date.now() };
        setChoice(c);
        setNow(Date.now());
        if ("minutes" in c) {
          setNativeSleep({ ms: c.minutes * 60_000, at: Date.now() });
          nativePost({ type: "sleepTimer", minutes: c.minutes });
        } else {
          setNativeSleep({ ms: null, at: Date.now() });
          nativePost({ type: "sleepTimer", endOfTrack: true });
        }
        return;
      }
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
    [native, reset, setFadeAll],
  );

  useEffect(() => clearTimers, [clearTimers]);

  // The app: native's states say whether its timer runs and what is left.
  useEffect(() => {
    if (!native) return;
    return onNative((m) => {
      if (m.type !== "state") return;
      const ms = stateSleep(m);
      if (ms === undefined) return;
      lastStates.current[m.kind] = m;
      const p = pending.current;
      // Already on its way when the last tap was posted: the tap stands.
      if (p && (ms !== null) !== p.armed && Date.now() - p.at < PENDING_MS) return;
      pending.current = null;
      const at = Date.now();
      setNativeSleep((s) => (s && s.ms === null && ms === null ? s : { ms, at }));
      setNow(at);
      if (ms === null) setChoice(null);
    });
  }, [native]);
  // A timer this page didn't set (it was reloaded): its kind, from the active kind's own state. The
  // inactive kind's state, which may come first, says nothing about it.
  const activeKind: ItemKind = eps.active && eps.current ? "episode" : "track";
  let inferred: "endOfTrack" | "minutes" | null = null;
  if (native && !choice && nativeSleep?.ms != null) {
    const s = lastStates.current[activeKind];
    const left = s ? stateSleep(s) : null;
    inferred = s && left != null && s.durationMs > 0 && Math.abs(s.durationMs - s.positionMs - left) <= SAME_MS ? "endOfTrack" : "minutes";
  }
  const shown = useMemo<SleepChoice | null>(
    () => choice ?? (inferred === "endOfTrack" ? { endOfTrack: true } : inferred === "minutes" ? { minutes: 15 } : null),
    [choice, inferred],
  );
  // A deadline counts down between native's states (none come while paused).
  const nativeDeadline = native && shown !== null && "minutes" in shown && nativeSleep?.ms != null;
  useEffect(() => {
    if (!nativeDeadline) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [nativeDeadline]);

  let remainingMs = deadline === null ? null : Math.max(0, deadline - now);
  if (native) {
    const ms = shown ? (nativeSleep?.ms ?? null) : null;
    remainingMs = ms === null ? null : nativeDeadline ? Math.max(0, ms - (now - nativeSleep!.at)) : ms;
  }
  const available = !native || nativeSleep !== null;
  const value = useMemo<SleepTimer>(
    () => ({ choice: shown, remainingMs, start, cancel, available }),
    [shown, remainingMs, start, cancel, available],
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
