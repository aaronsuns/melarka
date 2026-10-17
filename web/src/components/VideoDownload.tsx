import { useState } from "react";
import { api } from "../api/client";
import type { DownloadJob, Track, YTVideo } from "../api/types";
import { useAuth } from "../auth/AuthProvider";
import { useDownloads } from "../downloads/DownloadsProvider";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { DownloadStatus } from "./DownloadStatus";
import { CheckIcon, ClockIcon, DownloadIcon, ProgressRing, RetryIcon } from "./icons";
import { useReportRowError, type OnRowError } from "./rowError";

type RowState =
  | { kind: "idle" }
  | { kind: "confirm"; tracks: Track[] } // the library seems to have this song already: ask
  | { kind: "posting" }
  | { kind: "queued"; jobId?: number } // this user's own job, queued or downloading
  | { kind: "queued-elsewhere" } // deduped onto someone else's in-flight job
  | { kind: "done" }
  | { kind: "error"; message: string };

// myId is never 0, so a member's deduped job — which the server strips to
// user_id 0 before it leaves the process — and an admin's (who keeps the
// real, non-zero owner id) both correctly fall through to "not mine" here.
function rowStateFor(job: DownloadJob, myId: number | undefined): RowState {
  if (job.status === "done") return job.track_id != null && job.track_available ? { kind: "queued", jobId: job.id } : { kind: "done" };
  if (job.user_id !== myId) return { kind: "queued-elsewhere" };
  return { kind: "queued", jobId: job.id };
}

/**
 * 下载 for one YouTube video (a search result or a recommendation): posts the
 * download, then shows its live status in place (排队中 → 下载中 → ▶ 播放),
 * or an error with 重试. A download started earlier this session (even before
 * navigating away and back) keeps showing its live status. It is a round icon
 * (⬇, a progress ring, ▶, ↻); errors go to the row's text through `onError`.
 */
export function VideoDownload({ video, onError }: { video: YTVideo; onError?: OnRowError }) {
  const t = useT();
  const { user } = useAuth();
  const dl = useDownloads();
  const [state, setState] = useState<RowState>({ kind: "idle" });

  // 下载: first, does the library seem to have this song already (another
  // upload of it)? Then ask; a failed check doesn't stand in the way.
  async function download() {
    // Re-entrancy guard: only an idle row starts a download; retrying an
    // errored row resets it to idle first.
    if (state.kind !== "idle") return;
    setState({ kind: "posting" });
    const same = await api.youtubeInLibrary(video).catch(() => []);
    if (same.length) {
      setState({ kind: "confirm", tracks: same });
      return;
    }
    await post();
  }

  async function post() {
    setState({ kind: "posting" });
    try {
      const jobs = await api.createDownload({ video });
      const job = jobs[0];
      if (job) dl.track([job]);
      setState(job ? rowStateFor(job, user?.id) : { kind: "queued" });
    } catch (e) {
      setState({ kind: "error", message: errorMessage(e, "yt.downloadFailed") });
    }
  }

  useReportRowError(onError, state.kind === "error" ? state.message : "");

  let row = state;
  const known = row.kind === "idle" ? Object.values(dl.jobs).find((j) => j.video_id === video.id) : undefined;
  if (known) row = { kind: "queued", jobId: known.id };

  switch (row.kind) {
    case "idle":
      return <button className="icon-btn filled" aria-label={t("yt.download")} title={t("yt.download")} onClick={download}><DownloadIcon /></button>;
    case "confirm":
      return (
        <>
          <button className="icon-btn filled busy" disabled aria-label={t("yt.download")} title={t("yt.download")}><DownloadIcon /></button>
          <div className="sheet-backdrop" onClick={() => setState({ kind: "idle" })}>
            <div className="sheet" role="dialog" aria-label={t("yt.sameSongTitle")} onClick={(e) => e.stopPropagation()}>
              <h2>{t("yt.sameSongTitle")}</h2>
              <p className="muted small">{t("yt.sameSongHint")}</p>
              <ul className="rows">
                {row.tracks.map((tr) => (
                  <li key={tr.id} className="sheet-row"><span className="ellipsis">《{tr.title}》</span><span className="muted ellipsis">{tr.artist}</span></li>
                ))}
              </ul>
              <div className="lyrics-maker-actions">
                <button className="secondary" onClick={() => setState({ kind: "idle" })}>{t("common.cancel")}</button>
                <button className="primary" onClick={() => void post()}>{t("yt.downloadAnyway")}</button>
              </div>
            </div>
          </div>
        </>
      );
    case "posting":
      return <button className="icon-btn filled busy" disabled aria-label={t("yt.download")} title={t("yt.download")}><ProgressRing pct={null} /></button>;
    case "queued":
      return row.jobId != null ? (
        <DownloadStatus jobId={row.jobId} compact onError={onError} />
      ) : (
        <button className="icon-btn state" disabled aria-label={t("yt.queued")} title={t("yt.queued")}><CheckIcon /></button>
      );
    case "queued-elsewhere":
      return <button className="icon-btn state" disabled aria-label={t("yt.queuedElsewhere")} title={t("yt.queuedElsewhere")}><ClockIcon /></button>;
    case "done":
      return <button className="icon-btn state" disabled aria-label={t("yt.alreadyInLibrary")} title={t("yt.alreadyInLibrary")}><CheckIcon /></button>;
    case "error":
      return <button className="icon-btn outline" aria-label={t("common.retry")} title={t("common.retry")} onClick={() => setState({ kind: "idle" })}><RetryIcon /></button>;
  }
}
