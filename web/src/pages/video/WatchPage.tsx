import { useEffect, useRef, useState } from "react";
import { Link, useLocation, useParams } from "react-router";
import { api, ApiError } from "../../api/client";
import type { PreviewInfo, YTVideo } from "../../api/types";
import { PREVIEW_POLL_MS, usePreviews } from "../../channels/PreviewProvider";
import { useVisiblePoll } from "../../channels/useVisiblePoll";
import { Cover } from "../../components/Cover";
import { FollowButton } from "../../components/FollowButton";
import { VideoCard, videoThumbnail } from "../../components/VideoCard";
import { duration } from "../../format";
import { errorMessage, failedPreviewText, isTransient, previewErrorText } from "../../i18n/errors";
import { has, useT } from "../../i18n/i18n";
import { useSessionVideo } from "../../player/useSessionVideo";

const SHORT_LINES = 3;
// A description longer than this folds too, even as one paragraph (CSS clamps it to 3 lines).
const SHORT_CHARS = 160;

// The 720p preview: on its way (progress), done, or failed with why (note);
// unavailable: there is no 720p, so 高清 stays off. info: its row (title, channel).
type Hd = { id: number; status: "downloading" | "done" | "failed"; progress: number; queued: boolean; stream_url: string; note: string; unavailable: boolean; info: PreviewInfo | null };
const HD_STARTING: Hd = { id: 0, status: "downloading", progress: 0, queued: false, stream_url: "", note: "", unavailable: false, info: null };

// Why the 360p can't be watched; hdInstead: it can't be streamed while it
// downloads, 高清 is the start; retry: worth asking again (重试).
type StartProblem = { text: string; hdInstead: boolean; retry: boolean };

/**
 * /watch/:id (§18.2): one YouTube video through the preview machinery. The
 * 360p starts at once and plays while it downloads; 高清 fetches the 720p
 * beside it and switches at the same second when it is done. ▶ 听 goes on as
 * an audio preview (lock screen, background), 保留 keeps the file in
 * Channels. Title, channel and description are YouTube's text: text only.
 * There is no resume position for 视频.
 */
export default function WatchPage() {
  const t = useT();
  const { id = "" } = useParams();
  const location = useLocation();
  const previews = usePreviews();
  // The card the user tapped (search, 推荐, 相关): what is known before the preview answers.
  const passed = (location.state as { video?: YTVideo } | null)?.video;
  const card = passed && passed.id === id ? passed : undefined;
  const cardRef = useRef(card);
  cardRef.current = card;

  const [info, setInfo] = useState<PreviewInfo | null>(null); // the 360p preview
  const [problem, setProblem] = useState<StartProblem | null>(null);
  const [src, setSrc] = useState<string | null>(null);
  const [autoPlay, setAutoPlay] = useState(true);
  const [hd, setHd] = useState<Hd | null>(null);
  const [hdOn, setHdOn] = useState(false);
  const [offerAt, setOfferAt] = useState<number | null>(null);
  const [brokenAt, setBrokenAt] = useState<number | null>(null); // the stream broke at this second (重试)
  const [keeping, setKeeping] = useState(false);
  // The episode it became, and from which preview (a 360p keep is offered again once 高清 is done: the server upgrades it).
  const [kept, setKept] = useState<{ episode: string; from: number } | null>(null);
  const [actionErr, setActionErr] = useState("");
  const [more, setMore] = useState(false);
  const [related, setRelated] = useState<YTVideo[] | null>(null);
  const [relatedErr, setRelatedErr] = useState("");
  const [following, setFollowing] = useState<Set<string>>(() => new Set());
  const video = useRef<HTMLVideoElement>(null);
  // Bumped for every video shown: answers for an earlier one are dropped.
  const gen = useRef(0);
  // The HD switch: where the 360p was, and whether it was playing.
  const switchAt = useRef<{ second: number; play: boolean } | null>(null);
  const recorded = useRef(false);

  const cur = info && info.video_id === id ? info : null;
  // The HD row knows the title too (a shared link whose 360p can't stream).
  const hdInfo = hd?.info && hd.info.video_id === id ? hd.info : null;
  const title = cur?.title || hdInfo?.title || card?.title || "";
  const channel = cur?.channel || hdInfo?.channel || card?.channel || "";
  const channelId = cur?.channel_id || hdInfo?.channel_id || card?.channel_id || "";
  const durationS = cur?.duration_s || hdInfo?.duration_s || card?.duration_s || 0;
  const thumbnail = videoThumbnail(id);
  const meta = useRef({ title, channel, thumbnail });
  meta.current = { title, channel, thumbnail };

  const sv = useSessionVideo(video, src, {
    metadata: () => {
      const m = meta.current;
      if (!m.title) return null;
      return {
        title: m.title,
        artist: m.channel,
        album: t("nav.video"),
        artwork: [{ src: new URL(m.thumbnail, window.location.href).href, sizes: "320x180", type: "image/jpeg" }],
      };
    },
    onBackgroundStop: (second) => setOfferAt(second),
  });

  // Takes a 360p status: it plays as soon as there is one, even while it
  // downloads — unless it is merged (YouTube had no format 18): that plays
  // once complete, 准备中 N% until then (a stream already asked for is let go).
  function take360(p: PreviewInfo) {
    setInfo(p);
    if (p.status === "failed") {
      const hdInstead = p.error === "video_preview_unavailable";
      setProblem({ text: hdInstead ? t("video.hdInstead") : failedPreviewText(p.error), hdInstead, retry: !hdInstead });
      return;
    }
    setProblem(null);
    if (p.status === "downloading" && p.merged) setSrc((s) => (s === p.stream_url ? null : s));
    else setSrc((s) => s ?? p.stream_url);
  }

  // Starts (or, from 重试, starts again) the 360p preview.
  function start() {
    const g = gen.current;
    const c = cardRef.current;
    setProblem(null);
    api.startPreview({ video_id: id, media: "video", title: c?.title, channel: c?.channel, duration_s: c?.duration_s }).then(
      (p) => g === gen.current && take360(p),
      (e) => {
        if (g !== gen.current) return;
        const hdInstead = e instanceof ApiError && e.code === "video_preview_unavailable";
        setProblem({ text: hdInstead ? t("video.hdInstead") : previewErrorText(e), hdInstead, retry: !hdInstead });
      },
    );
  }

  useEffect(() => {
    // Another video on the same page: nothing of the last one carries over.
    const g = ++gen.current;
    video.current?.pause();
    sv.release();
    setInfo(null);
    setProblem(null);
    setSrc(null);
    setAutoPlay(true);
    setHd(null);
    setHdOn(false);
    setOfferAt(null);
    setBrokenAt(null);
    setKeeping(false);
    setKept(null);
    setActionErr("");
    setMore(false);
    setRelated(null);
    setRelatedErr("");
    switchAt.current = null;
    recorded.current = false;
    start();
    api.videoRelated(id).then(
      (r) => g === gen.current && setRelated(r.videos ?? []),
      (e) => g === gen.current && setRelatedErr(errorMessage(e, "common.loadFailed")),
    );
  }, [id]);

  // 关注频道 needs to know what the user already follows: asked once.
  useEffect(() => {
    api.myChannels().then(
      (r) => setFollowing(new Set((r.channels ?? []).map((c) => c.channel.id))),
      () => {},
    );
  }, []);

  // The watch goes into 最近观看 (and seeds 为你推荐) once its title is known.
  useEffect(() => {
    if (recorded.current || !title) return;
    recorded.current = true;
    api.recordWatch({ video_id: id, title, channel, channel_id: channelId || undefined, duration_s: durationS }).catch(() => {});
  }, [id, title, channel, channelId, durationS]);

  // The 360p's status, until it is done and has its title and description.
  useVisiblePoll(
    () => {
      if (!cur) return;
      const g = gen.current;
      api.preview(cur.id).then(
        (p) => g === gen.current && take360(p),
        () => {},
      );
    },
    PREVIEW_POLL_MS,
    `${id}:${cur?.id}`,
    !!cur && cur.status !== "failed" && !(cur.status === "done" && cur.title),
  );

  // 高清's progress, until it is done (then the switch) or failed.
  useVisiblePoll(
    () => {
      if (!hd || hd.id === 0) return;
      const g = gen.current;
      api.preview(hd.id).then(
        (p) => g === gen.current && takeHd(p),
        (e) => {
          // A blip: the next read. Anything else (the server dropped the
          // download: 503 preview_retry, then 404) ends it; 高清 can be tapped again.
          if (g !== gen.current || isTransient(e)) return;
          setHd((h) => h && { ...h, status: "failed", note: hdErrorText(e) });
        },
      );
    },
    PREVIEW_POLL_MS,
    `${id}:${hd?.id}`,
    !!hd && hd.id > 0 && hd.status === "downloading",
  );

  function takeHd(p: PreviewInfo) {
    const unavailable = p.status === "failed" && p.error === "hd_unavailable";
    let note = "";
    if (p.status === "failed") {
      if (unavailable) note = t("video.hdUnavailable");
      else if (p.error === "preview_queue_timeout") note = t("video.hdQueueTimeout");
      else note = t("video.hdFailed");
    }
    setHd({ id: p.id, status: p.status, progress: p.progress, queued: !!p.queued, stream_url: p.stream_url, note, unavailable, info: p });
    if (p.status === "done") switchToHd(p.stream_url);
  }

  // Why 高清 failed, in words: no 720p, or the server's reason (the limit, no space, YouTube pushing back).
  function hdErrorText(e: unknown): string {
    if (e instanceof ApiError && e.code === "hd_unavailable") return t("video.hdUnavailable");
    if (e instanceof ApiError && e.code && has(`error.${e.code}`)) return previewErrorText(e);
    return t("video.hdFailed");
  }

  // The 720p takes over at the same second; it plays only if the 360p was playing.
  function switchToHd(url: string) {
    const v = video.current;
    if (v && src) {
      switchAt.current = { second: v.currentTime, play: !v.paused };
      setAutoPlay(false);
    } else {
      switchAt.current = null; // nothing was showing: the HD is the start
      setAutoPlay(true);
    }
    setBrokenAt(null);
    setSrc(url);
    setHdOn(true);
  }

  // The stream broke. A 720p goes back to the 360p at the same second (高清
  // can be tapped again); a 360p says so and offers 重试 from there.
  function onVideoError(e: { currentTarget: HTMLVideoElement }) {
    const v = video.current;
    if (!v || e.currentTarget !== v || !src) return;
    const second = v.currentTime;
    if (hdOn && cur && cur.status !== "failed" && cur.stream_url) {
      switchAt.current = { second, play: !v.paused };
      setAutoPlay(false);
      setSrc(cur.stream_url);
      setHdOn(false);
      setHd((h) => h && { ...h, status: "failed", note: t("video.hdFailed") });
      return;
    }
    setBrokenAt(second);
  }

  function retryStream() {
    const v = video.current;
    if (!v || brokenAt === null) return;
    switchAt.current = { second: brokenAt, play: true };
    setBrokenAt(null);
    v.load();
  }

  async function startHd() {
    const g = gen.current;
    setHd(HD_STARTING);
    try {
      const p = await api.startPreview({ video_id: id, media: "hd", title: title || undefined, channel: channel || undefined, duration_s: durationS || undefined });
      if (g === gen.current) takeHd(p);
    } catch (e) {
      if (g !== gen.current) return;
      setHd({ ...HD_STARTING, status: "failed", note: hdErrorText(e), unavailable: e instanceof ApiError && e.code === "hd_unavailable" });
    }
  }

  function onMeta() {
    const v = video.current;
    const s = switchAt.current;
    if (!v || !s) return;
    switchAt.current = null;
    v.currentTime = s.second;
    if (s.play) void v.play()?.catch(() => {});
  }

  // ▶ 听: an audio preview from this second, in the preview sheet (lock screen, background).
  function listen(second: number) {
    sv.pauseOwn();
    setOfferAt(null);
    previews.open({ videoId: id, media: "audio", title, channel, durationS, keepTo: "channel", startAt: second });
  }

  const hdDone = !!hd && hd.status === "done" && hd.id > 0;
  // While 高清 downloads, 保留 waits for it: keeping the 360p then would make the episode 360p.
  const hdBusy = hd?.status === "downloading";
  const keepId = hdDone ? hd.id : cur?.status === "done" ? cur.id : null;
  const showKept = kept !== null && (kept.from === keepId || hdBusy);
  async function keep() {
    if (keepId === null) return;
    const g = gen.current;
    setKeeping(true);
    setActionErr("");
    try {
      const r = await api.keepPreview(keepId, "channel");
      if (g === gen.current) setKept({ episode: r.episode_id ?? id, from: keepId });
    } catch (e) {
      if (g === gen.current) setActionErr(errorMessage(e, "common.actionFailed"));
    } finally {
      if (g === gen.current) setKeeping(false);
    }
  }

  let hdLabel = t("video.hd");
  if (hdOn) hdLabel = t("video.hdOn");
  else if (hd?.status === "downloading" && hd.queued) hdLabel = t("video.hdQueued");
  else if (hd?.status === "downloading") hdLabel = t("video.hdProgress", { percent: Math.round(hd.progress) });
  const hdNote = hd?.status === "failed" ? hd.note : "";
  const desc = (cur?.description || hdInfo?.description || "").trim();
  const lines = desc ? desc.split("\n") : [];
  const folds = lines.length > SHORT_LINES || desc.length > SHORT_CHARS;
  // 360p unavailable and the HD now playing: nothing left to say.
  const showProblem = problem && !(problem.hdInstead && src);

  return (
    <article className="watch-page" data-no-music-prime="">
      {src ? (
        <video
          ref={video}
          className="watch-video"
          src={src}
          controls
          playsInline
          autoPlay={autoPlay}
          onLoadedMetadata={onMeta}
          onError={onVideoError}
          {...sv.handlers}
          onPlay={() => {
            sv.handlers.onPlay();
            setOfferAt(null);
          }}
        />
      ) : (
        <div className="watch-placeholder">
          <div className="video-thumb">
            <Cover seed={channelId || id} label={channel || title || id} size={64} src={thumbnail} />
          </div>
          {!problem && (
            <p className="muted small">
              {cur?.status === "downloading" && cur.merged ? t("video.preparingPercent", { percent: Math.round(cur.progress) }) : t("video.preparing")}
            </p>
          )}
        </div>
      )}
      {showProblem && (
        <div className="notice-strip" role="alert">
          <span className="error small">{problem.text}</span>
          {problem.retry && <button className="secondary" onClick={start}>{t("common.retry")}</button>}
        </div>
      )}
      {brokenAt !== null && (
        <div className="notice-strip" role="alert">
          <span className="error small">{t("video.streamBroken")}</span>
          <button className="secondary" onClick={retryStream}>{t("common.retry")}</button>
        </div>
      )}
      {title && <h1 className="watch-title">{title}</h1>}
      <div className="watch-channel">
        <span className="muted small ellipsis">
          {channelId ? <Link to={`/channels/${channelId}`}>{channel}</Link> : channel}
          {durationS > 0 && ` · ${duration(durationS * 1000)}`}
        </span>
        {channelId && (
          <FollowButton
            key={channelId}
            channelId={channelId}
            following={following.has(channelId)}
            label={t("channels.followChannel")}
            onChange={(on) =>
              setFollowing((s) => {
                const next = new Set(s);
                if (on) next.add(channelId);
                else next.delete(channelId);
                return next;
              })
            }
          />
        )}
      </div>
      {offerAt !== null && (
        <div className="notice-strip" role="status">
          <span>{t("video.stoppedInBackground")}</span>
          <button className="secondary" onClick={() => listen(offerAt)}>{t("video.continueAsAudio")}</button>
        </div>
      )}
      <div className="episode-actions">
        <button
          className={problem?.hdInstead && !hd ? "secondary primary" : "secondary"}
          aria-pressed={hdOn}
          disabled={hdOn || hd?.status === "downloading" || hd?.unavailable === true}
          onClick={startHd}
        >
          {hdLabel}
        </button>
        <button className="secondary" onClick={() => listen(video.current && !video.current.ended ? video.current.currentTime : 0)}>▶ {t("video.listen")}</button>
        {showKept ? (
          <Link className="secondary watch-kept" to={`/episodes/${encodeURIComponent(kept.episode)}`}>{t("video.kept")}</Link>
        ) : (
          <button className="secondary" disabled={keepId === null || keeping || hdBusy} onClick={keep}>{t("video.keep")}</button>
        )}
      </div>
      {hdNote && <p className="muted small">{hdNote}</p>}
      {actionErr && <p className="error small" role="alert">{actionErr}</p>}
      {lines.length > 0 && (
        <div className="episode-description">
          <p className={folds && !more ? "pre-line clamp-3" : "pre-line"}>{(more ? lines : lines.slice(0, SHORT_LINES)).join("\n")}</p>
          {folds && (
            <button className="link" onClick={() => setMore(!more)}>{more ? t("video.less") : t("video.more")}</button>
          )}
        </div>
      )}
      {(related === null || related.length > 0 || relatedErr) && <h2 className="section-title">{t("video.related")}</h2>}
      {relatedErr && <p className="muted small">{relatedErr}</p>}
      {related === null && !relatedErr && <p className="muted small">{t("common.loading")}</p>}
      {related && related.length > 0 && (
        <div className="video-grid">
          {related.map((v) => <VideoCard key={v.id} v={v} />)}
        </div>
      )}
    </article>
  );
}
