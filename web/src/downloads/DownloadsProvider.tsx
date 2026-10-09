import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type Dispatch, type ReactNode, type SetStateAction } from "react";
import { useLocation } from "react-router";
import { api } from "../api/client";
import type { DownloadJob } from "../api/types";
import { useAuth } from "../auth/AuthProvider";
import { useT } from "../i18n/i18n";
import { usePlayer } from "../player/PlayerProvider";
import { errorMessage } from "../i18n/errors";
import { IDLE_POLL_MS, isActiveJob, useJobPoll } from "./useJobPoll";

const TOAST_MS = 8000;

interface DownloadsApi {
  /** Live state of the jobs this browser session started. */
  jobs: Record<number, DownloadJob>;
  /** Remember (and watch) the user's own jobs just enqueued from this session. */
  track: (jobs: DownloadJob[]) => void;
  retry: (id: number) => Promise<void>;
  /** Plays a downloaded track; primes synchronously, so call it straight from a tap. */
  playTrack: (trackId: number) => Promise<void>;
  /** The user's job list as last polled (null until the first response). */
  list: DownloadJob[] | null;
  setList: Dispatch<SetStateAction<DownloadJob[] | null>>;
  listError: string;
  /** A page that shows the list registers here (while mounted) to keep it fresh. */
  watch: () => () => void;
  /** A row showing ▶ 播放 for a job registers here, so its toast is not redundant. */
  rowShowing: (jobId: number) => () => void;
}

const Ctx = createContext<DownloadsApi | null>(null);

export function useDownloads(): DownloadsApi {
  const v = useContext(Ctx);
  if (!v) throw new Error("useDownloads outside DownloadsProvider");
  return v;
}

/**
 * Keeps, in memory only, the ids of jobs started from this browser session,
 * polls them while any is queued/downloading, and raises a one-at-a-time
 * "downloaded — play" toast when one finishes.
 */
export function DownloadsProvider({ children }: { children: ReactNode }) {
  const t = useT();
  const { user } = useAuth();
  const player = usePlayer();
  const [jobs, setJobs] = useState<Record<number, DownloadJob>>({});
  const jobsRef = useRef(jobs);
  const toasted = useRef(new Set<number>());
  const [toasts, setToasts] = useState<DownloadJob[]>([]);
  const [list, setList] = useState<DownloadJob[] | null>(null);
  const [listError, setListError] = useState("");
  const [watchers, setWatchers] = useState(0);
  const showing = useRef(new Map<number, number>());
  const { pathname } = useLocation();
  const resumeRef = useRef<() => void>(() => {});

  const commit = useCallback((next: Record<number, DownloadJob>) => {
    jobsRef.current = next;
    setJobs(next);
  }, []);

  const track = useCallback(
    (added: DownloadJob[]) => {
      // Someone else's job is only worth keeping once done (its ▶ 播放): the
      // user's own list never reports a foreign in-flight job, so polling
      // for one would never end.
      const mine = added.filter((j) => j.user_id === user?.id || j.status === "done");
      if (mine.length === 0) return;
      const next = { ...jobsRef.current };
      for (const j of mine) next[j.id] = j;
      commit(next);
      resumeRef.current();
    },
    [user?.id, commit],
  );

  const pending = useMemo(() => Object.values(jobs).some(isActiveJob), [jobs]);

  const { resume } = useJobPoll({
    enabled: pending || watchers > 0,
    idleMs: watchers > 0 ? IDLE_POLL_MS : null,
    onError: (e) => setListError(errorMessage(e, "downloads.loadFailed")),
    onJobs: (js) => {
      setList(js);
      setListError("");
      const prev = jobsRef.current;
      const byId = new Map(js.map((j) => [j.id, j]));
      const next = { ...prev };
      const finished: DownloadJob[] = [];
      for (const old of Object.values(prev)) {
        if (!isActiveJob(old)) continue;
        // A tracked job that vanished from the list was removed meanwhile.
        const cur = byId.get(old.id) ?? { ...old, status: "cancelled" as const };
        next[old.id] = cur;
        if (cur.status === "done" && cur.track_id != null && cur.track_available && !toasted.current.has(cur.id)) {
          toasted.current.add(cur.id);
          finished.push(cur);
        }
      }
      commit(next);
      if (finished.length) setToasts((q) => [...q, ...finished]);
    },
  });

  const watch = useCallback(() => {
    setWatchers((n) => n + 1);
    return () => setWatchers((n) => n - 1);
  }, []);

  const rowShowing = useCallback((id: number) => {
    showing.current.set(id, (showing.current.get(id) ?? 0) + 1);
    return () => {
      const n = (showing.current.get(id) ?? 1) - 1;
      if (n <= 0) showing.current.delete(id);
      else showing.current.set(id, n);
    };
  }, []);

  resumeRef.current = resume;

  const retry = useCallback(
    async (id: number) => {
      await api.retryDownload(id);
      resumeRef.current();
      const old = jobsRef.current[id];
      if (old) commit({ ...jobsRef.current, [id]: { ...old, status: "queued", progress: 0, error: "" } });
    },
    [commit],
  );

  const playTrack = useCallback(
    async (trackId: number) => {
      // Synchronously inside the tap, before the await: iOS only treats a
      // play() called directly from the gesture as allowed.
      player.prime();
      const tr = await api.track(trackId);
      player.playList([tr], 0);
    },
    [player],
  );

  const current = toasts[0];
  useEffect(() => {
    if (!current) return;
    // Redundant: the downloads page, or the row the user tapped 下载 on,
    // already offers ▶ 播放 for it.
    if (pathname === "/downloads" || showing.current.has(current.id)) {
      setToasts((q) => q.slice(1));
      return;
    }
    const timer = setTimeout(() => setToasts((q) => q.slice(1)), TOAST_MS);
    return () => clearTimeout(timer);
  }, [current, pathname]);

  const value = useMemo(
    () => ({ jobs, track, retry, playTrack, list, setList, listError, watch, rowShowing }),
    [jobs, track, retry, playTrack, list, listError, watch, rowShowing],
  );

  return (
    <Ctx.Provider value={value}>
      {children}
      {current && (
        <div className="toast" role="status">
          <span className="ellipsis">{t("downloads.toastDone", { title: current.title })}</span>
          <button
            className="secondary"
            onClick={() => {
              const id = current.track_id;
              setToasts((q) => q.slice(1));
              if (id != null) void playTrack(id).catch(() => {});
            }}
          >
            <span aria-hidden="true">▶</span> {t("common.play")}
          </button>
        </div>
      )}
    </Ctx.Provider>
  );
}
