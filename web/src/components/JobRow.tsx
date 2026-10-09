import type { DownloadJob } from "../api/types";
import { isSafeThumbnailUrl } from "../format";
import { jobErrorShort } from "../i18n/errors";
import { isActiveJob } from "../downloads/useJobPoll";
import { t, useT } from "../i18n/i18n";
import { Cover } from "./Cover";

export const isActive = isActiveJob;

export function statusLabel(j: DownloadJob): string {
  switch (j.status) {
    case "queued":
      return t("job.queued");
    case "downloading":
      return t("job.downloading", { pct: Math.round(j.progress) });
    case "done":
      return t("job.done");
    case "failed":
      return t("job.failed");
    case "cancelled":
      return t("job.cancelled");
  }
}

interface Props {
  job: DownloadJob;
  busy: boolean;
  rowError?: string;
  // Shown on the admin "all downloads" view, where several users' jobs are
  // mixed together; the personal downloads page omits it (it's always you).
  showUsername?: boolean;
  highlight?: boolean;
  onCancel?: () => void;
  onRetry?: () => void;
  onRemove?: () => void;
  onPlay?: () => void;
}

export function JobRow({ job, busy, rowError, showUsername, highlight, onCancel, onRetry, onRemove, onPlay }: Props) {
  const t = useT();
  return (
    <li className={highlight ? "row job-highlight" : "row"} id={`job-${job.id}`}>
      {isSafeThumbnailUrl(job.thumbnail) ? (
        <img src={job.thumbnail} alt="" width={48} height={48} loading="lazy" referrerPolicy="no-referrer" className="yt-thumb" />
      ) : (
        <Cover seed={job.video_id} label={job.title} size={48} />
      )}
      <span className="yt-text">
        <span className="ellipsis">{job.title}</span>
        <span className="ellipsis muted small">
          {showUsername ? `${job.channel} · ${job.username || t("job.unknownUser")}` : job.channel}
        </span>
        {job.status === "failed" && job.error ? (
          // Short and translated; yt-dlp's raw line only as the tooltip.
          <span className="ellipsis small row-error" title={job.error}>{jobErrorShort(job.error)}</span>
        ) : (
          <span className="muted small">{statusLabel(job)}</span>
        )}
        {rowError && <span className="error small">{rowError}</span>}
      </span>
      <span className="job-actions">
        {onCancel && isActive(job) && (
          <button className="secondary" disabled={busy} onClick={onCancel}>{t("common.cancel")}</button>
        )}
        {onRetry && (job.status === "failed" || job.status === "cancelled") && (
          <button className="secondary" disabled={busy} onClick={onRetry}>{t("common.retry")}</button>
        )}
        {onPlay && job.status === "done" && job.track_id != null && job.track_available && (
          <button className="secondary" onClick={onPlay}>{t("common.play")}</button>
        )}
        {onRemove && (job.status === "failed" || job.status === "cancelled") && (
          <button className="secondary" disabled={busy} onClick={onRemove}>{t("downloads.remove")}</button>
        )}
      </span>
    </li>
  );
}
