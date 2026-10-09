import { createContext, useCallback, useContext, useEffect, useMemo, useReducer, useRef, useState, type ReactNode } from "react";
import { api, streamUrl } from "../api/client";
import { coverUrl } from "../components/Cover";
import type { Lyrics, OnOpen, Quality, Track } from "../api/types";
import { t } from "../i18n/i18n";
import {
  cachedFavorites, isPlayerBuffering, localKind, notePlayed, onPlayerBuffering, playableNow, requestLookahead, setPlayerBuffering,
} from "../offline/bridge";
import { OFFLINE_QUALITY } from "../offline/keys";
import { EventBuffer } from "./events";
import { activeLine, getLyrics, isBlankLine, onLyrics, prefetchLyrics } from "./lyricsCache";
import { chooseNext, isLocalChoice, type ChooseOpts, type NextChoice } from "./nextTrack";
import { BlobPreloader, BLOB_MAX_BYTES, estimateHighBytes } from "./preload";
import { claimSession, onSessionClaim, ownsSession } from "./sessionOwner";
import { appendable, current, emptyQueue, needsRefill, queueReducer, upcoming, type QueueAction, type QueueState } from "./queue";
import { hasNative } from "../native/bridge";
import { NativePlayerProvider } from "./NativePlayerProvider";

export interface Player {
  queue: QueueState;
  current: Track | null;
  playing: boolean;
  quality: Quality;
  error: string | null;
  // A passing message (e.g. offline, uncached songs were skipped); clears itself.
  notice: string | null;
  // Shows a passing notice in the mini player (also while idle).
  showNotice(msg: string): void;
  // Autoplay was refused when Lark opened with a shuffled favorites queue
  // (iOS Safari always, desktop browsers often): the queue is loaded but
  // silent, and the next tap anywhere starts it (see prime()).
  needsTap: boolean;
  playList(tracks: Track[], start: number, opts?: { source?: "list" | "shuffle" | "favorites" }): void;
  enqueueNext(t: Track): void;
  toggle(): void;
  play(): void;
  pause(): void;
  next(): void;
  prev(): void;
  seek(seconds: number): void;
  jump(index: number): void;
  remove(trackId: number): void;
  // Replaces every queue entry sharing this track's id with the given
  // object (an admin edit can land while the track is playing, queued more
  // than once, or both) — the reducer's identity change propagates to
  // `current`, so NowPlaying/MiniPlayer/lock-screen metadata pick it up
  // without a reload.
  updateTrack(t: Track): void;
  setQuality(q: Quality): void;
  // Must be called synchronously inside a tap handler that is about to
  // await something before calling playList() (e.g. a fetch): iOS only
  // lets a media element start playing from a user gesture, and a play()
  // after an await is no longer "inside" the gesture. Priming plays and
  // immediately pauses the element once from the gesture, which unlocks it
  // for the later play(). As the document's tap listener it gets the event:
  // taps inside [data-no-music-prime] (episode, video, preview controls) are
  // ignored.
  prime(e?: Event): void;
  // Records the listen in progress and sends every buffered play event.
  // Call before logging out, while the session is still valid.
  flushEvents(): Promise<void>;
  // Replaces the whole queue with a random slice of the library and plays it.
  // Primes the audio element synchronously (like playList) before the
  // fetch, so it stays callable directly from a tap handler.
  shuffleAll(): Promise<void>;
  // Like shuffleAll(), but of the user's favorites: a "favorites" queue that
  // refills and loops. With no favorites it plays a global shuffle and says so.
  shuffleFavorites(): Promise<void>;
  // False until the boot (resume / shuffle on open) has settled: the idle
  // mini player must not offer a shortcut that would race the restore.
  ready: boolean;
}

export interface PlayerProgress {
  position: number;
  duration: number;
}

const Ctx = createContext<Player | null>(null);
// Position/duration change on every timeupdate (several times a second).
// They live in their own context so only the two components that show
// progress re-render per tick — not every track row on the page.
const ProgressCtx = createContext<PlayerProgress | null>(null);
// The native transport (NativePlayerProvider) fills the same two contexts.
export { Ctx as PlayerCtx, ProgressCtx as PlayerProgressCtx };
const SKIP_THRESHOLD_S = 30;
const MAX_CONSECUTIVE_ERRORS = 3;
// MediaError.MEDIA_ERR_NETWORK: the download failed (e.g. a 4G dead zone),
// not the file. Retry the same track (at most this many times, with these
// delays) before moving on — to a cached track when there is one.
const MEDIA_ERR_NETWORK = 2;
const NETWORK_RETRY_DELAYS_MS = [2000, 5000, 10_000];
// Visible page: a network track stalled this long gives way to a cached one
// (hidden, that happens at once — a locked iPhone suspends a silent page).
const STALL_MS = 6000;
// How many upcoming tracks are downloaded ahead and prepared on the server.
const LOOKAHEAD = 3;
// While hidden, a user action shields its track from automatic switching
// this long at most (a pick that never starts must not mean silence).
const USER_ACTION_HIDDEN_MS = 10_000;
// A track that failed to play is skipped this long (then tried again).
const FAILED_TTL_MS = 30 * 60_000;
// HTMLMediaElement readyState / networkState values.
const HAVE_FUTURE_DATA = 3;
const NETWORK_LOADING = 2;
const RADIO_RETRY_MS = 30_000;
// REFILL_EXCLUDE_MAX caps the exclude list a radio/shuffle refill sends.
const REFILL_EXCLUDE_MAX = 300;
// How long a passing notice stays up.
const NOTICE_MS = 4000;
// After "playing", the rest of the track may still be downloading; until
// "canplaythrough" (or this long) the offline cache leaves the network alone.
const BUFFERING_GRACE_MS = 10_000;
// A buffering that never reports its end (no canplaythrough, no error) stops
// blocking the offline cache after this long.
const BUFFERING_MAX_MS = 120_000;

// The first index at or after `from` whose track can play right now (offline,
// only cached ones can), or -1.
function firstPlayable(tracks: Track[], from: number): number {
  for (let i = from; i < tracks.length; i++) if (playableNow(tracks[i].id)) return i;
  return -1;
}

const isHidden = () => typeof document !== "undefined" && document.visibilityState === "hidden";
const isOnline = () => typeof navigator === "undefined" || navigator.onLine !== false;

function makePreloader(): BlobPreloader | null {
  if (typeof URL === "undefined" || typeof URL.createObjectURL !== "function") return null;
  return new BlobPreloader({
    fetch: (u, init) => fetch(u, init),
    busy: isPlayerBuffering,
    createURL: (b) => URL.createObjectURL(b),
    revokeURL: (u) => URL.revokeObjectURL(u),
  });
}

// How long after a tap started a needsTap queue that tap's own play/pause
// handling is ignored (iOS fires the click well after the touchend).
const TAP_SETTLE_MS = 1000;

// 0.05 s of silence, 8 kHz mono 8-bit PCM. Generated with:
// ffmpeg -f lavfi -i anullsrc=r=8000:cl=mono -t 0.05 -c:a pcm_u8 silent.wav
export const SILENT_WAV =
  "data:audio/wav;base64,UklGRtYBAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgATElTVBoAAABJTkZPSVNGVA4AAABMYXZmNjIuMTIuMTAxAGRhdGGQAQAAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgA==";

const isNotAllowed = (e: unknown) => (e as { name?: unknown } | null)?.name === "NotAllowedError";
// The refill source for each queue source's answer: a favorites refill that
// fell back to the whole library becomes a plain shuffle.
const favoritesSource = (src: "favorites" | "all") => (src === "favorites" ? "favorites" : "shuffle");

// A shuffled favorites queue, or — with no favorites — a shuffle of the whole
// library (fellBack). Shared by the boot path and the shuffleFavorites() action.
async function favoritesOrFallback(): Promise<{ tracks: Track[]; source: "favorites" | "shuffle"; fellBack: boolean }> {
  const r = await api.randomFavorites(50, []);
  if (r.source === "favorites" && r.tracks.length > 0) return { tracks: r.tracks, source: "favorites", fellBack: false };
  const tracks = r.tracks.length > 0 ? r.tracks : await api.randomTracks(50, []);
  return { tracks, source: "shuffle", fellBack: true };
}

function storedQuality(): Quality {
  const q = localStorage.getItem("lark.quality");
  return q === "lossless" || q === "saver" ? q : "high";
}

interface Listen {
  trackId: number;
  startedAt: number;
  seconds: number;
  lastTime: number;
}

interface LoadOpts {
  autoplay: boolean;
  startAt?: number;
  // Chosen by the user (a tap, next/previous): not switched away from for a
  // slow start. "keep": whatever the pending action says.
  picked?: boolean | "keep";
  // The queue index this track is being loaded for. A playlist can contain
  // the same track id twice; tracking the *position* (not just the id) lets
  // us tell "still on the same instance" apart from "advanced onto a
  // duplicate of the same track", which needs a restart from 0.
  index: number;
}

interface NetRetry {
  attempt: number;
  position: number;
  timer: ReturnType<typeof setTimeout> | null;
}

export type PlayerProviderProps = Parameters<typeof WebPlayerProvider>[0];

// Inside the Lark iPhone app (window.larkNative) music plays in the native
// engine; everywhere else on this page's own <audio>. Read once: a page
// never switches transport.
export function PlayerProvider(props: PlayerProviderProps) {
  const [native] = useState(hasNative);
  return native ? <NativePlayerProvider {...props} /> : <WebPlayerProvider {...props} />;
}

function WebPlayerProvider({
  children,
  audio: injected,
  userId,
  onOpen,
  carLyrics = true,
}: {
  children: ReactNode;
  audio?: HTMLAudioElement;
  userId?: number;
  // What to do when the player mounts; default "resume" (the original behaviour).
  onOpen?: OnOpen;
  // Put the current synced lyric line into the media-session title (car
  // Bluetooth displays and the lock screen show only title/artist/album).
  // Unlike onOpen this is live: toggling it applies at once. Default on.
  carLyrics?: boolean;
}) {
  const [audio] = useState<HTMLAudioElement>(() => injected ?? new Audio());
  const [queue, dispatch] = useReducer(queueReducer, emptyQueue);
  const [playing, setPlaying] = useState(false);
  // The element is really sounding (a "playing" event since the last load or
  // pause) — not prime()'s muted play-and-pause blip on the first tap.
  const [sounding, setSounding] = useState(false);
  const [position, setPosition] = useState(0);
  const [duration, setDuration] = useState(0);
  const [quality, setQualityState] = useState<Quality>(storedQuality);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNoticeState] = useState<string | null>(null);
  const noticeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const showNotice = useCallback((msg: string) => {
    if (noticeTimer.current) clearTimeout(noticeTimer.current);
    setNoticeState(msg);
    noticeTimer.current = setTimeout(() => {
      noticeTimer.current = null;
      setNoticeState(null);
    }, NOTICE_MS);
  }, []);
  const bufferingTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const setBuffering = useCallback(function setBuffering(b: boolean) {
    if (bufferingTimer.current) clearTimeout(bufferingTimer.current);
    bufferingTimer.current = b ? setTimeout(() => setBuffering(false), BUFFERING_MAX_MS) : null;
    setPlayerBuffering(b);
  }, []);
  const [needsTap, setNeedsTapState] = useState(false);

  const queueRef = useRef(queue);
  queueRef.current = queue;
  const qualityRef = useRef(quality);
  qualityRef.current = quality;
  const loadedId = useRef<number | null>(null);
  const loadedIndex = useRef<number>(-1);
  // The queue index a prev (or offline-skip) dispatch is expected to land
  // on, set only when that dispatch will actually move the index. This
  // (not a plain boolean) is what lets the "load whatever became current"
  // effect distinguish "the engine actually navigated onto a duplicate of
  // the currently-loaded track" from "the queue was edited
  // (remove/enqueueNext) and the currently-playing instance just got
  // renumbered" — the latter must NOT restart playback. A boolean flag
  // would go stale whenever the dispatch doesn't actually change
  // cur/queue.index (e.g. prev() at the first track, which the reducer
  // clamps to the same index): the effect then never runs to consume it,
  // so it would still read true at the next, unrelated queue edit. Storing
  // the specific target index side-steps that: we simply never set it
  // unless the index is actually about to change, and the effect only
  // treats it as navigation when queue.index lands exactly on it.
  // jump(), next, ended and errors need none of this — they load
  // synchronously, like playList() (see go()).
  const navTarget = useRef<number | null>(null);
  const wantPlay = useRef(false);
  const pendingSeek = useRef(0);
  const listen = useRef<Listen | null>(null);
  const errors = useRef(0);
  const refilling = useRef(false);
  const radioExhausted = useRef(false);
  const radioRetryTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [radioTick, setRadioTick] = useState(0);
  const restored = useRef(false);
  const [ready, setReady] = useState(false);
  // The restore dispatch must not trigger a save of its own: the position
  // it would send is audio.currentTime, which is still 0 until metadata
  // arrives, and would overwrite the position we just restored from.
  const skipNextSave = useRef(false);
  // An empty queue is normally never saved (it's the state before restore).
  // Once the user removes the last track, saving the empty queue is right.
  const allowEmptySave = useRef(false);
  // True once the element has actually played (a "playing" event) — only
  // then is it unlocked for iOS. A restored track is merely loaded.
  const hasPlayed = useRef(false);
  // True while a prime() attempt hasn't been rejected as "not a gesture".
  const primed = useRef(false);
  // prime() just played the loaded track muted and paused it at once. A
  // browser delivers that blip's "playing" after prime() has restored
  // `muted`, so it is remembered here (set before the play, read instead of
  // the element's muted state then): that "playing" never takes the session.
  // Consumed by it; cleared by the next real play().
  const primeBlip = useRef(false);
  // Last position known to be valid for the loaded track (for network retries).
  const knownPos = useRef(0);
  const netRetry = useRef<NetRetry | null>(null);
  // Tracks that failed to play (not the network), and when: skipped for
  // FAILED_TTL_MS. Forgotten when the network comes back, and once
  // something plays after a burst of failures (a network-wide problem).
  const failedIds = useRef(new Map<number, number>());
  const isFailed = useCallback((id: number) => {
    const at = failedIds.current.get(id);
    if (at === undefined) return false;
    if (Date.now() - at < FAILED_TTL_MS) return true;
    failedIds.current.delete(id);
    return false;
  }, []);
  // Since its load, the loaded track has played (a "playing" event).
  const startedSinceLoad = useRef(false);
  const stallTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // The user acted (picked, sought, pressed play/next/previous — the lock
  // screen and the car included) and the track hasn't played since: no
  // automatic switch away from it until it does.
  // When (Date.now()) and at which position; null: no action pending.
  const userActedAt = useRef<number | null>(null);
  const actedPos = useRef(0);
  const markAct = useCallback((pos: number) => {
    userActedAt.current = Date.now();
    actedPos.current = pos;
  }, []);
  const acting = useCallback(() => {
    const at = userActedAt.current;
    return at !== null && (!isHidden() || Date.now() - at < USER_ACTION_HIDDEN_MS);
  }, []);
  // Tracks moved past in a row because their network failed.
  const netSkips = useRef(0);
  const prepared = useRef("");
  const [preloader] = useState(makePreloader);
  // Read once: changing the preference in Settings must never re-run the boot.
  const onOpenRef = useRef<OnOpen>(onOpen ?? "resume");
  const needsTapRef = useRef(false);
  // When the capture-phase prime() last started a needsTap queue. For
  // TAP_SETTLE_MS afterwards play()/toggle() are no-ops, so that same tap's
  // own handler (typically the ▶ button's toggle()) doesn't pause it again —
  // including on iOS, where the tap's click arrives in a later task than
  // the touchend that already started playback.
  const tapStartedAt = useRef(-Infinity);
  const setNeedsTap = useCallback((v: boolean) => {
    needsTapRef.current = v;
    setNeedsTapState(v);
  }, []);
  const events = useMemo(
    () => new EventBuffer(api.postEvents, localStorage, userId === undefined ? "lark.pendingEvents" : `lark.pendingEvents.${userId}`),
    [userId],
  );

  const clearStall = useCallback(() => {
    if (stallTimer.current) clearTimeout(stallTimer.current);
    stallTimer.current = null;
  }, []);

  // The track's bytes are on the phone: the offline cache or a preloaded blob.
  const isLocal = useCallback((id: number) => localKind(id) !== null || (preloader?.has(id) ?? false), [preloader]);

  // Where to load a track from: a preloaded blob; the cached copy for one
  // downloaded ahead (or any cached copy while hidden — no time for a
  // network-first try); otherwise the stream at the chosen quality.
  const srcFor = useCallback(
    (track: Track) => {
      const blob = preloader?.take(track.id);
      if (blob) return blob;
      const kind = localKind(track.id);
      if (kind === "lookahead" || (kind !== null && isHidden())) return streamUrl(track.id, OFFLINE_QUALITY);
      return streamUrl(track.id, qualityRef.current);
    },
    [preloader],
  );

  // play(), and when the browser refuses it (no gesture — e.g. a
  // background start): ask for a tap rather than look as if playing.
  const playOrAskTap = useCallback(
    (trackId: number) => {
      primeBlip.current = false;
      void audio.play()?.catch((e: unknown) => {
        if (!isNotAllowed(e) || loadedId.current !== trackId) return;
        wantPlay.current = false;
        setPlaying(false);
        setNeedsTap(true);
      });
    },
    [audio, setNeedsTap],
  );

  const clearNetRetry = useCallback(() => {
    const r = netRetry.current;
    if (r?.timer) clearTimeout(r.timer);
    netRetry.current = null;
  }, []);

  const finishListen = useCallback(
    (reason: "ended" | "skip" | "switch") => {
      const l = listen.current;
      listen.current = null;
      if (!l || l.seconds < 1) return;
      events.add({
        track_id: l.trackId,
        started_at: l.startedAt,
        played_seconds: Math.round(l.seconds),
        skipped: reason === "skip" && l.seconds < SKIP_THRESHOLD_S,
        quality: qualityRef.current,
      });
      void events.flush();
    },
    [events],
  );

  const load = useCallback(
    (track: Track, opts: LoadOpts) => {
      const { autoplay, startAt = 0, index, picked = false } = opts;
      finishListen("switch");
      clearNetRetry();
      clearStall();
      if (picked === true) markAct(startAt);
      else if (picked === false) userActedAt.current = null;
      startedSinceLoad.current = false;
      setSounding(false);
      loadedId.current = track.id;
      loadedIndex.current = index;
      pendingSeek.current = startAt;
      knownPos.current = startAt;
      listen.current = { trackId: track.id, startedAt: Math.floor(Date.now() / 1000), seconds: 0, lastTime: startAt };
      setPosition(startAt);
      setDuration(track.duration_ms / 1000);
      // A track merely loaded (restored, paused) isn't fetched by every
      // browser until it plays: only a playing load counts as buffering.
      setBuffering(autoplay);
      notePlayed(track, false);
      audio.src = srcFor(track);
      if (autoplay) playOrAskTap(track.id);
    },
    [audio, finishListen, clearNetRetry, clearStall, setBuffering, srcFor, playOrAskTap, markAct],
  );

  // The never-stop choice of what plays next (see nextTrack.ts).
  const choose = useCallback(
    (opts: ChooseOpts): NextChoice =>
      chooseNext(queueRef.current, opts, {
        local: isLocal,
        failed: isFailed,
        playable: playableNow,
        favorites: cachedFavorites,
        now: Date.now,
        random: Math.random,
      }),
    [isLocal, isFailed],
  );
  // What a track change normally asks for: hidden (or offline), only a
  // track already on the phone, when there is one.
  const changeOpts = useCallback((): ChooseOpts => {
    const away = isHidden() || !isOnline();
    return { mustBeLocal: away, favoritesAtEnd: away };
  }, []);

  // Plays a choice synchronously — no render round-trip, no timer: in the
  // background this runs inside the element's own event, the last moment
  // the page is sure to be running.
  // `requeue`: the interrupted track, put back right after the substitute.
  const go = useCallback(
    (c: NextChoice, notice?: string, o: { picked?: boolean; requeue?: Track | null } = {}): boolean => {
      if (c.kind === "none") return false;
      const requeue = o.requeue ?? undefined;
      const action: QueueAction =
        c.kind === "queue" ? { type: "advance", to: c.index, requeue } : { type: "advanceInsert", track: c.track, requeue };
      const nq = queueReducer(queueRef.current, action);
      queueRef.current = nq;
      dispatch(action);
      load(nq.tracks[nq.index], { autoplay: wantPlay.current, index: nq.index, picked: o.picked });
      if (c.kind === "queue" && c.skippedOffline) showNotice(t("player.skippedUncached"));
      else if (notice) showNotice(notice);
      return true;
    },
    [load, showNotice],
  );

  // Reload the same track at the last known position after a network error.
  const retryNetwork = useCallback(() => {
    const r = netRetry.current;
    if (!r) return;
    if (r.timer) clearTimeout(r.timer);
    r.timer = null;
    const track = current(queueRef.current);
    if (!track || loadedId.current !== track.id) {
      netRetry.current = null;
      return;
    }
    pendingSeek.current = r.position;
    if (listen.current) listen.current.lastTime = r.position;
    audio.src = srcFor(track);
    if (wantPlay.current) playOrAskTap(track.id);
  }, [audio, srcFor, playOrAskTap]);

  // Load whatever became current (next/prev/jump/remove/ended), keeping play state.
  const cur = current(queue);
  useEffect(() => {
    if (!cur) return;
    // Already loaded exactly this instance (e.g. jump() already loaded it
    // synchronously for the iOS tap-gesture rule) — nothing to do.
    if (cur.id === loadedId.current && queue.index === loadedIndex.current) {
      navTarget.current = null;
      return;
    }
    if (!playableNow(cur.id)) {
      // Offline and not cached: on to the next cached track, if any (with
      // none, it's tried as before and fails the usual way).
      const j = firstPlayable(queue.tracks, queue.index + 1);
      if (j >= 0) {
        navTarget.current = j;
        dispatch({ type: "jump", index: j });
        showNotice(t("player.skippedUncached"));
        return;
      }
    }
    if (cur.id !== loadedId.current || queue.index === navTarget.current) {
      // Either a genuinely different track, or the same id reached by
      // explicit navigation (prev, an offline skip) landing exactly on the
      // index that navigation targeted, onto a duplicate of the
      // currently-loaded track (e.g. queue [A, A, B]) — both need a real
      // (re)load so a broken duplicate gets a fresh attempt instead of
      // silently stalling (play() on an already-errored element rejects
      // without firing a new error event).
      navTarget.current = null;
      load(cur, { autoplay: wantPlay.current, index: queue.index, picked: "keep" });
      return;
    }
    // Same id, different slot, but not the index navigation targeted: the
    // queue was edited (remove()/enqueueNext()) and the instance that's
    // already playing just got renumbered. Follow the renumbering without
    // restarting playback.
    loadedIndex.current = queue.index;
  }, [cur, queue.index, queue.tracks, load, showNotice]);

  // Release the audio element when the provider unmounts (e.g. logout).
  useEffect(() => {
    return () => {
      finishListen("switch");
      clearNetRetry();
      clearStall();
      if (radioRetryTimer.current) clearTimeout(radioRetryTimer.current);
      audio.pause();
      audio.removeAttribute("src");
      audio.load();
      setBuffering(false);
      if (noticeTimer.current) clearTimeout(noticeTimer.current);
    };
  }, [audio, finishListen, clearNetRetry, clearStall, setBuffering]);

  // The blob preloader waits for the current track to stop buffering.
  useEffect(() => {
    if (!preloader) return;
    const off = onPlayerBuffering(() => preloader.busyChanged());
    return () => {
      off();
      preloader.release();
    };
  }, [preloader]);

  // Audio element events.
  useEffect(() => {
    const onTime = () => {
      const l = listen.current;
      if (l) {
        const d = audio.currentTime - l.lastTime;
        if (d > 0 && d < 2) l.seconds += d;
        l.lastTime = audio.currentTime;
      }
      // While a seek is pending the element reports 0 — not a real position.
      if (pendingSeek.current === 0) knownPos.current = audio.currentTime;
      // Playing on past where the user put it: the action is done.
      if (userActedAt.current !== null && !audio.paused && !audio.seeking && audio.currentTime > actedPos.current + 0.25) {
        userActedAt.current = null;
      }
      setPosition(audio.currentTime);
    };
    // A seek into buffered audio fires no "playing": landing while playing ends the action.
    const onSeeked = () => {
      if (!audio.paused) userActedAt.current = null;
    };
    const onMeta = () => {
      if (pendingSeek.current > 0) {
        audio.currentTime = pendingSeek.current;
        pendingSeek.current = 0;
      }
      if (Number.isFinite(audio.duration)) setDuration(audio.duration);
      // A paused retry never reaches "playing": loading metadata is success.
      if (netRetry.current && audio.paused) {
        clearNetRetry();
        setError(null);
      }
    };
    const onPlay = () => setPlaying(true);
    const onPause = () => {
      clearStall();
      setSounding(false);
      setPlaying(false);
      if (!audio.src.startsWith("data:")) setBuffering(false); // nobody's listening: downloads may go ahead
    };
    const onPlaying = () => {
      if (bufferingTimer.current) clearTimeout(bufferingTimer.current);
      bufferingTimer.current = setTimeout(() => setBuffering(false), BUFFERING_GRACE_MS);
      hasPlayed.current = true;
      setNeedsTap(false); // started some other way (lock screen, headset)
      clearStall();
      userActedAt.current = null;
      startedSinceLoad.current = true;
      setSounding(true);
      // Something plays after a burst of failures: they were the network's.
      if (errors.current >= MAX_CONSECUTIVE_ERRORS) failedIds.current.clear();
      errors.current = 0;
      netSkips.current = 0;
      clearNetRetry();
      setError(null);
    };
    const stop = (msg: string | null) => {
      wantPlay.current = false;
      setPlaying(false);
      setBuffering(false);
      if (msg !== null) setError(msg);
    };
    const onEnded = () => {
      if (audio.src.startsWith("data:")) return; // the silent unlock clip
      clearStall();
      finishListen("ended");
      const done = current(queueRef.current);
      // The next track is about to buffer: say so before the cache hears of
      // the finished one, or it would start downloading in that very gap.
      setBuffering(true);
      if (done) notePlayed(done, true);
      if (!go(choose(changeOpts()))) stop(null);
    };
    // A track that doesn't come from the phone and isn't moving: switch to
    // one that does, if there is one.
    // The interrupted network track is requeued after the substitute.
    const switchable = () => {
      const id = loadedId.current;
      return id !== null && wantPlay.current && !audio.src.startsWith("data:") && !isLocal(id) && !audio.seeking && !acting();
    };
    const stallSwitch = () => {
      clearStall();
      if (!switchable()) return;
      const c = choose({ mustBeLocal: true, favoritesAtEnd: true });
      if (isLocalChoice(c)) go(c, t("player.playingCached"), { requeue: current(queueRef.current) });
    };
    const onStall = (kind: "waiting" | "stalled") => {
      if (!switchable()) return;
      if (!isHidden()) {
        if (!stallTimer.current) stallTimer.current = setTimeout(stallSwitch, STALL_MS);
        return;
      }
      // Hidden: at once — on the browser's own "stalled" (seconds without
      // data), or a "waiting" of a track that had started and has run dry
      // while still loading.
      if (kind === "stalled" || (startedSinceLoad.current && audio.readyState < HAVE_FUTURE_DATA && audio.networkState === NETWORK_LOADING)) {
        stallSwitch();
      }
    };
    const onNetworkError = () => {
      const position = pendingSeek.current > 0 ? pendingSeek.current : knownPos.current;
      const r: NetRetry = netRetry.current ?? { attempt: 0, position, timer: null };
      r.position = position;
      if (r.timer) clearTimeout(r.timer);
      r.timer = null;
      netRetry.current = r;
      setError(t("player.networkRetry"));
      const capped = r.attempt >= NETWORK_RETRY_DELAYS_MS.length;
      // Hidden, the retry timers would be frozen with the page: a cached
      // track now beats a retry later.
      if (isHidden() || capped) {
        const c = choose({ mustBeLocal: true, favoritesAtEnd: true });
        if (isLocalChoice(c) && go(c, t("player.playingCached"), { requeue: current(queueRef.current) })) {
          setError(null);
          return;
        }
      }
      if (!capped) {
        r.timer = setTimeout(retryNetwork, NETWORK_RETRY_DELAYS_MS[r.attempt]);
        r.attempt += 1;
        return;
      }
      // Offline with nothing cached: no more timers — the "online" event retries.
      if (!isOnline()) return;
      netSkips.current += 1;
      listen.current = null;
      const c: NextChoice = netSkips.current < MAX_CONSECUTIVE_ERRORS ? choose({ mustBeLocal: false, favoritesAtEnd: true }) : { kind: "none" };
      if (go(c)) {
        setError(null);
        return;
      }
      clearNetRetry();
      stop(t("player.networkStopped"));
    };
    const onError = () => {
      setBuffering(false);
      clearStall();
      // Errors from the silent unlock clip (or an emptied element) aren't
      // about any queued track.
      if (loadedId.current === null || audio.src.startsWith("data:")) return;
      if (audio.error?.code === MEDIA_ERR_NETWORK) {
        onNetworkError();
        return;
      }
      // The file itself won't play (decode error, 404, 415): not again this session.
      failedIds.current.set(loadedId.current, Date.now());
      errors.current += 1;
      const track = current(queueRef.current);
      const tooMany = errors.current >= MAX_CONSECUTIVE_ERRORS;
      const c = choose({ mustBeLocal: tooMany || isHidden(), favoritesAtEnd: true });
      listen.current = null;
      // Several in a row: only a track from the phone is worth trying.
      if ((tooMany && !isLocalChoice(c)) || !go(c)) {
        stop(track ? t("player.cannotPlayTrack", { title: track.title }) : t("player.cannotPlay"));
      }
    };
    const onWaiting = () => {
      setBuffering(true);
      onStall("waiting");
    };
    // iOS also reports stalls for a paused element that simply isn't loading.
    const onStalled = () => {
      if (audio.paused) return;
      setBuffering(true);
      onStall("stalled");
    };
    const onSettled = () => setBuffering(false);
    const handlers: [string, () => void][] = [
      ["timeupdate", onTime], ["loadedmetadata", onMeta], ["play", onPlay], ["pause", onPause],
      ["playing", onPlaying], ["ended", onEnded], ["error", onError], ["seeked", onSeeked],
      // Not "emptied": assigning src fires it for every new track, right after load() set buffering.
      ["waiting", onWaiting], ["stalled", onStalled], ["canplaythrough", onSettled],
    ];
    handlers.forEach(([n, h]) => audio.addEventListener(n, h));
    // Back online: don't wait out the backoff.
    const onOnline = () => {
      failedIds.current.clear(); // maybe they were the network's
      retryNetwork();
    };
    window.addEventListener("online", onOnline);
    return () => {
      handlers.forEach(([n, h]) => audio.removeEventListener(n, h));
      window.removeEventListener("online", onOnline);
    };
  }, [audio, finishListen, clearNetRetry, clearStall, retryNetwork, setNeedsTap, setBuffering, choose, changeOpts, go, isLocal, acting]);
  // A seek (from anywhere) cancels a pending stall switch.
  useEffect(() => {
    audio.addEventListener("seeking", clearStall);
    return () => audio.removeEventListener("seeking", clearStall);
  }, [audio, clearStall]);

  const prime = useCallback((e?: Event) => {
    // An episode, video or preview control: that tap starts something else,
    // never the music (it would only be paused again a moment later).
    const target = e?.target as Element | null | undefined;
    if (target?.closest?.("[data-no-music-prime]")) return;
    // An episode (video, preview) owns the session: a tap elsewhere (a nav
    // tab) is no music action — not even a muted unlock. Direct calls
    // (shuffleAll, no event) are music actions and go on.
    if (e && !ownsSession("music")) return;
    // Autoplay was refused on open: this tap is the gesture that starts the
    // already-loaded queue — for real, not muted and paused, and never via
    // the silent clip (no src change). Checked before the guards below,
    // since an earlier prime may already have run.
    if (needsTapRef.current) {
      setNeedsTap(false);
      const id = loadedId.current;
      if (id !== null && audio.paused) {
        primed.current = true;
        wantPlay.current = true;
        tapStartedAt.current = performance.now();
        primeBlip.current = false;
        audio.play()?.catch((e: unknown) => {
          // Still not a gesture to this browser: keep the prompt for the next tap.
          if (!isNotAllowed(e) || hasPlayed.current || loadedId.current !== id || !audio.paused) return;
          primed.current = false;
          wantPlay.current = false;
          setNeedsTap(true);
        });
        return;
      }
    }
    if (hasPlayed.current || primed.current || !audio.paused) return;
    primed.current = true;
    if (loadedId.current !== null) {
      // A track is loaded (e.g. restored after login) but has never played
      // from a gesture. Play and immediately pause it, muted so nothing is
      // heard. The immediate pause() makes the play() promise reject with
      // an AbortError in real browsers — only NotAllowedError means the
      // tap didn't count as a gesture.
      const { muted, volume } = audio;
      audio.muted = true;
      primeBlip.current = true;
      const attempt = audio.play();
      audio.pause();
      audio.muted = muted;
      audio.volume = volume;
      attempt?.catch((e: unknown) => {
        if (e instanceof Error && e.name === "NotAllowedError") primed.current = false;
      });
      return;
    }
    audio.src = SILENT_WAV;
    audio
      .play()
      ?.then(() => {
        // A real track may have been loaded (and started) meanwhile — only
        // stop the silent clip itself.
        if (audio.src === SILENT_WAV) audio.pause();
      })
      .catch(() => {
        // Not treated as a user gesture (or some other rejection) — let the
        // next tap try again.
        primed.current = false;
      });
  }, [audio, setNeedsTap]);

  // iOS only lets a media element start playing from a user gesture, and
  // `pointerdown` does not count as one there (only click/touchend do).
  // Prime the element on every click/touchend until it has actually played
  // once ("playing" fired). A track that is merely loaded — e.g. restored
  // from the server after login — does NOT count: it was never played from
  // a gesture. prime() itself skips taps once an attempt wasn't rejected.
  useEffect(() => {
    const cleanup = () => {
      document.removeEventListener("click", prime, true);
      document.removeEventListener("touchend", prime, true);
      audio.removeEventListener("playing", cleanup);
    };
    audio.addEventListener("playing", cleanup);
    document.addEventListener("click", prime, true);
    document.addEventListener("touchend", prime, true);
    return cleanup;
  }, [audio, prime]);

  const playList = useCallback(
    (tracks: Track[], start: number, opts?: { source?: "list" | "shuffle" | "favorites" }) => {
      if (tracks.length === 0) return;
      let i = Math.min(Math.max(start, 0), tracks.length - 1);
      if (!playableNow(tracks[i].id)) {
        const j = firstPlayable(tracks, i + 1);
        if (j >= 0) {
          i = j;
          showNotice(t("player.skippedUncached"));
        }
      }
      radioExhausted.current = false;
      errors.current = 0;
      setError(null);
      wantPlay.current = true;
      dispatch({ type: "playList", tracks, start: i, source: opts?.source });
      load(tracks[i], { autoplay: true, index: i, picked: true }); // synchronous play(): iOS only allows it inside the tap handler
    },
    [load, showNotice],
  );

  const shuffleAll = useCallback(async () => {
    // Unlock the element inside the tap: the playList() below runs after an
    // await, which iOS no longer counts as part of the gesture.
    prime();
    const ts = await api.randomTracks(50, []);
    playList(ts, 0, { source: "shuffle" });
  }, [prime, playList]);

  const shuffleFavorites = useCallback(async () => {
    prime(); // inside the tap, like shuffleAll()
    const { tracks, source, fellBack } = await favoritesOrFallback();
    if (tracks.length === 0) throw "empty library"; // a non-Error: callers show their localized "unavailable" text
    if (fellBack) showNotice(t("player.noFavoritesShuffleAll"));
    playList(tracks, 0, { source });
  }, [prime, playList, showNotice, t]);

  const play = useCallback(() => {
    if (performance.now() - tapStartedAt.current < TAP_SETTLE_MS) return; // this tap's prime() already started it
    wantPlay.current = true;
    // A slow (re)start of what the user asked for isn't switched away from;
    // a "play" to an element already playing (a car connecting) is no action.
    if (audio.paused) markAct(audio.currentTime);
    const q = queueRef.current;
    const track = current(q);
    if (track && loadedId.current !== track.id) load(track, { autoplay: true, index: q.index, picked: true });
    else if (netRetry.current) retryNetwork(); // the element is errored: play() alone would just reject
    else if (track) playOrAskTap(track.id);
  }, [audio, load, retryNetwork, markAct, playOrAskTap]);

  const pause = useCallback(() => {
    wantPlay.current = false;
    audio.pause();
  }, [audio]);

  const toggle = useCallback(() => {
    if (performance.now() - tapStartedAt.current < TAP_SETTLE_MS) return; // this tap's prime() just started it: don't pause it again
    if (audio.paused) play();
    else pause();
  }, [audio, play, pause]);

  // The same never-stop choice as at the end of a track (from the lock
  // screen or the car, too), loaded synchronously.
  const next = useCallback(() => {
    const c = choose(changeOpts());
    if (c.kind === "none") return; // already at the end: nothing moves
    finishListen("skip");
    go(c, undefined, { picked: true });
  }, [finishListen, choose, changeOpts, go]);

  const seek = useCallback(
    (s: number) => {
      markAct(s);
      clearStall();
      audio.currentTime = s;
      knownPos.current = s;
      if (netRetry.current) netRetry.current.position = s;
      if (listen.current) listen.current.lastTime = s;
      setPosition(s);
    },
    [audio, markAct, clearStall],
  );

  const prev = useCallback(() => {
    const q = queueRef.current;
    if (audio.currentTime > 3 || q.index === 0) seek(0);
    else {
      markAct(0);
      navTarget.current = q.index - 1;
      dispatch({ type: "prev" });
    }
  }, [audio, seek, markAct]);

  // No navTarget bookkeeping needed here: unlike prev,
  // jump() loads synchronously (like playList()), so by the time the "load
  // whatever became current" effect runs, loadedId/loadedIndex already
  // match — its "already loaded this instance" early-return handles it.
  const jump = useCallback(
    (index: number) => {
      const q = queueRef.current;
      if (q.tracks.length === 0) return;
      let i = Math.min(Math.max(index, 0), q.tracks.length - 1);
      if (!playableNow(q.tracks[i].id)) {
        const j = firstPlayable(q.tracks, i + 1);
        if (j >= 0) {
          i = j;
          showNotice(t("player.skippedUncached"));
        }
      }
      wantPlay.current = true;
      dispatch({ type: "jump", index: i });
      load(q.tracks[i], { autoplay: true, index: i, picked: true }); // synchronous play(): iOS only allows it inside the tap handler
    },
    [load, showNotice],
  );
  const enqueueNext = useCallback((t: Track) => dispatch({ type: "enqueueNext", track: t }), []);
  const updateTrack = useCallback((t: Track) => dispatch({ type: "updateTrack", track: t }), []);
  const remove = useCallback(
    (trackId: number) => {
      const q = queueRef.current;
      if (!q.tracks.some((t) => t.id === trackId)) return;
      const remaining = q.tracks.filter((t) => t.id !== trackId);
      const currentRemoved = q.tracks[q.index]?.id === trackId;
      if (remaining.length === 0) {
        // Nothing left: stop and release the element (the effect that loads
        // "whatever became current" has nothing to load).
        wantPlay.current = false;
        finishListen("switch");
        clearNetRetry();
        audio.pause();
        audio.removeAttribute("src");
        audio.load();
        loadedId.current = null;
        loadedIndex.current = -1;
        pendingSeek.current = 0;
        knownPos.current = 0;
        setPosition(0);
        setDuration(0);
        setNeedsTap(false);
        allowEmptySave.current = true;
      } else if (currentRemoved && !q.tracks.slice(q.index + 1).some((t) => t.id !== trackId)) {
        // The current entry was the last one: the reducer falls back to the
        // previous track. Stop there instead of autoplaying it.
        wantPlay.current = false;
        audio.pause();
      }
      dispatch({ type: "remove", trackId });
    },
    [audio, finishListen, clearNetRetry, setNeedsTap],
  );

  const setQuality = useCallback(
    (q: Quality) => {
      localStorage.setItem("lark.quality", q);
      qualityRef.current = q;
      setQualityState(q);
      const track = current(queueRef.current);
      if (track && loadedId.current === track.id) {
        const pos = audio.currentTime;
        const wasPlaying = !audio.paused;
        pendingSeek.current = pos;
        audio.src = streamUrl(track.id, q);
        if (wasPlaying) {
          primeBlip.current = false;
          void audio.play()?.catch(() => {});
        }
      }
    },
    [audio],
  );

  // Radio/shuffle refill when the queue is about to run out. A "shuffle"
  // queue (started via shuffleAll()) refills from the random endpoint,
  // keeping the source "shuffle"; a "favorites" queue (opened with
  // shuffle_favorites) refills from the user's favorites and loops over
  // them (see appendable); every other source keeps using radio. An answer
  // that would add nothing is never dispatched — a no-op append would still
  // change the queue and re-run this effect in a loop. For radio/shuffle it
  // means the source is exhausted; for favorites only an empty answer does.
  // A failed request (e.g. no signal) is retried on the next queue change,
  // or after RADIO_RETRY_MS.
  useEffect(() => {
    if (queue.tracks.length === 0 || !needsRefill(queue, isFailed) || refilling.current || radioExhausted.current) return;
    refilling.current = true;
    // Only the most recent ids: enough to avoid near repeats, and the
    // request URL stays bounded however long the session's queue grows.
    const ids = queue.tracks.slice(-REFILL_EXCLUDE_MAX).map((t) => t.id);
    const fetchMore: Promise<{ ts: Track[]; source: "radio" | "shuffle" | "favorites" }> =
      queue.source === "favorites"
        ? api.randomFavorites(20, ids).then((r) => ({ ts: r.tracks, source: favoritesSource(r.source) }))
        : queue.source === "shuffle"
          ? api.randomTracks(20, ids).then((ts) => ({ ts, source: "shuffle" as const }))
          : api.radio(10, ids).then((ts) => ({ ts, source: "radio" as const }));
    fetchMore
      .then(({ ts, source }) => {
        if (appendable(queueRef.current, ts, source).length > 0) dispatch({ type: "append", tracks: ts, source });
        // Nothing to add from a favorites answer that wasn't empty: every
        // favorite is already upcoming. Not exhausted — the next queue change
        // (the next track) asks again and gets the following pass.
        else if (source !== "favorites" || ts.length === 0) radioExhausted.current = true;
      })
      .catch(() => {
        if (radioRetryTimer.current) clearTimeout(radioRetryTimer.current);
        radioRetryTimer.current = setTimeout(() => {
          radioRetryTimer.current = null;
          setRadioTick((n) => n + 1);
        }, RADIO_RETRY_MS);
      })
      .finally(() => {
        refilling.current = false;
      });
  }, [queue, radioTick, isFailed]);

  // While this track plays: the lyrics of the next one; the next few
  // transcoded on the server and downloaded to the phone (the offline
  // cache waits until the current track has buffered) — or, with no
  // offline cache, the next one fetched into memory — so that a track
  // change never waits on the network.
  useEffect(() => {
    if (!cur) return;
    const ahead = upcoming(queue)
      .filter((t) => !isFailed(t.id))
      .slice(0, LOOKAHEAD);
    const n = ahead[0];
    if (!n) return;
    prefetchLyrics(n.id); // found lyrics are cached per track; a miss is asked again next change
    // At the tier the lookahead fetches (and a cached copy plays at).
    const key = ahead.map((t) => t.id).join(",");
    if (prepared.current !== key) {
      prepared.current = key;
      void api.prepareTracks(ahead.map((t) => t.id), OFFLINE_QUALITY).catch(() => {});
    }
    // Downloads only while something plays: opening the app (a restored,
    // paused queue) spends no data.
    if (!sounding) return;
    const before = queue.tracks[queue.index - 1];
    const accepted = requestLookahead(ahead, before ? [cur.id, before.id] : [cur.id]);
    if (preloader && !accepted.includes(n.id) && localKind(n.id) === null && estimateHighBytes(n) <= BLOB_MAX_BYTES) {
      preloader.request(n.id, streamUrl(n.id, OFFLINE_QUALITY));
    }
  }, [cur, queue, preloader, sounding, isFailed]);

  // Boot, once, per the user's "When Lark opens" preference (read once at
  // mount). "resume" restores the server-side queue, paused; "nothing"
  // leaves the queue empty; "shuffle_favorites" plays a shuffle of the
  // user's favorites (or of everything, when they have none) — and when the
  // browser refuses that autoplay, needsTap asks for the tap that prime()
  // turns into the start.
  useEffect(() => {
    const restoreQueue = () =>
      api.queue().then(({ queue: q, tracks }) => {
        if (tracks.length === 0 || loadedId.current !== null) return;
        const curId = q.track_ids[q.current_index];
        // `tracks` is positionally aligned with `track_ids`, so prefer the
        // exact position when it agrees — a queue can legitimately contain
        // the same track id twice, and a plain findIndex would always pick
        // the first copy regardless of which one the server says is current.
        const idx = tracks[q.current_index]?.id === curId ? q.current_index : Math.max(0, tracks.findIndex((t) => t.id === curId));
        skipNextSave.current = true;
        dispatch({ type: "restore", tracks, index: idx });
        load(tracks[idx], { autoplay: false, startAt: tracks[idx].id === curId ? q.position_ms / 1000 : 0, index: idx });
      });
    const shuffleOnOpen = () =>
      favoritesOrFallback().then(({ source, tracks }) => {
        // Something already started (a tap was faster than the fetch): keep it.
        if (tracks.length === 0 || loadedId.current !== null) return;
        radioExhausted.current = false;
        errors.current = 0;
        setError(null);
        dispatch({ type: "playList", tracks, start: 0, source });
        const first = tracks[0];
        load(first, { autoplay: false, index: 0 });
        // An episode started while this was loading: the queue waits, silent.
        if (!ownsSession("music")) return;
        wantPlay.current = true;
        primeBlip.current = false;
        audio.play()?.catch((e: unknown) => {
          // Only a refusal of *this* start, with nothing played since, asks for a tap.
          if (!isNotAllowed(e) || hasPlayed.current || loadedId.current !== first.id || !audio.paused) return;
          wantPlay.current = false;
          setNeedsTap(true);
        });
      });
    const mode = onOpenRef.current;
    (mode === "nothing" ? Promise.resolve() : mode === "shuffle_favorites" ? shuffleOnOpen() : restoreQueue())
      .catch(() => {})
      .finally(() => {
        restored.current = true;
        setReady(true);
      });
    void events.flush();
    const t = setInterval(() => void events.flush(), 30_000);
    return () => clearInterval(t);
  }, [events, load, audio, setNeedsTap]);

  // Save the queue: debounced on change, periodically while playing, and
  // when the page is hidden or closed (keepalive, so it survives unload).
  const saveQueue = useCallback(
    (keepalive = false) => {
      if (!restored.current) return;
      const q = queueRef.current;
      if (q.tracks.length === 0 && !allowEmptySave.current) return;
      allowEmptySave.current = false;
      // Until metadata arrives, a pending seek (restore, quality switch,
      // network retry) is the real position; audio.currentTime still says 0.
      const pos = pendingSeek.current > 0 ? pendingSeek.current : audio.currentTime;
      const empty = q.tracks.length === 0;
      void api
        .saveQueue(
          { track_ids: q.tracks.map((t) => t.id), current_index: empty ? 0 : q.index, position_ms: empty ? 0 : Math.round(pos * 1000) },
          { keepalive },
        )
        .catch(() => {});
    },
    [audio],
  );
  useEffect(() => {
    if (skipNextSave.current) {
      skipNextSave.current = false;
      return;
    }
    const t = setTimeout(() => saveQueue(), 1000);
    return () => clearTimeout(t);
  }, [queue, saveQueue]);
  useEffect(() => {
    if (!playing) return;
    const t = setInterval(() => saveQueue(), 15_000);
    return () => clearInterval(t);
  }, [playing, saveQueue]);
  useEffect(() => {
    const onHide = () => saveQueue(true);
    const onVisibility = () => {
      if (document.visibilityState === "hidden") saveQueue(true);
    };
    window.addEventListener("pagehide", onHide);
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      window.removeEventListener("pagehide", onHide);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [saveQueue]);

  const flushEvents = useCallback(async () => {
    finishListen("switch");
    await events.drain();
  }, [events, finishListen]);

  // Lock screen / headset controls. seekbackward/seekforward are explicitly
  // nulled out (rather than left unset) so the lock screen doesn't show
  // stale default skip-by-N-seconds buttons we don't implement. Some mobile
  // browsers drop a media session's action handlers across a fresh `play`
  // (e.g. after the element's src was reassigned for a new track), so the
  // whole registration is re-run on every audio "play" event, not just once
  // per dependency change.
  useEffect(() => {
    const ms = navigator.mediaSession;
    if (!ms) return;
    const set = (a: MediaSessionAction, h: MediaSessionActionHandler | null) => {
      try {
        ms.setActionHandler(a, h);
      } catch {
        /* unsupported action */
      }
    };
    const actions: [MediaSessionAction, MediaSessionActionHandler | null][] = [
      ["play", play],
      ["pause", pause],
      ["nexttrack", next],
      ["previoustrack", prev],
      ["seekto", (d) => d.seekTime !== undefined && seek(d.seekTime)],
      ["seekbackward", null],
      ["seekforward", null],
    ];
    // Only while music owns the session (an episode, video or preview may);
    // music claiming it back re-registers them.
    const register = () => {
      if (ownsSession("music")) actions.forEach(([a, h]) => set(a, h));
    };
    register();
    audio.addEventListener("play", register);
    const off = onSessionClaim((o) => {
      if (o === "music") register();
    });
    return () => {
      off();
      audio.removeEventListener("play", register);
      if (ownsSession("music")) actions.forEach(([a]) => set(a, null));
    };
  }, [audio, play, pause, next, prev, seek]);
  // Music really sounding takes the session back — not the silent unlock
  // clip, not a muted prime (see primeBlip). Separate from the lock-screen
  // handlers: it must work where there is no Media Session too.
  useEffect(() => {
    const onPlaying = () => {
      if (primeBlip.current) {
        primeBlip.current = false;
        return;
      }
      if (audio.src.startsWith("data:") || audio.muted) return;
      claimSession("music", { force: true });
    };
    audio.addEventListener("playing", onPlaying);
    return () => audio.removeEventListener("playing", onPlaying);
  }, [audio]);
  // Lock screen / Control Center / car display metadata. Keyed by the track
  // id, not the object: the queue replaces the object for a favorite or
  // dislike toggle, and that must neither flash the song back over a lyric
  // line nor ask for the lyrics again. An edit that changes what is shown
  // (title/artist/album) re-renders the current state via renderMeta.
  const curRef = useRef(cur);
  curRef.current = cur;
  const renderMeta = useRef<(() => void) | null>(null);
  const curId = cur?.id;
  useEffect(() => {
    const ms = navigator.mediaSession;
    if (!ms || curId === undefined || typeof MediaMetadata === "undefined") return;
    // Lock screen / Control Center art: absolute same-origin URLs, so the
    // session cookie authenticates them like the audio stream.
    const art = ([300, 1000] as const).map((size) => ({
      src: new URL(coverUrl("track", curId, size), location.href).href,
      sizes: `${size}x${size}`,
      type: "image/jpeg",
    }));
    // Car lyrics: the current line becomes the title and "song · artist" the
    // artist. Driven by the element's timeupdate — it keeps firing while iOS
    // plays in the background, unlike rAF or React renders — and the session
    // is only touched when the shown line changes. Lyrics come from the
    // shared cache (the mini player fetches them; this joins that request).
    let lines: { t_ms: number; text: string }[] | null = null;
    let offset = 0; // the lyrics' shared shift (lines show at t_ms + offset)
    let shown = -1; // index of the line in the title; -1 = the song
    const render = () => {
      if (!ownsSession("music")) return;
      const c = curRef.current;
      if (!c || c.id !== curId) return;
      if (shown < 0) {
        ms.metadata = new MediaMetadata({ title: c.title, artist: c.artist, album: c.album, artwork: art });
      } else {
        const byline = c.artist ? `${c.title} · ${c.artist}` : c.title;
        ms.metadata = new MediaMetadata({ title: lines![shown].text.trim(), artist: byline, album: c.album, artwork: art });
      }
    };
    renderMeta.current = render;
    render();
    if (!carLyrics) {
      return () => {
        if (renderMeta.current === render) renderMeta.current = null;
      };
    }
    const apply = () => {
      const i = lines ? activeLine(lines, audio.currentTime, offset) : -1;
      const line = i >= 0 && !isBlankLine(lines![i].text) ? i : -1;
      if (line === shown) return;
      shown = line;
      render();
    };
    const take = (l: Lyrics) => {
      lines = l.found && l.synced && !l.instrumental && l.lines?.length ? l.lines : null;
      offset = l.offset_ms ?? 0;
      apply();
    };
    let live = true;
    getLyrics(curId).then((l) => live && take(l), () => {});
    const off = onLyrics((id, l) => live && id === curId && take(l));
    audio.addEventListener("timeupdate", apply);
    return () => {
      live = false;
      off();
      audio.removeEventListener("timeupdate", apply);
      if (renderMeta.current === render) renderMeta.current = null;
    };
  }, [curId, carLyrics, audio]);
  // Someone else claimed the session: music steps aside (paused, its queue
  // kept). Whether or not it was playing, nothing of music's may start by
  // itself afterwards — no tap-to-start prompt, no pending network retry, no
  // stall switch, no autoplay of the next load — only an explicit music
  // action (play, a pick, shuffle) brings it back. Claimed back: the song's
  // metadata returns to the lock screen.
  useEffect(
    () =>
      onSessionClaim((o) => {
        if (o === "music") {
          renderMeta.current?.();
          return;
        }
        wantPlay.current = false;
        setNeedsTap(false);
        clearNetRetry();
        clearStall();
        if (!audio.paused && !audio.src.startsWith("data:")) audio.pause();
      }),
    [audio, setNeedsTap, clearNetRetry, clearStall],
  );
  // Same track, edited: re-render only if what the session shows changed.
  const shownKey = cur ? `${cur.id}\n${cur.title}\n${cur.artist}\n${cur.album}` : "";
  const renderedKey = useRef(shownKey);
  useEffect(() => {
    if (renderedKey.current === shownKey) return;
    const sameTrack = renderedKey.current.split("\n")[0] === shownKey.split("\n")[0];
    renderedKey.current = shownKey;
    if (sameTrack) renderMeta.current?.();
  }, [shownKey]);

  // Lock-screen scrubber position. loadedmetadata always updates it (a new
  // track just loaded); timeupdate is throttled to once a second since it
  // otherwise fires several times a second. setPositionState throws on
  // invalid values (e.g. a non-finite duration mid-load), hence the guards.
  useEffect(() => {
    const ms = navigator.mediaSession;
    if (!ms || typeof ms.setPositionState !== "function") return;
    let last = 0;
    const apply = () => {
      if (!ownsSession("music") || !Number.isFinite(audio.duration)) return;
      try {
        ms.setPositionState({ duration: audio.duration, position: audio.currentTime, playbackRate: 1 });
      } catch {
        /* invalid values */
      }
    };
    const onTime = () => {
      const now = Date.now();
      if (now - last < 1000) return;
      last = now;
      apply();
    };
    const onMeta = () => {
      last = Date.now();
      apply();
    };
    audio.addEventListener("timeupdate", onTime);
    audio.addEventListener("loadedmetadata", onMeta);
    return () => {
      audio.removeEventListener("timeupdate", onTime);
      audio.removeEventListener("loadedmetadata", onMeta);
    };
  }, [audio]);

  const value = useMemo<Player>(
    () => ({
      queue, current: cur, playing, quality, error, notice, showNotice, needsTap,
      playList, enqueueNext, toggle, play, pause, next, prev, seek, jump, remove, updateTrack, setQuality, prime, flushEvents, shuffleAll, shuffleFavorites, ready,
    }),
    [
      queue, cur, playing, quality, error, notice, showNotice, needsTap, playList, enqueueNext, toggle, play, pause, next, prev, seek, jump, remove,
      updateTrack, setQuality, prime, flushEvents, shuffleAll, shuffleFavorites, ready,
    ],
  );
  const progress = useMemo<PlayerProgress>(() => ({ position, duration }), [position, duration]);
  return (
    <Ctx.Provider value={value}>
      <ProgressCtx.Provider value={progress}>{children}</ProgressCtx.Provider>
    </Ctx.Provider>
  );
}

export function usePlayer(): Player {
  const v = useContext(Ctx);
  if (!v) throw new Error("usePlayer outside PlayerProvider");
  return v;
}

export function usePlayerProgress(): PlayerProgress {
  const v = useContext(ProgressCtx);
  if (!v) throw new Error("usePlayerProgress outside PlayerProvider");
  return v;
}
