import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { api, episodeStreamUrl } from "../api/client";
import type { Episode } from "../api/types";
import { t } from "../i18n/i18n";
import { claimSession, onSessionClaim, ownsSession } from "../player/sessionOwner";
import { buildQueue, clearStoredQueue, loadStoredQueue, saveStoredQueue, type EpisodeOrder } from "./episodeQueue";
import { hasNative } from "../native/bridge";
import { NativeEpisodesProvider } from "./NativeEpisodesProvider";

export const EPISODE_RATES = [1, 1.25, 1.5, 2] as const;
const SAVE_EVERY_MS = 15_000;
const RATE_KEY = "lark.episodeRate";
// Within this many seconds of the end, an episode starts over instead of resuming.
const NEAR_END_S = 30;
// ⏮ within this many seconds of the start goes to the previous episode; later, to the start.
const RESTART_S = 3;
const MAX_SKIPS = 3;
const MEDIA_ERR_NETWORK = 2;

export interface EpisodesPlayer {
  queue: Episode[];
  index: number;
  current: Episode | null;
  playing: boolean;
  active: boolean;
  rate: number;
  error: string | null;
  order: EpisodeOrder;
  includePlayed: boolean;
  /** Plays list[start] as it is, the list as the queue (an episode page's ▶ 听). */
  play(list: Episode[], start: number, opts?: { startAt?: number }): void;
  /**
   * Plays from a list the user tapped in: list[start] (start < 0: the first
   * in order) and the rest of the list as the queue — sorted or shuffled,
   * without played episodes unless includePlayed (the tapped one stays).
   */
  playList(list: Episode[], start: number, opts?: { order?: EpisodeOrder; includePlayed?: boolean }): void;
  /** Plays queue entry i. */
  jump(i: number): void;
  setOrder(o: EpisodeOrder): void;
  setIncludePlayed(on: boolean): void;
  toggle(): void;
  pause(): void;
  seek(seconds: number): void;
  skip(delta: number): void;
  next(): void;
  prev(): void;
  setRate(r: number): void;
  close(): void;
}

export interface EpisodesProgress {
  position: number;
  duration: number;
}

const Ctx = createContext<EpisodesPlayer | null>(null);
const ProgressCtx = createContext<EpisodesProgress>({ position: 0, duration: 0 });
// The native transport (NativeEpisodesProvider) fills the same two contexts.
export { Ctx as EpisodesCtx, ProgressCtx as EpisodesProgressCtx };

function storedRate(): number {
  try {
    const r = Number(localStorage.getItem(RATE_KEY));
    return (EPISODE_RATES as readonly number[]).includes(r) ? r : 1;
  } catch {
    return 1;
  }
}

/** Where an episode starts: its saved position, unless played, unstarted or nearly finished. */
export function resumeAt(ep: Episode): number {
  if (ep.played || ep.position_s <= 0) return 0;
  if (ep.duration_s > 0 && ep.position_s > ep.duration_s - NEAR_END_S) return 0;
  return ep.position_s;
}

/**
 * Plays channel episodes on their own <audio> element — a separate queue
 * that never holds music. Starting one claims the session (music pauses);
 * any other player claiming it pauses the episode. Progress goes to the
 * server every 15 s while playing, on pause, on page hide and at the end.
 */
export type EpisodesProviderProps = Parameters<typeof WebEpisodesProvider>[0];

// Inside the Lark iPhone app episodes play in the native engine (see
// PlayerProvider); everywhere else on this page's own <audio>.
export function EpisodesProvider(props: EpisodesProviderProps) {
  const [native] = useState(hasNative);
  return native ? <NativeEpisodesProvider {...props} /> : <WebEpisodesProvider {...props} />;
}

function WebEpisodesProvider({ children, audio: injected, userId = 0 }: { children: ReactNode; audio?: HTMLAudioElement; userId?: number }) {
  const [audio] = useState<HTMLAudioElement>(() => injected ?? new Audio());
  // A reload brings back the queue this device had (paused; nothing claimed).
  const [stored] = useState(() => {
    const q = loadStoredQueue(userId);
    if (!q) return null;
    const byId = new Map(q.source.map((e) => [e.video_id, e]));
    const list = q.ids.map((id) => byId.get(id)).filter((e): e is Episode => !!e);
    if (list.length === 0) return null;
    return { ...q, list, index: Math.min(Math.max(0, q.index), list.length - 1) };
  });
  // source: the list the queue was built from (order and "include played" rebuild from it).
  const [source, setSource] = useState<Episode[]>(stored?.source ?? []);
  const [queue, setQueue] = useState<Episode[]>(stored?.list ?? []);
  const [index, setIndex] = useState(stored?.index ?? 0);
  const [order, setOrderState] = useState<EpisodeOrder>(stored?.order ?? "newest");
  const [includePlayed, setIncludePlayedState] = useState(stored?.includePlayed ?? false);
  const [playing, setPlaying] = useState(false);
  // Restored inactive: the session starts as music's, and an episode UI
  // shown over it would hide music's player (继续收听 offers it instead).
  const [active, setActive] = useState(false);
  const [rate, setRateState] = useState(storedRate);
  const [error, setError] = useState<string | null>(null);
  const [position, setPosition] = useState(0);
  const [duration, setDuration] = useState(0);
  const queueRef = useRef(queue);
  queueRef.current = queue;
  const sourceRef = useRef(source);
  sourceRef.current = source;
  const indexRef = useRef(index);
  indexRef.current = index;
  const rateRef = useRef(rate);
  rateRef.current = rate;
  const pendingSeek = useRef(0);
  // The loaded episode has started sounding (an error before that means it can't play at all).
  const started = useRef(false);
  // Episodes skipped in a row because their file is gone (capped, see onError).
  const skips = useRef(0);
  const lastSave = useRef(0);
  // This hide was already saved: visibilitychange and pagehide usually both
  // fire for one hide. Any other save, or the page coming back, resets it.
  const hideSaved = useRef(false);
  const current = queue[index] ?? null;
  const currentRef = useRef(current);
  currentRef.current = current;

  // The queue holds snapshots: what is saved is written into them too, so
  // going back to an episode resumes where it was left.
  const patch = useCallback((id: string, p: Partial<Episode>) => {
    const fix = (l: Episode[]) => (l.some((e) => e.video_id === id) ? l.map((e) => (e.video_id === id ? { ...e, ...p } : e)) : l);
    queueRef.current = fix(queueRef.current);
    sourceRef.current = fix(sourceRef.current);
    setQueue(queueRef.current);
    setSource(sourceRef.current);
  }, []);

  const save = useCallback(
    (opts: { played?: boolean; keepalive?: boolean } = {}) => {
      const ep = currentRef.current;
      if (!ep || !audio.src) return;
      lastSave.current = Date.now();
      hideSaved.current = opts.keepalive === true;
      const body = opts.played ? { position_s: 0, played: true } : { position_s: Math.floor(audio.currentTime) };
      api.episodeProgress(ep.video_id, body, { keepalive: opts.keepalive }).catch(() => {});
      patch(ep.video_id, opts.played ? { position_s: 0, played: true } : { position_s: body.position_s });
    },
    [audio, patch],
  );

  const notAbort = (e: unknown) => (e as { name?: string } | null)?.name !== "AbortError";

// Nothing of the episode stays on the lock screen once it gives the session
// back (music re-renders its own song, if it has one, when it hears the claim).
function clearLockScreen() {
  const ms = typeof navigator !== "undefined" ? navigator.mediaSession : undefined;
  if (ms && ownsSession("episode")) ms.metadata = null;
}

// load starts list[i] synchronously (a tap's gesture still counts on iOS).
  const load = useCallback(
    (list: Episode[], i: number, startAt?: number) => {
      const ep = list[i];
      if (!ep) return;
      claimSession("episode");
      setActive(true);
      setError(null);
      setPosition(0);
      setDuration(ep.duration_s);
      pendingSeek.current = startAt ?? resumeAt(ep);
      started.current = false;
      audio.src = episodeStreamUrl(ep.video_id, "audio");
      audio.defaultPlaybackRate = rateRef.current;
      audio.playbackRate = rateRef.current;
      lastSave.current = Date.now();
      audio.play()?.catch((e: unknown) => {
        if (notAbort(e)) setError(t("episodes.cannotPlay"));
      });
    },
    [audio],
  );

  const start = useCallback(
    (src: Episode[], list: Episode[], i: number, o: EpisodeOrder, inc: boolean, startAt?: number) => {
      if (currentRef.current && audio.src) save();
      skips.current = 0;
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
    [audio, load, save],
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
  const rebuild = useCallback((o: EpisodeOrder, inc: boolean) => {
    setOrderState(o);
    setIncludePlayedState(inc);
    const cur = currentRef.current;
    if (!cur) return;
    const q = buildQueue(sourceRef.current, cur.video_id, o, inc);
    queueRef.current = q.list;
    indexRef.current = q.index;
    setQueue(q.list);
    setIndex(q.index);
  }, []);
  const orderRef = useRef(order);
  orderRef.current = order;
  const incRef = useRef(includePlayed);
  incRef.current = includePlayed;
  const setOrder = useCallback((o: EpisodeOrder) => rebuild(o, incRef.current), [rebuild]);
  const setIncludePlayed = useCallback((on: boolean) => rebuild(orderRef.current, on), [rebuild]);

  // goTo plays list entry i; a user's skip saves the position first, the
  // automatic advance after "ended" doesn't (that one was saved as played).
  const goTo = useCallback(
    (i: number, saveFirst = true) => {
      const list = queueRef.current;
      if (i < 0 || i >= list.length) return;
      if (saveFirst) save();
      setIndex(i);
      indexRef.current = i;
      load(list, i);
    },
    [load, save],
  );

  const next = useCallback(() => goTo(indexRef.current + 1), [goTo]);
  const jump = useCallback((i: number) => goTo(i), [goTo]);
  const prev = useCallback(() => {
    if (audio.currentTime > RESTART_S || indexRef.current === 0) audio.currentTime = 0;
    else goTo(indexRef.current - 1);
  }, [audio, goTo]);
  const pause = useCallback(() => audio.pause(), [audio]);
  const toggle = useCallback(() => {
    if (!currentRef.current) return;
    // A queue restored after a reload has nothing loaded yet.
    if (!audio.src) {
      load(queueRef.current, indexRef.current);
      return;
    }
    if (audio.paused) {
      claimSession("episode");
      setActive(true);
      audio.play()?.catch((e: unknown) => {
        if (notAbort(e)) setError(t("episodes.cannotPlay"));
      });
    } else audio.pause();
  }, [audio, load]);
  const seek = useCallback(
    (s: number) => {
      const d = Number.isFinite(audio.duration) ? audio.duration : Infinity;
      audio.currentTime = Math.max(0, Math.min(s, d));
      setPosition(audio.currentTime);
    },
    [audio],
  );
  const skip = useCallback((delta: number) => seek(audio.currentTime + delta), [audio, seek]);
  const setRate = useCallback(
    (r: number) => {
      setRateState(r);
      audio.defaultPlaybackRate = r;
      audio.playbackRate = r;
      try {
        localStorage.setItem(RATE_KEY, String(r));
      } catch {
        /* private mode */
      }
    },
    [audio],
  );
  const close = useCallback(() => {
    save();
    audio.pause();
    audio.removeAttribute("src");
    setQueue([]);
    setSource([]);
    setIndex(0);
    setActive(false);
    clearStoredQueue(userId);
    clearLockScreen();
    claimSession("music");
  }, [audio, save, userId]);

  // The queue as this device last had it, for a reload.
  useEffect(() => {
    if (queue.length === 0) return;
    saveStoredQueue(userId, { source, ids: queue.map((e) => e.video_id), index, order, includePlayed, active });
  }, [userId, source, queue, index, order, includePlayed, active]);

  // A restored queue's current episode: its position as the server has it now
  // (saved on hide, perhaps from another device).
  useEffect(() => {
    const id = stored?.list[stored.index]?.video_id;
    if (!id) return;
    let live = true;
    api.episode(id).then(
      (fresh) => live && patch(id, { position_s: fresh.position_s, played: fresh.played, audio: fresh.audio }),
      () => {},
    );
    return () => {
      live = false;
    };
  }, [stored, patch]);

  useEffect(() => {
    const onTime = () => {
      setPosition(audio.currentTime);
      if (!audio.paused && Date.now() - lastSave.current >= SAVE_EVERY_MS) save();
    };
    const onMeta = () => {
      if (pendingSeek.current > 0) audio.currentTime = pendingSeek.current;
      pendingSeek.current = 0;
      audio.playbackRate = rateRef.current;
      if (Number.isFinite(audio.duration)) setDuration(audio.duration);
    };
    const onPlay = () => setPlaying(true);
    const onPlaying = () => {
      started.current = true;
      skips.current = 0;
    };
    const onPause = () => {
      setPlaying(false);
      if (!audio.ended) save();
    };
    const onEnded = () => {
      setPlaying(false);
      save({ played: true }); // also marks it played in the queue
      if (indexRef.current + 1 < queueRef.current.length) goTo(indexRef.current + 1, false);
    };
    // An episode whose file is gone (410 expired, 404) is skipped before it
    // ever plays — at most MAX_SKIPS in a row. A network error (offline, a
    // blip) or a failure mid-way keeps the episode, with its error: the
    // file is asked for its status first, and a failed ask skips nothing.
    const onError = () => {
      const src = audio.src;
      if (!src) return;
      setError(t("episodes.cannotPlay"));
      if (started.current || audio.error?.code === MEDIA_ERR_NETWORK) return;
      if (skips.current >= MAX_SKIPS || indexRef.current + 1 >= queueRef.current.length) return;
      fetch(src, { credentials: "same-origin", headers: { Range: "bytes=0-0" } }).then(
        (r) => {
          r.body?.cancel().catch(() => {});
          if (r.status !== 404 && r.status !== 410) return;
          if (audio.src !== src || started.current || skips.current >= MAX_SKIPS) return; // moved on meanwhile
          if (indexRef.current + 1 >= queueRef.current.length) return;
          skips.current++;
          goTo(indexRef.current + 1, false);
        },
        () => {},
      );
    };
    // keepalive: the request outlives a page being closed or frozen.
    const saveOnHide = () => {
      if (!hideSaved.current && !audio.paused) save({ keepalive: true });
    };
    const onVisibility = () => {
      if (document.visibilityState === "hidden") saveOnHide();
      else hideSaved.current = false;
    };
    const hs: [string, () => void][] = [
      ["timeupdate", onTime], ["loadedmetadata", onMeta], ["play", onPlay], ["playing", onPlaying], ["pause", onPause], ["ended", onEnded], ["error", onError],
    ];
    hs.forEach(([n, h]) => audio.addEventListener(n, h));
    document.addEventListener("visibilitychange", onVisibility);
    window.addEventListener("pagehide", saveOnHide);
    const off = onSessionClaim((o) => {
      if (o === "episode") return;
      if (!audio.paused) audio.pause();
      setActive(false);
    });
    return () => {
      hs.forEach(([n, h]) => audio.removeEventListener(n, h));
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("pagehide", saveOnHide);
      off();
    };
  }, [audio, save, goTo]);

  // Unmount (logout, a user switch): save where the episode was, stop and
  // release the element, and give the session back — the owner is module
  // state, and the next user's music player must start as its owner.
  // Declared after the effect above, so its listeners are already gone.
  useEffect(
    () => () => {
      save();
      audio.pause();
      audio.removeAttribute("src");
      audio.load();
      if (ownsSession("episode")) {
        clearLockScreen();
        claimSession("music");
      }
    },
    [audio, save],
  );

  // Lock screen while an episode owns it: its title and channel, ⏮/⏭ within episodes.
  const curId = current?.video_id;
  useEffect(() => {
    const ms = navigator.mediaSession;
    const ep = currentRef.current;
    if (!ms || !ep || !active) return;
    const meta = () => {
      const ep = currentRef.current;
      if (typeof MediaMetadata === "undefined" || !ep) return;
      ms.metadata = new MediaMetadata({
        title: ep.title,
        artist: ep.channel_title,
        album: t("channels.title"),
        artwork: [{ src: new URL(ep.thumbnail, location.href).href, sizes: "480x360", type: "image/jpeg" }],
      });
    };
    const set = (a: MediaSessionAction, h: MediaSessionActionHandler | null) => {
      try {
        ms.setActionHandler(a, h);
      } catch {
        /* unsupported action */
      }
    };
    const actions: [MediaSessionAction, MediaSessionActionHandler | null][] = [
      // A car sends "play" when it connects: resume a paused episode, never pause a playing one.
      ["play", () => {
        if (audio.paused) toggle();
      }],
      ["pause", () => pause()],
      ["nexttrack", () => next()],
      ["previoustrack", () => prev()],
      ["seekto", (d) => d.seekTime !== undefined && seek(d.seekTime)],
      ["seekbackward", null],
      ["seekforward", null],
    ];
    // Only while an episode owns the session (a restored queue doesn't yet).
    const register = () => {
      if (!ownsSession("episode")) return;
      meta();
      actions.forEach(([a, h]) => set(a, h));
    };
    register();
    audio.addEventListener("play", register);
    return () => audio.removeEventListener("play", register);
  }, [curId, active, audio, toggle, pause, next, prev, seek]);

  // Lock-screen scrubber (at most once a second).
  useEffect(() => {
    const ms = navigator.mediaSession;
    if (!ms || typeof ms.setPositionState !== "function") return;
    let last = 0;
    const apply = () => {
      if (!ownsSession("episode") || !Number.isFinite(audio.duration) || Date.now() - last < 1000) return;
      last = Date.now();
      try {
        ms.setPositionState({ duration: audio.duration, position: audio.currentTime, playbackRate: audio.playbackRate });
      } catch {
        /* invalid values mid-load */
      }
    };
    audio.addEventListener("timeupdate", apply);
    return () => audio.removeEventListener("timeupdate", apply);
  }, [audio]);

  const value = useMemo<EpisodesPlayer>(
    () => ({ queue, index, current, playing, active, rate, error, order, includePlayed, play, playList, jump, setOrder, setIncludePlayed, toggle, pause, seek, skip, next, prev, setRate, close }),
    [queue, index, current, playing, active, rate, error, order, includePlayed, play, playList, jump, setOrder, setIncludePlayed, toggle, pause, seek, skip, next, prev, setRate, close],
  );
  const progress = useMemo(() => ({ position, duration }), [position, duration]);
  return (
    <Ctx.Provider value={value}>
      <ProgressCtx.Provider value={progress}>{children}</ProgressCtx.Provider>
    </Ctx.Provider>
  );
}

export function useEpisodes(): EpisodesPlayer {
  const v = useContext(Ctx);
  if (!v) throw new Error("useEpisodes outside EpisodesProvider");
  return v;
}

export function useEpisodesProgress(): EpisodesProgress {
  return useContext(ProgressCtx);
}
