import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, onFavoriteSet } from "../api/client";
import type { Quality, Track } from "../api/types";
import { t } from "../i18n/i18n";
import { nativePost, onNative, type NativeState } from "../native/bridge";
import { fromItem, trackItem } from "../native/items";
import { clockPosition, stoppedClock, useNativeClock, type ClockBase } from "../native/useNativeClock";
import { PlayerCtx, PlayerProgressCtx, type Player, type PlayerProgress, type PlayerProviderProps } from "./PlayerProvider";
import { nextRepeat, type PlayModes, type RepeatMode } from "./modes";
import { current, emptyQueue, queueReducer, saveUpNext, storedUpNext, type QueueAction, type QueueState } from "./queue";
import { claimSession, onSessionClaim } from "./sessionOwner";

// How long a passing notice stays up (as the web player).
const NOTICE_MS = 4000;
// Native answers hello with its queue at once; if it never does, the idle
// mini player's shortcut still appears.
const READY_AFTER_MS = 1500;
// A setModes native hasn't confirmed yet: states that disagree with it are
// taken as sent before native handled it, for at most this long.
const MODES_PENDING_MS = 2000;
// flushEvents gives up waiting for native's `flushed` after this long.
const FLUSH_TIMEOUT_MS = 3000;
// An app without shuffle/repeat (its state carries no `shuffle`): the
// buttons stay hidden and the queue keeps its never-stop behaviour.
const NO_MODES: PlayModes = { shuffle: false, repeat: "off" };
const noop = () => {};
const isRepeat = (v: unknown): v is RepeatMode => v === "off" || v === "all" || v === "one";
// The modes native's music state reports; null from an app without them.
const stateModes = (s: NativeState | null): PlayModes | null =>
  s && typeof s.shuffle === "boolean" ? { shuffle: s.shuffle, repeat: isRepeat(s.repeat) ? s.repeat : "off" } : null;

function storedQuality(): Quality {
  const q = localStorage.getItem("lark.quality");
  return q === "lossless" || q === "saver" ? q : "high";
}

// The web player's shuffle choice (PlayerProvider's favoritesOrFallback).
async function favoritesOrFallback(): Promise<{ tracks: Track[]; source: "favorites" | "shuffle"; fellBack: boolean }> {
  const r = await api.randomFavorites(50, []);
  if (r.source === "favorites" && r.tracks.length > 0) return { tracks: r.tracks, source: "favorites", fellBack: false };
  const tracks = r.tracks.length > 0 ? r.tracks : await api.randomTracks(50, []);
  return { tracks, source: "shuffle", fellBack: true };
}

let flushSeq = 0;

// Native's queue carries no record of which tracks the user queued (play
// next, add to queue). When it is the mirrored queue again — the echo of an
// edit, an advance, a refill appended — that record is carried over (moved
// along with the index); any other queue starts with none.
// Reordered around it (shuffle off keeps the play-next tracks in front), the
// block stays while the same entries still directly follow the same current one.
function withQueued(prev: QueueState, next: QueueState): QueueState {
  const n = prev.upNext;
  if (!n) return next;
  const same = prev.tracks.length <= next.tracks.length && prev.tracks.every((t, i) => t.id === next.tracks[i].id);
  if (same) return queueReducer({ ...prev, tracks: next.tracks, source: next.source }, { type: "jump", index: next.index });
  const block = prev.tracks.slice(prev.index, prev.index + 1 + n);
  const now = next.tracks.slice(next.index, next.index + 1 + n);
  return now.length === block.length && block.every((t, i) => t.id === now[i].id) ? { ...next, upNext: n } : next;
}

/**
 * The music Player inside the Lark iPhone app: native plays, keeps the queue,
 * posts play events and syncs /queue; this mirrors its `queue` and `state`
 * events into the same contexts the web player fills, and turns the Player
 * actions into bridge messages. No <audio>, no EventBuffer, no Media Session,
 * no PUT /queue, no offline cache.
 */
export function NativePlayerProvider({ children, onOpen, carLyrics = true, userId }: PlayerProviderProps) {
  const [queue, setQueueState] = useState<QueueState>(emptyQueue);
  const queueRef = useRef(queue);
  const [state, setState] = useState<NativeState | null>(null);
  const [clock, setClock] = useState<ClockBase>(stoppedClock);
  const [quality, setQualityState] = useState<Quality>(storedQuality);
  const [ready, setReady] = useState(false);
  const [notice, setNoticeState] = useState<string | null>(null);
  const noticeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const flushWaiters = useRef(new Map<string, () => void>());
  const playingRef = useRef(false);
  const firstQueue = useRef(true);
  // The modes as last sent or reported (a second tap before native answers steps on from the first).
  const modesRef = useRef<PlayModes>(NO_MODES);
  const pendingModes = useRef<{ modes: PlayModes; at: number } | null>(null);

  const mirror = useCallback((q: QueueState) => {
    queueRef.current = q;
    setQueueState(q);
  }, []);

  const showNotice = useCallback((msg: string) => {
    if (noticeTimer.current) clearTimeout(noticeTimer.current);
    setNoticeState(msg);
    noticeTimer.current = setTimeout(() => {
      noticeTimer.current = null;
      setNoticeState(null);
    }, NOTICE_MS);
  }, []);
  useEffect(
    () => () => {
      if (noticeTimer.current) clearTimeout(noticeTimer.current);
    },
    [],
  );

  // Native → web.
  useEffect(
    () =>
      onNative((m) => {
        switch (m.type) {
          case "queue": {
            if (m.kind !== "track") return;
            const items = Array.isArray(m.items) ? m.items : [];
            const tracks = items.map((i) => fromItem({ ...i, kind: "track" }) as Track);
            const index = tracks.length === 0 ? 0 : Math.min(Math.max(0, m.index), tracks.length - 1);
            const next: QueueState = { tracks, index, source: m.source ?? "list" };
            const first = firstQueue.current && queueRef.current.tracks.length === 0;
            firstQueue.current = false;
            if (first) {
              // The app's queue on (re)opening: the block stored on this device, if it is this very queue.
              const restored = queueReducer(emptyQueue, { type: "restore", tracks, index, upNext: storedUpNext(userId, tracks.map((t) => t.id), index) });
              mirror({ ...restored, source: next.source });
            } else mirror(withQueued(queueRef.current, next));
            setReady(true);
            return;
          }
          case "state": {
            if (m.kind !== "track") {
              // Native plays one kind at a time: an episode sounding means music is not, whatever
              // music's last state said (a kind switch may report only the new kind).
              if (m.playing && playingRef.current) {
                playingRef.current = false;
                setState((s) => s && { ...s, playing: false, buffering: false });
                setClock((c) => ({ ...c, positionMs: Math.round(clockPosition(c, Date.now()) * 1000), playing: false, at: Date.now() }));
              }
              return;
            }
            // An automatic advance may reach the web as a state first.
            const q = queueRef.current;
            if (m.index !== q.index && m.index >= 0 && m.index < q.tracks.length && String(q.tracks[m.index].id) === m.itemId) {
              mirror(queueReducer(q, { type: "jump", index: m.index }));
            }
            const was = playingRef.current;
            playingRef.current = m.playing;
            const said = stateModes(m) ?? NO_MODES;
            const asked = pendingModes.current;
            // A state already on its way before native handled the last tap: the tap stands.
            const stale = asked && (said.shuffle !== asked.modes.shuffle || said.repeat !== asked.modes.repeat) && Date.now() - asked.at < MODES_PENDING_MS;
            if (!stale) {
              pendingModes.current = null;
              modesRef.current = said;
            }
            setState(m);
            setClock({ positionMs: m.positionMs, durationMs: m.durationMs, playing: m.playing, rate: m.rate, at: Date.now() });
            // Native sounding takes the session: a web preview or video pauses.
            if (m.playing && !was) claimSession("music", { force: true });
            return;
          }
          case "flushed": {
            const done = flushWaiters.current.get(m.id);
            if (done) done();
            return;
          }
          case "notice":
            if (typeof m.text === "string" && m.text) showNotice(m.text);
            return;
          default:
            return;
        }
      }),
    [mirror, showNotice],
  );

  // Web → native, once: native answers with its queue and state (and, on
  // its first hello after a launch, acts on the open preference).
  const onOpenRef = useRef(onOpen ?? "resume");
  useEffect(() => {
    nativePost({ type: "hello", onOpen: onOpenRef.current });
    const tm = setTimeout(() => setReady(true), READY_AFTER_MS);
    return () => clearTimeout(tm);
  }, []);
  useEffect(() => {
    nativePost({ type: "setPrefs", quality, carLyrics });
  }, [quality, carLyrics]);
  // Native's favorites cache follows every toggle.
  useEffect(() => onFavoriteSet((trackId, on) => nativePost({ type: "favoriteChanged", trackId, on })), []);
  // A preview or a video taking the session pauses native (an episode is
  // native's own: it switched itself).
  useEffect(
    () =>
      onSessionClaim((o) => {
        if (o === "preview" || o === "video") nativePost({ type: "pauseForWeb" });
      }),
    [],
  );

  // The queued block, kept on this device (see saveUpNext).
  useEffect(() => {
    if (queue.tracks.length > 0) saveUpNext(userId, queue);
  }, [queue, userId]);

  const sendQueue = useCallback((q: QueueState, start: { positionMs?: number; play: boolean }) => {
    nativePost({
      type: "setQueue", kind: "track", items: q.tracks.map(trackItem), index: q.index,
      ...(start.positionMs === undefined ? {} : { positionMs: start.positionMs }), play: start.play, source: q.source,
    });
  }, []);
  // A new queue or another entry: from its start, playing.
  const start = useCallback(
    (a: QueueAction) => {
      const q = queueReducer(queueRef.current, a);
      if (q.tracks.length === 0) return;
      mirror(q);
      setClock({ ...stoppedClock, durationMs: q.tracks[q.index].duration_ms, at: Date.now() });
      sendQueue(q, { positionMs: 0, play: true });
    },
    [mirror, sendQueue],
  );
  // An edit: the current entry keeps playing where it is (no positionMs).
  const edit = useCallback(
    (a: QueueAction, play = playingRef.current) => {
      const before = queueRef.current;
      const q = queueReducer(before, a);
      if (q === before) return;
      mirror(q);
      if (q.tracks.length === 0) nativePost({ type: "stop", kind: "track" });
      else sendQueue(q, { play });
    },
    [mirror, sendQueue],
  );

  const playList = useCallback(
    (tracks: Track[], i: number, opts?: { source?: "list" | "shuffle" | "favorites" }) => {
      if (tracks.length === 0) return;
      start({ type: "playList", tracks, start: i, source: opts?.source });
    },
    [start],
  );
  const jump = useCallback((index: number) => start({ type: "jump", index }), [start]);
  const enqueueNext = useCallback((tr: Track) => edit({ type: "enqueueNext", track: tr }), [edit]);
  const addToQueue = useCallback((tr: Track) => edit({ type: "addToQueue", track: tr }), [edit]);
  // The queue sheet: the current item can move (it stays current, unreloaded) but not go.
  const move = useCallback((from: number, to: number) => edit({ type: "move", from, to }), [edit]);
  const removeAt = useCallback((index: number) => edit({ type: "removeAt", index }), [edit]);
  const updateTrack = useCallback((tr: Track) => edit({ type: "updateTrack", track: tr }), [edit]);
  const remove = useCallback(
    (trackId: number) => {
      const q = queueRef.current;
      if (!q.tracks.some((x) => x.id === trackId)) return;
      const currentRemoved = q.tracks[q.index]?.id === trackId;
      // The current entry was the last one: the reducer falls back to the
      // previous track — stop there instead of playing it (as the web player).
      const fellBack = currentRemoved && !q.tracks.slice(q.index + 1).some((x) => x.id !== trackId);
      edit({ type: "remove", trackId }, fellBack ? false : playingRef.current);
      if (queueRef.current.tracks.length === 0) setClock(stoppedClock);
    },
    [edit],
  );

  // Shuffle and repeat run in the native engine (the lock screen and the car
  // too): the web only asks; native answers with its queue and state.
  const postModes = useCallback((m: PlayModes) => {
    modesRef.current = m;
    pendingModes.current = { modes: m, at: Date.now() };
    nativePost({ type: "setModes", shuffle: m.shuffle, repeat: m.repeat });
  }, []);
  const setShuffle = useCallback(
    (on: boolean) => {
      const m = modesRef.current;
      if (m.shuffle === on) return;
      // As the web player: everything after the current track is shuffled, queued tracks too.
      const q = queueRef.current;
      if (on && q.upNext) mirror(queueReducer(q, { type: "setOrder", tracks: q.tracks, index: q.index, upNext: 0 }));
      postModes({ ...m, shuffle: on });
    },
    [mirror, postModes],
  );
  const cycleRepeat = useCallback(() => postModes({ ...modesRef.current, repeat: nextRepeat(modesRef.current.repeat) }), [postModes]);

  const play = useCallback(() => nativePost({ type: "play", kind: "track" }), []);
  const pause = useCallback(() => nativePost({ type: "pause", kind: "track" }), []);
  const toggle = useCallback(() => (playingRef.current ? pause() : play()), [play, pause]);
  const next = useCallback(() => nativePost({ type: "next", kind: "track" }), []);
  const prev = useCallback(() => nativePost({ type: "prev", kind: "track" }), []);
  const seek = useCallback((s: number) => {
    const ms = Math.max(0, Math.round(s * 1000));
    nativePost({ type: "seek", kind: "track", ms });
    setClock((c) => ({ ...c, positionMs: ms, at: Date.now() }));
  }, []);
  const setQuality = useCallback((q: Quality) => {
    localStorage.setItem("lark.quality", q);
    setQualityState(q);
  }, []);
  const prime = useCallback(() => {}, []); // native needs no gesture

  const shuffleAll = useCallback(async () => {
    const ts = await api.randomTracks(50, []);
    playList(ts, 0, { source: "shuffle" });
  }, [playList]);
  const shuffleFavorites = useCallback(async () => {
    const { tracks, source, fellBack } = await favoritesOrFallback();
    if (tracks.length === 0) throw "empty library"; // a non-Error: callers show their localized "unavailable" text
    if (fellBack) showNotice(t("player.noFavoritesShuffleAll"));
    playList(tracks, 0, { source });
  }, [playList, showNotice]);

  // Native records the listen in progress and sends its pending play events.
  const flushEvents = useCallback(
    () =>
      new Promise<void>((resolve) => {
        const id = `flush-${Date.now()}-${++flushSeq}`;
        const done = () => {
          clearTimeout(timer);
          flushWaiters.current.delete(id);
          resolve();
        };
        const timer = setTimeout(done, FLUSH_TIMEOUT_MS);
        flushWaiters.current.set(id, done);
        nativePost({ type: "flushEvents", id });
      }),
    [],
  );

  const cur = current(queue);
  const playing = state?.playing ?? false;
  const error = state?.error ?? null;
  const clockBase = useMemo(
    () => (clock.durationMs > 0 || !cur ? clock : { ...clock, durationMs: cur.duration_ms }),
    [clock, cur],
  );
  const { position, duration } = useNativeClock(clockBase);
  const reported = stateModes(state);
  const modesAvailable = reported !== null;
  const shownShuffle = reported?.shuffle ?? false;
  const shownRepeat = reported?.repeat ?? "off";
  const modes = useMemo<PlayModes>(() => ({ shuffle: shownShuffle, repeat: shownRepeat }), [shownShuffle, shownRepeat]);

  const value = useMemo<Player>(
    () => ({
      queue, current: cur, playing, quality, error, notice, showNotice, needsTap: false,
      playList, enqueueNext, addToQueue, move, removeAt, toggle, play, pause, next, prev, seek, jump, remove, updateTrack, setQuality, prime, flushEvents, shuffleAll, shuffleFavorites, ready,
      modes, modesAvailable, setShuffle, cycleRepeat,
      // The native engine runs its own sleep timer; the web one is hidden here.
      setFade: noop, stopAfterCurrent: noop,
    }),
    [
      queue, cur, playing, quality, error, notice, showNotice, playList, enqueueNext, addToQueue, move, removeAt, toggle, play, pause, next, prev, seek, jump, remove,
      updateTrack, setQuality, prime, flushEvents, shuffleAll, shuffleFavorites, ready, modes, modesAvailable, setShuffle, cycleRepeat,
    ],
  );
  const progress = useMemo<PlayerProgress>(() => ({ position, duration }), [position, duration]);
  return (
    <PlayerCtx.Provider value={value}>
      <PlayerProgressCtx.Provider value={progress}>{children}</PlayerProgressCtx.Provider>
    </PlayerCtx.Provider>
  );
}
