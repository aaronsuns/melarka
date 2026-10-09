import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { api } from "../../api/client";
import type { ChannelSuggestions } from "../../api/types";
import { useVisiblePoll } from "../../channels/useVisiblePoll";
import { ChannelDownloadButton } from "../../components/ChannelDownloadButton";
import { Cover } from "../../components/Cover";
import { FollowButton } from "../../components/FollowButton";
import { CloseIcon } from "../../components/icons";
import { PreviewButton } from "../../components/PreviewButton";
import { duration, isSafeThumbnailUrl } from "../../format";
import { errorMessage } from "../../i18n/errors";
import { useT } from "../../i18n/i18n";

const POLL_MS = 5000;

/**
 * 推荐: channels and videos found through the YouTube Mixes of the user's
 * channels. 刷新 starts a refresh, or joins the one already running; while
 * one runs the list is asked for again every 5 s, only while the page is
 * visible. 429 too_soon means today's refresh budget is used up.
 */
export function Suggestions() {
  const t = useT();
  const [data, setData] = useState<ChannelSuggestions | null>(null);
  const [err, setErr] = useState("");
  // A follow or 下载 that failed, shown in that row's text (the actions never grow).
  const [rowErr, setRowErr] = useState<Record<string, string>>({});
  const setRowError = useCallback((id: string, msg: string) => setRowErr((m) => ({ ...m, [id]: msg })), []);
  const dataRef = useRef(data);
  dataRef.current = data;
  const asked = useRef(false);
  const live = useRef(true);
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
    };
  }, []);
  const load = useCallback(() => {
    asked.current = true;
    api.channelSuggestions().then(
      (d) => live.current && setData(d),
      (e) => live.current && setErr(errorMessage(e, "common.loadFailed")),
    );
  }, []);
  // The first read, then again only while a refresh runs.
  useVisiblePoll(() => {
    if (!asked.current || dataRef.current?.refreshing) load();
  }, POLL_MS);
  async function refresh() {
    setErr("");
    try {
      await api.refreshChannelSuggestions();
      if (!live.current) return;
      setData((d) => d && { ...d, refreshing: true });
    } catch (e) {
      if (live.current) setErr(errorMessage(e, "common.actionFailed"));
    }
  }
  // 不感兴趣 removes the row at once, and puts it back where it was if the server refuses.
  function dismiss(kind: "channels" | "videos", id: string) {
    setErr("");
    const d = dataRef.current;
    if (!d) return;
    const list = kind === "channels" ? d.channels : d.videos;
    const index = list.findIndex((x) => ("id" in x ? x.id : x.video_id) === id);
    if (index < 0) return;
    const item = list[index];
    setData((cur) => cur && (kind === "channels" ? { ...cur, channels: cur.channels.filter((c) => c.id !== id) } : { ...cur, videos: cur.videos.filter((v) => v.video_id !== id) }));
    api.dismissSuggestion(kind, id).catch((e) => {
      if (!live.current) return;
      setErr(errorMessage(e, "common.actionFailed"));
      setData((cur) => {
        if (!cur) return cur;
        const back = <T,>(xs: T[]): T[] => {
          if (xs.includes(item as T)) return xs;
          const next = [...xs];
          next.splice(Math.min(index, next.length), 0, item as T);
          return next;
        };
        return kind === "channels" ? { ...cur, channels: back(cur.channels) } : { ...cur, videos: back(cur.videos) };
      });
    });
  }
  return (
    <>
      <div className="episode-actions">
        <button className="secondary compact" onClick={refresh} disabled={!data || data.refreshing}>{data?.refreshing ? t("channels.refreshing") : t("channels.refresh")}</button>
      </div>
      {err && <p className="error small" role="alert">{err}</p>}
      {!data && !err && <p className="muted">{t("common.loading")}</p>}
      {data && data.channels.length === 0 && data.videos.length === 0 && !data.refreshing && <p className="muted">{t("channels.noSuggestions")}</p>}
      {data && data.channels.length > 0 && (
        <>
          <h2 className="section-title">{t("channels.suggestedChannels")}</h2>
          <ul className="rows recs">
            {data.channels.map((c) => (
              <li key={c.id} className="row suggested-channel">
                <Cover seed={c.id} label={c.title} size={48} src={isSafeThumbnailUrl(c.avatar) ? c.avatar : undefined} noReferrer />
                <Link to={`/channels/${c.id}`} className="yt-text">
                  <span className="ellipsis">{c.title}</span>
                  <span className="ellipsis muted small">{t("channels.pointedBy", { count: c.score })}</span>
                  {rowErr[c.id] && <span className="ellipsis small row-error" role="alert" title={rowErr[c.id]}>{rowErr[c.id]}</span>}
                </Link>
                <span className="job-actions row-actions">
                  <FollowButton channelId={c.id} following={false} onError={(m) => setRowError(c.id, m)} />
                  <button className="icon-btn plain" aria-label={t("channels.notInterested")} title={t("channels.notInterested")} onClick={() => dismiss("channels", c.id)}><CloseIcon /></button>
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
      {data && data.videos.length > 0 && (
        <>
          <h2 className="section-title">{t("channels.suggestedVideos")}</h2>
          <ul className="rows yt-results recs">
            {data.videos.map((v) => {
              const target = { videoId: v.video_id, title: v.title, channel: v.channel, durationS: v.duration_s };
              return (
                <li key={v.video_id} className="row">
                  <Cover seed={v.video_id} label={v.title} size={48} src={isSafeThumbnailUrl(v.thumbnail) ? v.thumbnail : undefined} noReferrer />
                  <span className="yt-text">
                    <span className="row-title">{v.title}</span>
                    {rowErr[v.video_id] ? (
                      <span className="ellipsis small row-error" role="alert" title={rowErr[v.video_id]}>{rowErr[v.video_id]}</span>
                    ) : (
                      <span className="ellipsis muted small">{v.channel} · {duration(v.duration_s * 1000)}</span>
                    )}
                  </span>
                  <span className="job-actions row-actions">
                    <PreviewButton icon target={{ ...target, media: "audio", keepTo: "channel" }} />
                    <ChannelDownloadButton target={target} onError={(m) => setRowError(v.video_id, m)} />
                    <button className="icon-btn plain" aria-label={t("channels.notInterested")} title={t("channels.notInterested")} onClick={() => dismiss("videos", v.video_id)}><CloseIcon /></button>
                  </span>
                </li>
              );
            })}
          </ul>
        </>
      )}
    </>
  );
}
