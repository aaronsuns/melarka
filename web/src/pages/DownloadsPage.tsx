import { useEffect, useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { api } from "../api/client";
import type { DownloadJob } from "../api/types";
import { JobRow } from "../components/JobRow";
import { useDownloads } from "../downloads/DownloadsProvider";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";

const HIGHLIGHT_MS = 3000;

function mergeJobs(existing: DownloadJob[], added: DownloadJob[]): DownloadJob[] {
  const byId = new Map(existing.map((j) => [j.id, j]));
  for (const j of added) byId.set(j.id, j);
  return [...byId.values()].sort((a, b) => b.created_at - a.created_at);
}

export default function DownloadsPage() {
  const t = useT();
  const dl = useDownloads();
  const [url, setUrl] = useState("");
  const [posting, setPosting] = useState(false);
  const [postMsg, setPostMsg] = useState("");
  const [postError, setPostError] = useState("");
  const [actionError, setActionError] = useState("");
  const [busyIds, setBusyIds] = useState<Set<number>>(new Set());
  const [rowErrors, setRowErrors] = useState<Record<number, string>>({});
  const postBusy = useRef(false);

  // The provider owns the one poll of the job list (2 s while anything is
  // queued/downloading, 15 s while this page is open, none while hidden).
  const { watch, list: jobs, setList: setJobs, listError } = dl;
  useEffect(() => watch(), [watch]);
  const loadError = actionError || listError;
  const setLoadError = setActionError;

  // ?job=<id> (from a search-result status link): scroll that job into view
  // once the list has loaded and highlight it briefly.
  const focusJob = Number(useSearchParams()[0].get("job")) || null;
  const [highlightId, setHighlightId] = useState<number | null>(null);
  const focused = useRef<number | null>(null);
  const present = focusJob != null && !!jobs?.some((j) => j.id === focusJob);
  useEffect(() => {
    if (!present || focusJob == null || focused.current === focusJob) return;
    focused.current = focusJob;
    setHighlightId(focusJob);
    document.getElementById(`job-${focusJob}`)?.scrollIntoView?.({ block: "center" });
    const timer = setTimeout(() => setHighlightId(null), HIGHLIGHT_MS);
    return () => clearTimeout(timer);
  }, [present, focusJob]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const trimmed = url.trim();
    if (!trimmed || postBusy.current) return;
    postBusy.current = true;
    setPosting(true);
    setPostError("");
    setPostMsg("");
    try {
      const added = await api.createDownload({ url: trimmed });
      setPostMsg(t("downloads.added", { count: added.length }));
      setUrl("");
      setJobs((js) => mergeJobs(js ?? [], added));
    } catch (err) {
      setPostError(errorMessage(err, "downloads.addFailed"));
    } finally {
      postBusy.current = false;
      setPosting(false);
    }
  }

  function withBusy(id: number, fn: () => Promise<void>) {
    setBusyIds((s) => (s.has(id) ? s : new Set(s).add(id)));
    void fn()
      .catch((e: unknown) => setLoadError(errorMessage(e, "common.actionFailed")))
      .finally(() =>
        setBusyIds((s) => {
          if (!s.has(id)) return s;
          const next = new Set(s);
          next.delete(id);
          return next;
        }),
      );
  }

  function cancel(job: DownloadJob) {
    if (busyIds.has(job.id)) return;
    withBusy(job.id, async () => {
      await api.cancelDownload(job.id);
      setJobs((js) => js?.map((j) => (j.id === job.id ? { ...j, status: "cancelled" as const } : j)) ?? js);
    });
  }

  function remove(job: DownloadJob) {
    if (busyIds.has(job.id)) return;
    withBusy(job.id, async () => {
      await api.cancelDownload(job.id); // on a finished job the server just drops it from the list
      setJobs((js) => js?.filter((j) => j.id !== job.id) ?? js);
    });
  }

  function retry(job: DownloadJob) {
    if (busyIds.has(job.id)) return;
    setRowErrors((e) => ({ ...e, [job.id]: "" }));
    withBusy(job.id, async () => {
      try {
        await api.retryDownload(job.id);
      } catch (e) {
        // e.g. 409 already_queued (another job already covers this video):
        // about this row, so it is shown on the row.
        setRowErrors((m) => ({ ...m, [job.id]: errorMessage(e, "downloads.retryFailed") }));
        return;
      }
      setJobs((js) => js?.map((j) => (j.id === job.id ? { ...j, status: "queued" as const, progress: 0, error: "" } : j)) ?? js);
    });
  }

  async function play(job: DownloadJob) {
    if (job.track_id == null) return;
    setRowErrors((e) => ({ ...e, [job.id]: "" }));
    try {
      // playTrack primes synchronously, before its first await.
      await dl.playTrack(job.track_id);
    } catch {
      // The job's own track row (e.g. deleted/trashed since it finished),
      // not the page-level banner — it's specific to this one job.
      setRowErrors((e) => ({ ...e, [job.id]: t("downloads.trackGone") }));
    }
  }

  return (
    <>
      <h1 className="page-title">{t("downloads.title")}</h1>
      <form className="inline-form" onSubmit={submit}>
        <input
          placeholder={t("downloads.placeholder")}
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          disabled={posting}
          enterKeyHint="go"
        />
        <button className="primary" disabled={!url.trim() || posting}>{t("downloads.submit")}</button>
      </form>
      <p className="muted small">{t("downloads.autoFavoriteHint")}</p>
      {postMsg && <p className="muted">{postMsg}</p>}
      {postError && <p className="error">{postError}</p>}
      {loadError && <p className="error">{loadError}</p>}
      {jobs?.length === 0 && <p className="muted">{t("downloads.empty")}</p>}
      <ul className="rows">
        {jobs?.map((job) => (
          <JobRow
            key={job.id}
            job={job}
            busy={busyIds.has(job.id)}
            highlight={job.id === highlightId}
            rowError={rowErrors[job.id]}
            onCancel={() => cancel(job)}
            onRetry={() => retry(job)}
            onRemove={() => remove(job)}
            onPlay={() => void play(job)}
          />
        ))}
      </ul>
    </>
  );
}
