import { useEffect, useMemo, useRef, type RefObject } from "react";
import { claimSession, onSessionClaim, ownsSession } from "./sessionOwner";

// iOS may pause the video a moment before it reports the page hidden: a
// playing video's pause this close before the hide counts as stopped by it.
export const PAUSE_BEFORE_HIDE_MS = 1500;

const VIDEO_ACTIONS: MediaSessionAction[] = ["play", "pause", "seekto", "nexttrack", "previoustrack", "seekbackward", "seekforward"];

function setAction(ms: MediaSession, a: MediaSessionAction, h: MediaSessionActionHandler | null) {
  try {
    ms.setActionHandler(a, h);
  } catch {
    /* unsupported action */
  }
}

// The video gives the session up (the page goes, or shows another video):
// nothing of it stays on the lock screen and music owns the session again,
// as when the episode player closes.
function releaseVideoSession() {
  if (!ownsSession("video")) return;
  const ms = typeof navigator !== "undefined" ? navigator.mediaSession : undefined;
  if (ms) {
    ms.metadata = null;
    VIDEO_ACTIONS.forEach((a) => setAction(ms, a, null));
  }
  claimSession("music");
}

/** A <video> that owns the Media Session as "video" while it plays. */
export interface SessionVideoOptions {
  /** Lock-screen metadata for the current video, or null before it is known. */
  metadata: () => MediaMetadataInit | null;
  /**
   * Every pause the hook saw (not one of an ended video): hidden = the page
   * was hidden; own = we or the lock screen paused it; hideSaved = this hide
   * was already saved through onHideWhilePlaying.
   */
  onPause?: (p: { second: number; hidden: boolean; own: boolean; wasPlaying: boolean; hideSaved: boolean }) => void;
  /** The page went to the background while the video was playing (save now: keepalive). Once per hide. */
  onHideWhilePlaying?: () => void;
  /** iOS stopped the video because the page went to the background, at this second. */
  onBackgroundStop?: (second: number) => void;
}

export interface SessionVideo {
  /** Spread onto the <video>: onPlay, onPause, onEnded. */
  handlers: { onPlay: () => void; onPause: () => void; onEnded: () => void };
  /** Pauses the video as "ours" (no background-stop offer follows). */
  pauseOwn: () => void;
  /** Gives the session back to music if the video still owns it. */
  release: () => void;
  playing: () => boolean;
  /** A regular (not keepalive) save happened: the next hide saves again. */
  saved: () => void;
}

export function useSessionVideo(video: RefObject<HTMLVideoElement | null>, src: string | null, opts: SessionVideoOptions): SessionVideo {
  const optsRef = useRef(opts);
  optsRef.current = opts;
  // Tracked from the element's own play/pause events (what iOS fires), not .paused.
  const videoPlaying = useRef(false);
  // A playing video's last pause that wasn't ours or the lock screen's: when, and at which second.
  const lastPause = useRef<{ at: number; second: number } | null>(null);
  // The next pause comes from us (another player took over) or the lock screen, not from iOS.
  const ownPause = useRef(false);
  // This hide was already saved (visibilitychange and pagehide usually both fire).
  const hideSaved = useRef(false);

  const sv = useMemo<SessionVideo>(() => {
    const pauseOwn = () => {
      const v = video.current;
      if (!v) return;
      // Only a pause that will happen is ours: flagging an already paused
      // video would swallow the next real (iOS background) pause.
      if (!v.paused) ownPause.current = true;
      v.pause();
    };
    const backgroundStop = (second: number) => {
      lastPause.current = null;
      optsRef.current.onBackgroundStop?.(second);
    };
    return {
      handlers: {
        onPlay: () => {
          videoPlaying.current = true;
          ownPause.current = false; // a flag no pause consumed never outlives a fresh play
          lastPause.current = null;
        },
        onPause: () => {
          // Read before clearing: iOS may pause before it reports the page hidden.
          const wasPlaying = videoPlaying.current;
          videoPlaying.current = false;
          const own = ownPause.current;
          ownPause.current = false;
          const v = video.current;
          if (!v || v.ended) return;
          const hidden = document.visibilityState === "hidden";
          optsRef.current.onPause?.({ second: v.currentTime, hidden, own, wasPlaying, hideSaved: hideSaved.current });
          if (!wasPlaying || own) return;
          if (hidden) backgroundStop(v.currentTime);
          else lastPause.current = { at: Date.now(), second: v.currentTime };
        },
        onEnded: () => {
          videoPlaying.current = false;
        },
      },
      pauseOwn,
      release: () => {
        lastPause.current = null;
        releaseVideoSession();
      },
      playing: () => videoPlaying.current,
      saved: () => {
        hideSaved.current = false;
      },
    };
  }, [video]);

  // The video steps aside when music, an episode or a preview takes over.
  useEffect(
    () =>
      onSessionClaim((o) => {
        if (o === "video") return;
        sv.pauseOwn();
      }),
    [sv],
  );

  // The video playing owns the session: it claims it (music and the episode
  // player pause), and while it owns it the lock screen shows this video
  // and its play/pause/seek drive it. Re-run on every "play" like the
  // other players: some browsers drop handlers across a fresh play.
  useEffect(() => {
    const v = video.current;
    if (!v) return;
    const onPlay = () => {
      claimSession("video");
      const ms = typeof navigator !== "undefined" ? navigator.mediaSession : undefined;
      const md = optsRef.current.metadata();
      if (!ms || !md || !ownsSession("video")) return;
      if (typeof MediaMetadata !== "undefined") ms.metadata = new MediaMetadata(md);
      const handlers: Partial<Record<MediaSessionAction, MediaSessionActionHandler>> = {
        // A car's "play" resumes a paused video, never pauses a playing one.
        play: () => {
          if (v.paused) void v.play()?.catch(() => {});
        },
        pause: () => {
          if (!v.paused) ownPause.current = true;
          v.pause();
        },
        seekto: (d) => {
          if (d.seekTime !== undefined) v.currentTime = d.seekTime;
        },
      };
      VIDEO_ACTIONS.forEach((a) => setAction(ms, a, handlers[a] ?? null));
    };
    v.addEventListener("play", onPlay);
    return () => {
      v.removeEventListener("play", onPlay);
      // The element left the page (the page went, another video, ▶ 听):
      // pausing alone leaves iOS (and Chrome until GC) pulling the stream,
      // which competes with the next one on a slow link. A src change on
      // the same element (the 高清 switch) keeps it connected and is left alone.
      if (!v.isConnected) {
        v.pause();
        v.removeAttribute("src");
        v.load();
      }
    };
  }, [video, src]);

  // Leaving the page while the video owns the session.
  useEffect(() => () => releaseVideoSession(), []);

  // Page hide: save a playing video (once per hide), and notice iOS having
  // paused it just before.
  useEffect(() => {
    const saveOnHide = () => {
      if (!videoPlaying.current || hideSaved.current) return;
      hideSaved.current = true;
      optsRef.current.onHideWhilePlaying?.();
    };
    const onVis = () => {
      if (document.visibilityState !== "hidden") {
        hideSaved.current = false;
        return;
      }
      saveOnHide();
      const p = lastPause.current;
      if (!videoPlaying.current && p && Date.now() - p.at <= PAUSE_BEFORE_HIDE_MS) {
        lastPause.current = null;
        optsRef.current.onBackgroundStop?.(p.second);
      }
    };
    document.addEventListener("visibilitychange", onVis);
    window.addEventListener("pagehide", saveOnHide);
    return () => {
      document.removeEventListener("visibilitychange", onVis);
      window.removeEventListener("pagehide", saveOnHide);
    };
  }, []);

  return sv;
}
