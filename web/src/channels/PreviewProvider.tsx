import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { api, ApiError } from "../api/client";
import type { PreviewInfo } from "../api/types";
import { useDownloads } from "../downloads/DownloadsProvider";
import { duration } from "../format";
import { failedPreviewText, isTransient, previewErrorText } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { SILENT_WAV } from "../player/PlayerProvider";
import { claimSession, onSessionClaim, ownsSession } from "../player/sessionOwner";

export interface PreviewTarget {
  videoId: string;
  media: "audio" | "video";
  title: string;
  channel: string;
  durationS: number;
  keepTo: "music" | "channel";
  /** The audio is already on disk (an episode): when the video can't be previewed, offer ▶ 听 with this instead of an audio preview. */
  listenInstead?: () => void;
  /** Start at this second (视频's ▶ 听 goes on from where the video was): applied once the first load's metadata is in. */
  startAt?: number;
}

interface PreviewsApi {
  open(t: PreviewTarget): void;
  close(): void;
}

const Ctx = createContext<PreviewsApi | null>(null);
export const PREVIEW_POLL_MS = 2000;

export function usePreviews(): PreviewsApi {
  const v = useContext(Ctx);
  if (!v) throw new Error("usePreviews outside PreviewProvider");
  return v;
}

const ACTIONS: MediaSessionAction[] = ["play", "pause", "seekto", "nexttrack", "previoustrack", "seekbackward", "seekforward"];

function setAction(ms: MediaSession, a: MediaSessionAction, h: MediaSessionActionHandler | null) {
  try {
    ms.setActionHandler(a, h);
  } catch {
    /* unsupported action */
  }
}

function mediaSession(): MediaSession | undefined {
  return typeof navigator !== "undefined" ? navigator.mediaSession : undefined;
}

// The preview gives the session up (closed, kept, unmounted): nothing of it
// stays on the lock screen and music owns the session again — only when the
// preview still owns it, so closing a preview never stops whoever took over.
function releaseSession() {
  if (!ownsSession("preview")) return;
  const ms = mediaSession();
  if (ms) {
    ms.metadata = null;
    ACTIONS.forEach((a) => setAction(ms, a, null));
  }
  claimSession("music");
}

// What went wrong, and what the sheet offers for it.
type Problem = { text: string; offer?: "retry" | "audio" };

function problemOf(e: unknown): Problem {
  const code = e instanceof ApiError ? e.code : undefined;
  if (code === "video_preview_unavailable") return { text: previewErrorText(e), offer: "audio" };
  if (code === "preview_retry") return { text: previewErrorText(e), offer: "retry" };
  return { text: previewErrorText(e) };
}

function failedProblem(p: PreviewInfo): Problem {
  return { text: failedPreviewText(p.error), offer: p.error === "video_preview_unavailable" ? "audio" : undefined };
}

/**
 * ▶ 试听: one preview at a time in a bottom sheet with its own <audio>
 * (unlocked inside the tap, so it may start after the server answers) or a
 * <video controls>. It is its own session owner ("preview"): opening it
 * pauses music, an episode or an episode's video, and any of them taking
 * over pauses it. 保留 hands the file to the music library or Channels.
 */
export function PreviewProvider({ children, audio: injected }: { children: ReactNode; audio?: HTMLAudioElement }) {
  const t = useT();
  const dl = useDownloads();
  const [audio] = useState<HTMLAudioElement>(() => injected ?? new Audio());
  const [target, setTarget] = useState<PreviewTarget | null>(null);
  const [info, setInfo] = useState<PreviewInfo | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [kept, setKept] = useState<{ to: "music" | "channel"; trackId?: number } | null>(null);
  const [keepErr, setKeepErr] = useState(""); // 保留 failed: the preview itself goes on
  const [playing, setPlaying] = useState(false);
  const [pos, setPos] = useState(0);
  const seq = useRef(0);
  const poll = useRef<ReturnType<typeof setTimeout> | null>(null);
  const video = useRef<HTMLVideoElement>(null);
  const targetRef = useRef(target);
  targetRef.current = target;
  const infoRef = useRef(info);
  infoRef.current = info;
  // The next status read, put off while the page is hidden (run when it shows again).
  const deferred = useRef<(() => void) | null>(null);
  // The stream broke (a 409 preview_not_ready after the wait cap, a seek past
  // the downloaded bytes, a cut-off response): where it was, and how many
  // status reads have seen it still downloading since.
  const broken = useRef<{ at: number; tries: number } | null>(null);
  // Reloaded once the download was done: another error is a real failure.
  const reloadedDone = useRef(false);
  // Where a reloaded stream (or a preview opened at startAt) goes on from, once its metadata is in.
  const seekTo = useRef(0);

  const stopPoll = () => {
    if (poll.current) clearTimeout(poll.current);
    poll.current = null;
    deferred.current = null;
  };
  const resetStream = () => {
    broken.current = null;
    reloadedDone.current = false;
    seekTo.current = 0;
  };
  // Every 2 s, only while visible — except while a broken stream waits for its reload.
  const schedule = (fn: () => void) => {
    poll.current = setTimeout(() => {
      poll.current = null;
      if (document.visibilityState === "hidden" && !broken.current) deferred.current = fn;
      else fn();
    }, PREVIEW_POLL_MS);
  };
  const mediaEl = (): HTMLMediaElement | null => (targetRef.current?.media === "video" ? video.current : audio);
  const pauseAll = () => {
    audio.pause();
    video.current?.pause();
  };

  // Loads the stream again, from where it broke, and goes on playing if the preview still owns the session.
  const reload = (p: PreviewInfo) => {
    const el = mediaEl();
    if (!el) return;
    seekTo.current = broken.current?.at ?? 0;
    broken.current = null;
    reloadedDone.current = p.status === "done";
    el.src = p.stream_url;
    el.load();
    if (ownsSession("preview")) el.play()?.catch(() => {});
  };
  const reloadRef = useRef(reload);
  reloadRef.current = reload;

  // While the preview owns the session: the lock screen shows it, and its
  // play/pause/seek drive this element. Re-run on every "play" (some
  // browsers drop handlers across a fresh play).
  const ownLockScreen = useCallback((el: HTMLMediaElement) => {
    const ms = mediaSession();
    const tg = targetRef.current;
    if (!ms || !tg || !ownsSession("preview")) return;
    if (typeof MediaMetadata !== "undefined") ms.metadata = new MediaMetadata({ title: tg.title, artist: tg.channel });
    const handlers: Partial<Record<MediaSessionAction, MediaSessionActionHandler>> = {
      // A car's "play" resumes a paused preview, never pauses a playing one.
      play: () => {
        if (el.paused) void el.play()?.catch(() => {});
      },
      pause: () => el.pause(),
      seekto: (d) => {
        if (d.seekTime !== undefined) el.currentTime = d.seekTime;
      },
    };
    ACTIONS.forEach((a) => setAction(ms, a, handlers[a] ?? null));
  }, []);

  const watch = useCallback((id: number, mine: number) => {
    stopPoll();
    api.preview(id).then(
      (p) => {
        if (seq.current !== mine) return;
        setInfo(p);
        infoRef.current = p;
        if (p.status === "failed") {
          broken.current = null;
          pauseAll();
          setProblem(failedProblem(p));
          return;
        }
        // A broken stream: reload once the file is done, or after one more read if it still downloads.
        const b = broken.current;
        if (b) {
          if (p.status === "done" || b.tries >= 1) reloadRef.current(p);
          else b.tries++;
        }
        if (p.status === "downloading" || broken.current) schedule(() => watch(id, mine));
      },
      (e) => {
        if (seq.current !== mine) return;
        // A network hiccup: ask again. The server said why (pushed back, gone, failed): stop and say so.
        if (isTransient(e)) schedule(() => watch(id, mine));
        else {
          broken.current = null;
          pauseAll();
          setProblem(problemOf(e));
        }
      },
    );
  }, [audio]);

  // The audio or the video stream failed: find out where the preview stands
  // and reload it; a failure after a reload of the finished file is final.
  const onMediaError = useCallback(
    (el: HTMLMediaElement) => {
      const i = infoRef.current;
      if (!i || !el.src || el.src.startsWith("data:")) return;
      el.pause(); // shows ▶: a tap reloads it at once
      if (reloadedDone.current) {
        stopPoll();
        pauseAll();
        setProblem({ text: t("preview.failed"), offer: "retry" });
        return;
      }
      if (!broken.current) broken.current = { at: el.currentTime, tries: 0 };
      watch(i.id, seq.current);
    },
    [t, watch],
  );

  const open = useCallback(
    (p: PreviewTarget) => {
      const mine = ++seq.current;
      stopPoll();
      setTarget(p);
      targetRef.current = p;
      setInfo(null);
      setProblem(null);
      setKept(null);
      setKeepErr("");
      setPos(0);
      resetStream();
      seekTo.current = p.startAt ?? 0;
      claimSession("preview");
      if (p.media === "audio") {
        // Unlock this element inside the tap (iOS): the real source comes after the request.
        audio.src = SILENT_WAV;
        audio.play()?.catch(() => {});
      } else {
        audio.pause();
        audio.removeAttribute("src");
      }
      api.startPreview({ video_id: p.videoId, media: p.media, title: p.title, channel: p.channel, duration_s: p.durationS }).then(
        (i) => {
          if (seq.current !== mine) return;
          setInfo(i);
          if (i.status === "failed") {
            setProblem(failedProblem(i));
            return;
          }
          if (p.media === "audio") {
            audio.src = i.stream_url;
            // Someone else took over while the server answered: ready, not playing.
            if (ownsSession("preview")) audio.play()?.catch(() => {});
          }
          if (i.status === "downloading") watch(i.id, mine);
        },
        (e) => {
          if (seq.current !== mine) return;
          audio.pause();
          setProblem(problemOf(e));
        },
      );
    },
    [audio, watch],
  );

  const stop = useCallback(() => {
    seq.current++;
    stopPoll();
    resetStream();
    audio.pause();
    audio.removeAttribute("src");
    video.current?.pause();
  }, [audio]);

  const close = useCallback(() => {
    stop();
    setTarget(null);
    setInfo(null);
    setProblem(null);
    setKept(null);
    setKeepErr("");
    releaseSession();
  }, [stop]);

  useEffect(() => {
    const isStream = () => audio.src !== "" && !audio.src.startsWith("data:");
    const onPlay = () => {
      if (!isStream()) return; // the silent unlock
      setPlaying(true);
      claimSession("preview");
      ownLockScreen(audio);
    };
    const onPause = () => setPlaying(false);
    const onTime = () => setPos(audio.currentTime);
    const onError = () => onMediaError(audio);
    const onMeta = () => {
      if (!isStream()) return; // the silent unlock's metadata: not where to seek
      if (seekTo.current > 0) audio.currentTime = seekTo.current;
      seekTo.current = 0;
    };
    const hs: [string, () => void][] = [["play", onPlay], ["pause", onPause], ["timeupdate", onTime], ["error", onError], ["loadedmetadata", onMeta]];
    hs.forEach(([n, h]) => audio.addEventListener(n, h));
    // A status read put off while hidden runs when the page shows again.
    const onVisible = () => {
      const fn = deferred.current;
      if (document.visibilityState === "hidden" || !fn) return;
      deferred.current = null;
      fn();
    };
    document.addEventListener("visibilitychange", onVisible);
    const off = onSessionClaim((o) => {
      if (o === "preview") return;
      if (!audio.paused) audio.pause();
      video.current?.pause();
    });
    return () => {
      hs.forEach(([n, h]) => audio.removeEventListener(n, h));
      document.removeEventListener("visibilitychange", onVisible);
      off();
    };
  }, [audio, ownLockScreen, onMediaError]);

  // Unmount (logout, a user switch): stop, release the element and give the
  // session back. Declared after the effect above, so its listeners are gone.
  useEffect(
    () => () => {
      stop();
      audio.load();
      releaseSession();
    },
    [audio, stop],
  );

  async function keep() {
    const i = infoRef.current;
    const tg = targetRef.current;
    if (!i || !tg) return;
    setKeepErr("");
    try {
      const res = await api.keepPreview(i.id, tg.keepTo);
      if (targetRef.current !== tg) return;
      stop();
      releaseSession();
      setKept({ to: tg.keepTo, trackId: res.job?.track_id ?? undefined });
    } catch (e) {
      if (targetRef.current === tg) setKeepErr(previewErrorText(e));
    }
  }

  const value = useMemo(() => ({ open, close }), [open, close]);
  const total = info?.duration_s || target?.durationS || 0;
  // A merged video (YouTube had no format 18) plays once complete: its progress until then, not a player.
  const waitingMerged = info?.status === "downloading" && info.merged;
  return (
    <Ctx.Provider value={value}>
      {children}
      {target && (
        <div className="preview-sheet" role="dialog" aria-label={target.title} data-no-music-prime="">
          <div className="preview-head">
            <span className="yt-text">
              <span className="ellipsis">{target.title}</span>
              <span className="ellipsis muted small">{target.channel}</span>
            </span>
            <button className="icon" aria-label={t("preview.close")} onClick={close}>✕</button>
          </div>
          {problem && (
            <div className="preview-problem">
              <p className="error small" role="alert">{problem.text}</p>
              {problem.offer === "retry" && (
                <button className="secondary" onClick={() => open(target)}>{t("common.retry")}</button>
              )}
              {problem.offer === "audio" && target.listenInstead && (
                <button
                  className="secondary"
                  onClick={() => {
                    const listen = target.listenInstead!;
                    close();
                    listen();
                  }}
                >
                  ▶ {t("episodes.listen")}
                </button>
              )}
              {problem.offer === "audio" && !target.listenInstead && (
                <button className="secondary" onClick={() => open({ ...target, media: "audio" })}>▶ {t("preview.listen")}</button>
              )}
            </div>
          )}
          {!info && !problem && <p className="muted small">{t("preview.preparing")}</p>}
          {info && !problem && target.media === "video" && !kept && waitingMerged && (
            <p className="muted small">{t("video.preparingPercent", { percent: Math.round(info.progress) })}</p>
          )}
          {info && !problem && target.media === "video" && !kept && !waitingMerged && (
            <video
              ref={video}
              className="preview-video"
              src={info.stream_url}
              controls
              playsInline
              onPlay={(e) => {
                claimSession("preview");
                ownLockScreen(e.currentTarget);
              }}
              onLoadedMetadata={(e) => {
                if (seekTo.current > 0) e.currentTarget.currentTime = seekTo.current;
                seekTo.current = 0;
              }}
              onError={(e) => onMediaError(e.currentTarget)}
            />
          )}
          {info && !problem && target.media === "audio" && !kept && (
            <div className="preview-controls">
              <button
                className="icon"
                aria-label={playing ? t("common.pause") : t("common.play")}
                onClick={() => {
                  if (!audio.paused) return audio.pause();
                  claimSession("preview");
                  // A broken stream: this tap reloads it (from where it broke) and plays.
                  if (broken.current && infoRef.current) reload(infoRef.current);
                  else audio.play()?.catch(() => {});
                }}
              >
                {playing ? "⏸" : "▶"}
              </button>
              <input type="range" aria-label={t("preview.position")} min={0} max={total} step={1} value={Math.min(pos, total)}
                onChange={(e) => (audio.currentTime = Number(e.target.value))} />
              <span className="muted small">{duration(pos * 1000)}</span>
            </div>
          )}
          {info && info.status !== "failed" && !kept && (
            <button className="secondary primary" disabled={info.status !== "done"} onClick={keep}>
              {target.keepTo === "music" ? t("preview.keepMusic") : t("preview.keepChannel")}
            </button>
          )}
          {keepErr && <p className="error small" role="alert">{keepErr}</p>}
          {kept?.to === "music" && (
            <p className="muted small">
              {t("preview.keptMusic")}{" "}
              {kept.trackId !== undefined && (
                <button className="secondary" onClick={() => dl.playTrack(kept.trackId!).catch(() => {})}>▶ {t("common.play")}</button>
              )}
            </p>
          )}
          {kept?.to === "channel" && <p className="muted small">{t("preview.keptChannel")}</p>}
        </div>
      )}
    </Ctx.Provider>
  );
}
