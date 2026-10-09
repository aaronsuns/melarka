import { useEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router";
import { api, episodeStreamUrl } from "../../api/client";
import type { Episode } from "../../api/types";
import { EPISODE_RATES, resumeAt, useEpisodes } from "../../channels/EpisodesProvider";
import { Cover } from "../../components/Cover";
import { PreviewButton } from "../../components/PreviewButton";
import { duration, formatDate } from "../../format";
import { errorMessage } from "../../i18n/errors";
import { useT } from "../../i18n/i18n";
import { useSessionVideo } from "../../player/useSessionVideo";
import { episodeStatus } from "./EpisodeList";

const SHORT_LINES = 3;
// Watching saves the position like listening: every 15 s while playing, on pause and on page hide.
const SAVE_EVERY_MS = 15_000;

export default function EpisodePage() {
  const t = useT();
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const routerLocation = useLocation();
  const eps = useEpisodes();
  const [ep, setEp] = useState<Episode | null>(null);
  const [err, setErr] = useState(""); // the episode couldn't be loaded
  const [actionErr, setActionErr] = useState(""); // a button's request failed (the page, and a playing video, stay)
  const [more, setMore] = useState(false);
  const [videoSrc, setVideoSrc] = useState<string | null>(null);
  const [offerAt, setOfferAt] = useState<number | null>(null);
  const video = useRef<HTMLVideoElement>(null);
  const startAt = useRef(0);
  const lastSave = useRef(0);
  const epRef = useRef(ep);
  epRef.current = ep;
  const epsRef = useRef(eps);
  epsRef.current = eps;
  // The video owns the session while it plays; the page keeps its place.
  const sv = useSessionVideo(video, videoSrc, {
    metadata: () => {
      const e = epRef.current;
      if (!e) return null;
      return {
        title: e.title,
        artist: e.channel_title,
        album: t("channels.title"),
        artwork: [{ src: new URL(e.thumbnail, window.location.href).href, sizes: "480x360", type: "image/jpeg" }],
      };
    },
    onPause: ({ hidden, hideSaved }) => {
      if (!(hidden && hideSaved)) saveRef.current();
    },
    onHideWhilePlaying: () => saveRef.current({ keepalive: true }),
    onBackgroundStop: (second) => goOnRef.current(second),
  });

  useEffect(() => {
    // Another episode on the same page: nothing of the last one carries over.
    video.current?.pause();
    sv.release();
    setEp(null);
    setErr("");
    setActionErr("");
    setVideoSrc(null);
    setOfferAt(null);
    setMore(false);
    let live = true;
    api.episode(id).then((e) => live && setEp(e), (e) => live && setErr(errorMessage(e, "common.loadFailed")));
    return () => {
      live = false;
    };
  }, [id]);

  // Saves where the video is, and keeps the page's copy so ▶ 听 resumes from it.
  function saveVideo(opts: { played?: boolean; keepalive?: boolean } = {}) {
    const v = video.current;
    const e = epRef.current;
    if (!v || !e) return;
    lastSave.current = Date.now();
    if (opts.keepalive !== true) sv.saved();
    const position_s = opts.played ? 0 : Math.floor(v.currentTime);
    const body = opts.played ? { position_s: 0, played: true } : { position_s };
    api.episodeProgress(e.video_id, body, { keepalive: opts.keepalive }).catch(() => {});
    setEp((c) => (c && c.video_id === e.video_id ? { ...c, position_s, played: opts.played ? true : c.played } : c));
  }
  const saveRef = useRef(saveVideo);
  saveRef.current = saveVideo;

  // The page going to the background stopped the video: go on as audio from
  // that second (may be refused there; the offer stays for the return).
  function goOnAsAudio(second: number) {
    const e = epRef.current;
    if (!e) return;
    setOfferAt(second);
    epsRef.current.play([e], 0, { startAt: second });
  }
  const goOnRef = useRef(goOnAsAudio);
  goOnRef.current = goOnAsAudio;

  // The audio got going (in the background, or from the offer): the offer is done.
  useEffect(() => {
    if (offerAt !== null && eps.playing) setOfferAt(null);
  }, [offerAt, eps.playing]);

  if (err) return <p className="error">{err}</p>;
  if (!ep) return <p className="muted">{t("common.loading")}</p>;
  const cur = ep;
  const status = episodeStatus(cur, t);
  const at = resumeAt(cur);
  const lines = (cur.description ?? "").split("\n");

  function listen(from?: number) {
    const v = video.current;
    // From the video's second when it is showing (not when it ended); else the saved position.
    const second = from ?? (v && !v.ended && v.currentTime > 0 ? v.currentTime : undefined);
    setVideoSrc(null);
    setOfferAt(null);
    eps.play([cur], 0, second !== undefined ? { startAt: second } : {});
  }
  function watch() {
    startAt.current = resumeAt(cur);
    setOfferAt(null);
    setVideoSrc(episodeStreamUrl(cur.video_id, "video"));
  }
  function onVideoMeta() {
    const v = video.current;
    if (!v) return;
    if (startAt.current > 0) v.currentTime = startAt.current;
    startAt.current = 0;
    v.playbackRate = eps.rate;
  }
  function onVideoTime() {
    if (sv.playing() && Date.now() - lastSave.current >= SAVE_EVERY_MS) saveVideo();
  }
  function onVideoEnded() {
    saveVideo({ played: true });
  }
  async function toggleKeep() {
    setActionErr("");
    try {
      await api.setEpisodeKeep(cur.video_id, !cur.kept);
      setEp({ ...cur, kept: !cur.kept });
    } catch (e) {
      setActionErr(errorMessage(e, "common.actionFailed"));
    }
  }
  async function togglePlayed() {
    const played = !cur.played;
    setActionErr("");
    try {
      await api.episodeProgress(cur.video_id, { position_s: played ? 0 : cur.position_s, played });
      setEp({ ...cur, played, position_s: played ? 0 : cur.position_s });
    } catch (e) {
      setActionErr(errorMessage(e, "common.actionFailed"));
    }
  }
  async function hide() {
    setActionErr("");
    try {
      await api.setEpisodeHidden(cur.video_id, true);
      // Opened directly (a shared link, a reload): there is no page to go back to.
      if (routerLocation.key === "default") navigate("/channels", { replace: true });
      else navigate(-1);
    } catch (e) {
      setActionErr(errorMessage(e, "common.actionFailed"));
    }
  }
  return (
    <article className="episode-page" data-no-music-prime="">
      {videoSrc ? (
        <video
          ref={video}
          className="episode-video"
          src={videoSrc}
          controls
          playsInline
          autoPlay
          onLoadedMetadata={onVideoMeta}
          {...sv.handlers}
          onPlay={() => {
            sv.handlers.onPlay();
            lastSave.current = Date.now(); // the next save 15 s from here
            setOfferAt(null);
            if (video.current) video.current.playbackRate = eps.rate;
          }}
          onTimeUpdate={onVideoTime}
          onEnded={() => {
            sv.handlers.onEnded();
            onVideoEnded();
          }}
        />
      ) : (
        <Cover seed={cur.channel_id} label={cur.channel_title} size={160} src={cur.thumbnail} />
      )}
      <h1 className="page-title">{cur.title}</h1>
      <p className="muted small">
        <Link to={`/channels/${cur.channel_id}`}>{cur.channel_title}</Link> · {formatDate(cur.published_at)}
        {cur.duration_s > 0 && ` · ${duration(cur.duration_s * 1000)}`}
      </p>
      {offerAt !== null && !eps.playing && (
        <div className="notice-strip" role="status">
          <span>{t("episodes.videoStopped")}</span>
          <button className="secondary" onClick={() => listen(offerAt)}>{t("episodes.continueAsAudio")}</button>
        </div>
      )}
      {status && <p className="muted">{status}</p>}
      <div className="episode-actions">
        {!status && (
          <button className="secondary primary" onClick={() => listen()}>
            {at > 0 ? `▶ ${t("episodes.listen")} · ${t("episodes.resumeAt", { time: duration(at * 1000) })}` : `▶ ${t("episodes.listen")}`}
          </button>
        )}
        {cur.video?.status === "done" && !videoSrc && (
          <button className="secondary" onClick={watch}>▶ {t("episodes.watch")}</button>
        )}
        {status && (
          <PreviewButton target={{ videoId: cur.video_id, media: "audio", title: cur.title, channel: cur.channel_title, durationS: cur.duration_s, keepTo: "channel" }} />
        )}
        {cur.video?.status !== "done" && (
          <PreviewButton
            label={`▶ ${t("preview.watch")}`}
            target={{
              videoId: cur.video_id, media: "video", title: cur.title, channel: cur.channel_title, durationS: cur.duration_s, keepTo: "channel",
              // The audio is on disk: if the video can't be previewed, ▶ 听 it rather than preview it again.
              listenInstead: status ? undefined : () => listen(),
            }}
          />
        )}
        {videoSrc && offerAt === null && (
          <button className="secondary" onClick={() => listen(video.current && !video.current.ended ? video.current.currentTime : 0)}>{t("episodes.continueAsAudio")}</button>
        )}
      </div>
      <div className="segmented" role="group" aria-label={t("episodes.speed")}>
        {EPISODE_RATES.map((r) => (
          <button
            key={r}
            aria-pressed={eps.rate === r}
            onClick={() => {
              eps.setRate(r);
              if (video.current) video.current.playbackRate = r;
            }}
          >
            {r}×
          </button>
        ))}
      </div>
      <div className="episode-actions">
        <button className="secondary" aria-pressed={cur.kept} onClick={toggleKeep}>{cur.kept ? t("episodes.unkeep") : t("episodes.keep")}</button>
        <button className="secondary" onClick={togglePlayed}>{cur.played ? t("episodes.markUnplayed") : t("episodes.markPlayed")}</button>
        <button className="secondary" onClick={hide}>{t("episodes.hide")}</button>
      </div>
      {actionErr && <p className="error small" role="alert">{actionErr}</p>}
      {lines.length > 0 && lines[0] !== "" && (
        <div className="episode-description">
          <p className="pre-line">{(more ? lines : lines.slice(0, SHORT_LINES)).join("\n")}</p>
          {lines.length > SHORT_LINES && (
            <button className="link" onClick={() => setMore(!more)}>{more ? t("episodes.less") : t("episodes.more")}</button>
          )}
        </div>
      )}
    </article>
  );
}
