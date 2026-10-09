import { useEffect, useState } from "react";
import { api } from "../../api/client";
import type { DownloadJob, User } from "../../api/types";
import { isActive, JobRow } from "../../components/JobRow";
import { errorMessage } from "../../i18n/errors";
import { useT } from "../../i18n/i18n";
import { usePlayer } from "../../player/PlayerProvider";
import { usePlayDownloads } from "../MyDownloadsPage";

const ACTIVE_POLL_MS = 2000;
const IDLE_POLL_MS = 15_000;

// Same shape as DownloadsPage, but scoped to every user's jobs (?all=1) and
// labelling each row with who started it.
export default function AllDownloadsPage() {
  const t = useT();
  const player = usePlayer();
  const [jobs, setJobs] = useState<DownloadJob[] | null>(null);
  const [loadError, setLoadError] = useState("");
  const [busyIds, setBusyIds] = useState<Set<number>>(new Set());
  const [rowErrors, setRowErrors] = useState<Record<number, string>>({});
  const [users, setUsers] = useState<User[]>([]);
  const [who, setWho] = useState<"all" | number>("all");
  const downloads = usePlayDownloads();

  useEffect(() => {
    api.users().then(setUsers).catch(() => setUsers([]));
  }, []);

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = () => {
      api
        .downloads(true)
        .then((js) => {
          if (cancelled) return;
          setJobs(js);
          setLoadError("");
          timer = setTimeout(tick, js.some(isActive) ? ACTIVE_POLL_MS : IDLE_POLL_MS);
        })
        .catch((e) => {
          if (cancelled) return;
          setLoadError(errorMessage(e, "common.loadFailed"));
          timer = setTimeout(tick, IDLE_POLL_MS);
        });
    };
    tick();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, []);

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
        setRowErrors((m) => ({ ...m, [job.id]: errorMessage(e, "admin.downloads.retryFailed") }));
        return;
      }
      setJobs((js) => js?.map((j) => (j.id === job.id ? { ...j, status: "queued" as const, progress: 0, error: "" } : j)) ?? js);
    });
  }

  async function play(job: DownloadJob) {
    if (job.track_id == null) return;
    player.prime();
    setRowErrors((e) => ({ ...e, [job.id]: "" }));
    try {
      const t = await api.track(job.track_id);
      player.playList([t], 0);
    } catch {
      setRowErrors((e) => ({ ...e, [job.id]: t("admin.downloads.trackGone") }));
    }
  }

  return (
    <>
      <div className="inline-form">
        <select aria-label={t("admin.downloads.user")} value={String(who)} onChange={(e) => setWho(e.target.value === "all" ? "all" : Number(e.target.value))}>
          <option value="all">{t("admin.downloads.everyone")}</option>
          {users.map((u) => <option key={u.id} value={u.id}>{u.username}</option>)}
        </select>
        <button className="secondary" onClick={() => void downloads.play({ user: who })}>{t("playlist.playAll")}</button>
        <button className="secondary" onClick={() => void downloads.play({ user: who, shuffle: true })}>🔀 {t("library.shuffle")}</button>
      </div>
      {downloads.msg && <p className="muted small">{downloads.msg}</p>}
      {loadError && <p className="error">{loadError}</p>}
      {jobs?.length === 0 && <p className="muted">{t("admin.downloads.empty")}</p>}
      <ul className="rows">
        {jobs?.filter((j) => who === "all" || j.user_id === who).map((job) => (
          <JobRow
            key={job.id}
            job={job}
            busy={busyIds.has(job.id)}
            rowError={rowErrors[job.id]}
            showUsername
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
