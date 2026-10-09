import { useEffect, useState } from "react";
import { Link } from "react-router";
import { errorMessage, jobErrorShort } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { useDownloads } from "../downloads/DownloadsProvider";
import { isActiveJob } from "../downloads/useJobPoll";
import { CheckIcon, PlayIcon, ProgressRing, RetryIcon } from "./icons";
import { statusLabel } from "./JobRow";
import { useReportRowError, type OnRowError } from "./rowError";

/**
 * The live state of a download this session started, in the place the 下载
 * button was: status link while it runs, ▶ 播放 once it's done, error + 重试
 * if it failed.
 *
 * `compact` (result rows): a round icon instead — a progress ring linking to
 * the job while it runs, ▶ once done, ↻ to retry — and the error goes to the
 * row's text through `onError` (short and translated; yt-dlp's raw line only
 * as its tooltip).
 */
export function DownloadStatus({ jobId, compact, onError }: { jobId: number; compact?: boolean; onError?: OnRowError }) {
  const t = useT();
  const dl = useDownloads();
  const job = dl.jobs[jobId];
  const [err, setErr] = useState("");
  const playable = job?.status === "done" && job.track_id != null && job.track_available;
  const { rowShowing } = dl;
  useEffect(() => (playable ? rowShowing(jobId) : undefined), [playable, rowShowing, jobId]);
  const ended = !!job && !isActiveJob(job) && job.status !== "done";
  const errText = err || (ended ? (job.status === "failed" && job.error ? jobErrorShort(job.error) : statusLabel(job)) : "");
  const errDetail = ended && job.status === "failed" ? job.error : "";
  useReportRowError(compact ? onError : undefined, errText, errDetail);
  if (!job) return null;

  if (isActiveJob(job)) {
    const label = statusLabel(job);
    if (compact) {
      const pct = job.status === "downloading" ? Math.round(job.progress) : null;
      return (
        <Link className="icon-btn ring-btn" to={`/downloads?job=${job.id}`} title={label}>
          <ProgressRing pct={pct} />
          {pct !== null && <span className="ring-pct" aria-hidden="true">{pct}</span>}
          <span className="sr-only">{label}</span>
        </Link>
      );
    }
    return <Link className="secondary status-link" to={`/downloads?job=${job.id}`}>{label}</Link>;
  }
  const play = () => {
    setErr("");
    dl.playTrack(job.track_id!).catch(() => setErr(t("downloads.trackGone")));
  };
  const retry = () => {
    setErr("");
    dl.retry(job.id).catch((e) => setErr(errorMessage(e, "downloads.retryFailed")));
  };
  if (compact) {
    if (job.status === "done") {
      return playable ? (
        <button className="icon-btn filled" aria-label={t("common.play")} title={t("common.play")} onClick={play}><PlayIcon /></button>
      ) : (
        <button className="icon-btn state" disabled aria-label={t("yt.alreadyInLibrary")} title={t("yt.alreadyInLibrary")}><CheckIcon /></button>
      );
    }
    return <button className="icon-btn outline" aria-label={t("common.retry")} title={t("common.retry")} onClick={retry}><RetryIcon /></button>;
  }
  if (job.status === "done") {
    if (playable) {
      return (
        <span className="job-actions">
          {err && <span className="error small">{err}</span>}
          <button className="secondary" onClick={play}>
            <span aria-hidden="true">▶</span> {t("common.play")}
          </button>
        </span>
      );
    }
    return <button className="secondary" disabled>{t("yt.alreadyInLibrary")}</button>;
  }
  return (
    <span className="job-actions">
      <span className="error small" title={errDetail || undefined}>{errText}</span>
      <button className="secondary" onClick={retry}>{t("common.retry")}</button>
    </span>
  );
}
