import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Episode } from "../api/types";
import { nativePost, onNative, type NativeState } from "../native/bridge";
import { episodeItem, fromItem } from "../native/items";
import { clockPosition, stoppedClock, useNativeClock, type ClockBase } from "../native/useNativeClock";
import { moveEntry, removeEntry } from "../player/dragReorder";
import { claimSession, onSessionClaim, ownsSession } from "../player/sessionOwner";
import { buildQueue, clearStoredQueue, loadStoredQueue, saveStoredQueue, type EpisodeOrder } from "./episodeQueue";
import { EPISODE_RATES, EpisodesCtx, EpisodesProgressCtx, resumeAt, type EpisodesPlayer, type EpisodesProviderProps } from "./EpisodesProvider";

const RATE_KEY = "lark.episodeRate";

function storedRate(): number {
  try {
    const r = Number(localStorage.getItem(RATE_KEY));
    return (EPISODE_RATES as readonly number[]).includes(r) ? r : 1;
  } catch {
    return 1;
  }
}

const ms = (s: number) => Math.max(0, Math.round(s * 1000));
// The native engine runs its own sleep timer; the web one is hidden in the app.
const noop = () => {};

/**
 * The 频道 episode player inside the Lark iPhone app: native plays the
 * episodes and saves their progress; this builds the queues exactly as the
 * web player does (buildQueue, resumeAt) and mirrors native's `queue` and
 * `state` events. No <audio>, no progress PUTs, no Media Session. The queue
 * is still kept in localStorage, so a reload shows it at once; native's
 * `queue` event is the truth and replaces it.
 */
export function NativeEpisodesProvider({ children, userId = 0 }: EpisodesProviderProps) {
  const [stored] = useState(() => {
    const q = loadStoredQueue(userId);
    if (!q) return null;
    const byId = new Map(q.source.map((e) => [e.video_id, e]));
    const list = q.ids.map((id) => byId.get(id)).filter((e): e is Episode => !!e);
    if (list.length === 0) return null;
    return { ...q, list, index: Math.min(Math.max(0, q.index), list.length - 1) };
  });
  const [source, setSource] = useState<Episode[]>(stored?.source ?? []);
  const [queue, setQueue] = useState<Episode[]>(stored?.list ?? []);
  const [index, setIndex] = useState(stored?.index ?? 0);
  const [order, setOrderState] = useState<EpisodeOrder>(stored?.order ?? "newest");
  const [includePlayed, setIncludePlayedState] = useState(stored?.includePlayed ?? false);
  const [state, setState] = useState<NativeState | null>(null);
  const [clock, setClock] = useState<ClockBase>(stoppedClock);
  const [rate, setRateState] = useState(storedRate);
  // Shown over music's player: an episode was started (here or natively)
  // and nothing else has taken the session since. Restored: not yet.
  const [shown, setShown] = useState(false);

  const queueRef = useRef(queue);
  queueRef.current = queue;
  const sourceRef = useRef(source);
  sourceRef.current = source;
  const indexRef = useRef(index);
  indexRef.current = index;
  const orderRef = useRef(order);
  orderRef.current = order;
  const incRef = useRef(includePlayed);
  incRef.current = includePlayed;
  const playingRef = useRef(false);
  const current = queue[index] ?? null;

  const sendQueue = useCallback((list: Episode[], i: number, start: { positionMs?: number; play: boolean }) => {
    nativePost({
      type: "setQueue", kind: "episode", items: list.map(episodeItem), index: i,
      ...(start.positionMs === undefined ? {} : { positionMs: start.positionMs }), play: start.play, source: "list",
    });
  }, []);

  // Playing list[i] from `startAt` (else its resume point): native switches to it.
  const load = useCallback(
    (list: Episode[], i: number, startAt?: number) => {
      const ep = list[i];
      if (!ep) return;
      claimSession("episode");
      setShown(true);
      const positionMs = ms(startAt ?? resumeAt(ep));
      setClock({ positionMs, durationMs: ms(ep.duration_s), playing: false, rate, at: Date.now() });
      sendQueue(list, i, { positionMs, play: true });
    },
    [sendQueue, rate],
  );

  const start = useCallback(
    (src: Episode[], list: Episode[], i: number, o: EpisodeOrder, inc: boolean, startAt?: number) => {
      setSource(src);
      setQueue(list);
      setIndex(i);
      setOrderState(o);
      setIncludePlayedState(inc);
      sourceRef.current = src;
      queueRef.current = list;
      indexRef.current = i;
      load(list, i, startAt);
    },
    [load],
  );
  const play = useCallback(
    (list: Episode[], i: number, opts: { startAt?: number } = {}) => start(list, list, i, "newest", false, opts.startAt),
    [start],
  );
  const playList = useCallback(
    (list: Episode[], i: number, opts: { order?: EpisodeOrder; includePlayed?: boolean } = {}) => {
      const o = opts.order ?? "newest";
      const inc = opts.includePlayed ?? false;
      const q = buildQueue(list, list[i]?.video_id ?? null, o, inc);
      if (q.list.length > 0) start(list, q.list, q.index, o, inc);
    },
    [start],
  );
  // Reorders (or re-filters) the queue around the current episode, which keeps playing.
  const rebuild = useCallback(
    (o: EpisodeOrder, inc: boolean) => {
      setOrderState(o);
      setIncludePlayedState(inc);
      const cur = queueRef.current[indexRef.current];
      if (!cur) return;
      const q = buildQueue(sourceRef.current, cur.video_id, o, inc);
      queueRef.current = q.list;
      indexRef.current = q.index;
      setQueue(q.list);
      setIndex(q.index);
      sendQueue(q.list, q.index, { play: playingRef.current });
    },
    [sendQueue],
  );
  const setOrder = useCallback((o: EpisodeOrder) => rebuild(o, incRef.current), [rebuild]);
  // Queue sheet edits: sent without a position, so native keeps the current episode playing.
  const reorder = useCallback(
    (list: Episode[], i: number) => {
      queueRef.current = list;
      indexRef.current = i;
      setQueue(list);
      setIndex(i);
      sendQueue(list, i, { play: playingRef.current });
    },
    [sendQueue],
  );
  const move = useCallback(
    (from: number, to: number) => {
      const m = from === to ? null : moveEntry(queueRef.current, indexRef.current, from, to);
      if (m) reorder(m.list, m.index);
    },
    [reorder],
  );
  const removeAt = useCallback(
    (i: number) => {
      const r = removeEntry(queueRef.current, indexRef.current, i);
      if (r) reorder(r.list, r.index);
    },
    [reorder],
  );
  const setIncludePlayed = useCallback((on: boolean) => rebuild(orderRef.current, on), [rebuild]);

  const jump = useCallback(
    (i: number) => {
      const list = queueRef.current;
      if (i < 0 || i >= list.length) return;
      setIndex(i);
      indexRef.current = i;
      load(list, i);
    },
    [load],
  );
  const next = useCallback(() => nativePost({ type: "next", kind: "episode" }), []);
  const prev = useCallback(() => nativePost({ type: "prev", kind: "episode" }), []);
  const pause = useCallback(() => nativePost({ type: "pause", kind: "episode" }), []);
  const toggle = useCallback(() => {
    if (!queueRef.current[indexRef.current]) return;
    if (playingRef.current) return pause();
    // The queue shown is native's: a stored one native lacks was cleared by native's answer to hello.
    claimSession("episode");
    setShown(true);
    nativePost({ type: "play", kind: "episode" });
  }, [pause]);
  const seek = useCallback((s: number) => {
    const at = ms(s);
    nativePost({ type: "seek", kind: "episode", ms: at });
    setClock((c) => ({ ...c, positionMs: c.durationMs > 0 ? Math.min(at, c.durationMs) : at, at: Date.now() }));
  }, []);
  const skip = useCallback((delta: number) => {
    nativePost({ type: "skip", kind: "episode", ms: Math.round(delta * 1000) });
  }, []);
  const setRate = useCallback((r: number) => {
    setRateState(r);
    try {
      localStorage.setItem(RATE_KEY, String(r));
    } catch {
      /* private mode */
    }
    nativePost({ type: "setRate", rate: r });
  }, []);
  const close = useCallback(() => {
    nativePost({ type: "stop", kind: "episode" });
    setQueue([]);
    setSource([]);
    setIndex(0);
    queueRef.current = [];
    sourceRef.current = [];
    setShown(false);
    clearStoredQueue(userId);
    claimSession("music");
  }, [userId]);

  // Unmount (logout, a user switch): give the session back, as the web
  // player does — the owner is module state, and the next user's music
  // player must start as its owner.
  useEffect(
    () => () => {
      if (ownsSession("episode")) claimSession("music");
    },
    [],
  );

  // The rate this device chose (native starts every launch at 1×).
  useEffect(() => {
    nativePost({ type: "setRate", rate: storedRate() });
  }, []);

  // Native → web.
  useEffect(
    () =>
      onNative((m) => {
        if (m.type === "queue" && m.kind === "episode") {
          const list = (Array.isArray(m.items) ? m.items : []).map((i) => fromItem({ ...i, kind: "episode" }) as Episode);
          if (list.length === 0) {
            queueRef.current = [];
            sourceRef.current = [];
            setQueue([]);
            setSource([]);
            setIndex(0);
            setShown(false);
            clearStoredQueue(userId);
            return;
          }
          const i = Math.min(Math.max(0, m.index), list.length - 1);
          // Native's copies carry the saved progress: the source list (what
          // order and "include played" rebuild from) takes them too.
          const fresh = new Map(list.map((e) => [e.video_id, e]));
          const src = sourceRef.current;
          const inSource = list.every((e) => src.some((s) => s.video_id === e.video_id));
          const nextSource = inSource ? src.map((s) => fresh.get(s.video_id) ?? s) : list;
          queueRef.current = list;
          sourceRef.current = nextSource;
          indexRef.current = i;
          setQueue(list);
          setSource(nextSource);
          setIndex(i);
          return;
        }
        if (m.type !== "state") return;
        if (m.kind === "track") {
          if (!m.playing) return;
          // Music took over natively; native plays one kind at a time, so the episode is not playing,
          // whatever its last state said (a kind switch may report only the new kind).
          setShown(false);
          if (playingRef.current) {
            playingRef.current = false;
            setState((s) => s && { ...s, playing: false, buffering: false });
            setClock((c) => ({ ...c, positionMs: Math.round(clockPosition(c, Date.now()) * 1000), playing: false, at: Date.now() }));
          }
          return;
        }
        const list = queueRef.current;
        if (m.index !== indexRef.current && m.index >= 0 && m.index < list.length && list[m.index].video_id === m.itemId) {
          indexRef.current = m.index;
          setIndex(m.index);
        }
        const was = playingRef.current;
        playingRef.current = m.playing;
        setState(m);
        if (Number.isFinite(m.rate) && m.rate > 0) setRateState(m.rate);
        setClock({ positionMs: m.positionMs, durationMs: m.durationMs, playing: m.playing, rate: m.rate, at: Date.now() });
        if (m.playing && !was) {
          setShown(true);
          claimSession("episode", { force: true });
        }
      }),
    [userId],
  );
  // Anything else taking the session (music, a preview, a video): the
  // episode's UI steps aside, as the web player's does.
  useEffect(
    () =>
      onSessionClaim((o) => {
        if (o !== "episode") setShown(false);
      }),
    [],
  );

  // The queue as this device last had it, for a reload.
  useEffect(() => {
    if (queue.length === 0) return;
    saveStoredQueue(userId, { source, ids: queue.map((e) => e.video_id), index, order, includePlayed, active: shown });
  }, [userId, source, queue, index, order, includePlayed, shown]);

  const playing = state?.playing ?? false;
  const error = state?.error ?? null;
  const active = shown && current !== null;
  const clockBase = useMemo(
    () => (clock.durationMs > 0 || !current ? clock : { ...clock, durationMs: ms(current.duration_s) }),
    [clock, current],
  );
  const progress = useNativeClock(clockBase);

  const value = useMemo<EpisodesPlayer>(
    () => ({ queue, index, current, playing, active, rate, error, order, includePlayed, play, playList, jump, move, removeAt, setOrder, setIncludePlayed, toggle, pause, seek, skip, next, prev, setRate, close, setFade: noop, stopAfterCurrent: noop }),
    [queue, index, current, playing, active, rate, error, order, includePlayed, play, playList, jump, move, removeAt, setOrder, setIncludePlayed, toggle, pause, seek, skip, next, prev, setRate, close],
  );
  return (
    <EpisodesCtx.Provider value={value}>
      <EpisodesProgressCtx.Provider value={progress}>{children}</EpisodesProgressCtx.Provider>
    </EpisodesCtx.Provider>
  );
}
