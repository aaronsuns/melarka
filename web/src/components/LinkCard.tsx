import { useState } from "react";
import { api } from "../api/client";
import { useAuth } from "../auth/AuthProvider";
import { useDownloads } from "../downloads/DownloadsProvider";
import { DownloadStatus } from "./DownloadStatus";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { watchUrl, type YouTubeLink } from "../youtubeLink";
import { ListDownloadButton } from "./ListDownloadButton";

type SongState = "idle" | "posting" | "queued" | "elsewhere" | "error";

function DownloadSong({ videoId, label }: { videoId: string; label: string }) {
  const t = useT();
  const { user } = useAuth();
  const dl = useDownloads();
  const [state, setState] = useState<SongState>("idle");
  const [localJobId, setJobId] = useState<number | null>(null);
  const jobId = localJobId ?? Object.values(dl.jobs).find((j) => j.video_id === videoId)?.id ?? null;
  const [message, setMessage] = useState("");

  async function go() {
    if (state !== "idle" && state !== "error") return;
    setState("posting");
    try {
      const job = (await api.createDownload({ url: watchUrl(videoId) }))[0];
      if (job && (job.user_id === user?.id || job.status === "done")) {
        dl.track([job]);
        setJobId(job.id);
        setState("queued");
      } else {
        setState(job ? "elsewhere" : "queued");
      }
    } catch (e) {
      setMessage(errorMessage(e, "yt.downloadFailed"));
      setState("error");
    }
  }

  if ((state === "queued" || state === "idle") && jobId != null) return <DownloadStatus jobId={jobId} />;
  if (state === "elsewhere") return <button className="secondary" disabled>{t("yt.queuedElsewhere")}</button>;
  if (state === "queued") return <button className="secondary" disabled>{t("yt.queued")}</button>;
  return (
    <span className="job-actions">
      {state === "error" && <span className="error small">{message}</span>}
      <button className="secondary" disabled={state === "posting"} onClick={() => void go()}>
        {state === "error" ? t("common.retry") : label}
      </button>
    </span>
  );
}

/** What to offer for a pasted YouTube link, instead of searching for it. */
export function LinkCard({ link }: { link: YouTubeLink }) {
  const t = useT();
  const wholeList = link.listId !== null && !link.isMix;
  return (
    <div className="link-card">
      <h2 className="section-title">{t("link.title")}</h2>
      <div className="link-actions">
        {link.videoId && wholeList && link.listId ? (
          <>
            <DownloadSong videoId={link.videoId} label={t("link.thisSong")} />
            <ListDownloadButton listId={link.listId} label={t("link.wholeList")} />
          </>
        ) : link.videoId ? (
          <DownloadSong videoId={link.videoId} label={t("link.downloadSong")} />
        ) : link.listId ? (
          <ListDownloadButton listId={link.listId} />
        ) : null}
      </div>
    </div>
  );
}
