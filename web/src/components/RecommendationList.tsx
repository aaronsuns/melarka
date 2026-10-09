import { useState } from "react";
import { api } from "../api/client";
import type { Recommendation } from "../api/types";
import { duration, isSafeThumbnailUrl } from "../format";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { Cover } from "./Cover";
import { CloseIcon } from "./icons";
import { PreviewButton } from "./PreviewButton";
import { RowErrorLine, useRowErrors } from "./rowError";
import { VideoDownload } from "./VideoDownload";

/**
 * "为你推荐" rows: thumbnail, title, channel, why it was picked, 下载 (live
 * status → ▶ 播放, like Search) and 不感兴趣 — which removes the row at once
 * and puts it back with an error if the server refuses.
 */
export function RecommendationList({ items, onChange }: { items: Recommendation[]; onChange: (next: (cur: Recommendation[]) => Recommendation[]) => void }) {
  const t = useT();
  const [err, setErr] = useState("");
  const [rowErr, setRowError] = useRowErrors();

  function dismiss(r: Recommendation) {
    setErr("");
    let index = -1;
    onChange((cur) => {
      index = cur.findIndex((x) => x.video_id === r.video_id);
      return cur.filter((x) => x.video_id !== r.video_id);
    });
    api.dismissRecommendation(r.video_id).catch((e) => {
      setErr(errorMessage(e, "recs.dismissFailed"));
      onChange((cur) => {
        if (cur.some((x) => x.video_id === r.video_id)) return cur;
        const next = [...cur];
        next.splice(index < 0 ? next.length : Math.min(index, next.length), 0, r);
        return next;
      });
    });
  }

  return (
    <>
      {err && <p className="error small">{err}</p>}
      <ul className="rows yt-results recs">
        {items.map((r) => (
          <li key={r.video_id} className="row">
            <Cover seed={r.video_id} label={r.title} size={48} src={isSafeThumbnailUrl(r.thumbnail) ? r.thumbnail : undefined} noReferrer />
            <span className="yt-text">
              <span className="row-title">{r.title}</span>
              <span className="ellipsis muted small">{r.channel} · {duration(r.duration_s * 1000)}</span>
              {rowErr[r.video_id] ? (
                <RowErrorLine error={rowErr[r.video_id]} />
              ) : (
                r.reason && (
                  <span className="ellipsis muted small">
                    {t(r.reason.kind === "favorite" ? "recs.becauseFavorited" : "recs.becausePlayed", { title: r.reason.title })}
                  </span>
                )
              )}
            </span>
            <span className="job-actions row-actions">
              <PreviewButton icon target={{ videoId: r.video_id, media: "audio", title: r.title, channel: r.channel, durationS: r.duration_s, keepTo: "music" }} />
              <VideoDownload video={{ id: r.video_id, title: r.title, channel: r.channel, url: r.url, thumbnail: r.thumbnail, duration_s: r.duration_s }} onError={(e) => setRowError(r.video_id, e)} />
              <button className="icon-btn plain" aria-label={t("recs.dismiss")} title={t("recs.dismiss")} onClick={() => dismiss(r)}><CloseIcon /></button>
            </span>
          </li>
        ))}
      </ul>
    </>
  );
}
