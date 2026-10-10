import { useId, useRef, useState } from "react";
import { api } from "../api/client";
import type { YTPlaylist, YTVideo } from "../api/types";
import { duration, isSafeThumbnailUrl } from "../format";
import { searchErrorText } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { Cover } from "./Cover";
import { ListDownloadButton } from "./ListDownloadButton";
import { PreviewButton } from "./PreviewButton";
import { RowErrorLine, useRowErrors } from "./rowError";
import { VideoDownload } from "./VideoDownload";

/** How many more entries each 显示更多 asks for (the server's page; it caps at 200). */
export const ENTRIES_PAGE = 50;

type Entries = { videos: YTVideo[]; more: boolean; n: number };

/**
 * One YouTube playlist search result. Tapping it (▸/▾) opens its tracks in
 * place, fetched the first time only; each track gets ▶ 试听 and ⬇ 下载 like a
 * video result, and 下载全部 stays on the playlist itself.
 */
export function YouTubePlaylistRow({ playlist: p }: { playlist: YTPlaylist }) {
  const t = useT();
  const panelId = useId();
  const [open, setOpen] = useState(false);
  const [entries, setEntries] = useState<Entries | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const busy = useRef(false);
  const [errs, setRowError] = useRowErrors();
  const label = t("yt.playlistTracks", { title: p.title });

  function load(n: number) {
    if (busy.current) return;
    busy.current = true;
    setLoading(true);
    setError("");
    api
      .youtubePlaylistEntries(p.id, n === ENTRIES_PAGE ? undefined : n)
      .then((res) => {
        setEntries((cur) => {
          const have = cur?.videos ?? [];
          const seen = new Set(have.map((v) => v.id));
          const added = res.videos.filter((v) => !seen.has(v.id));
          // A page that found nothing new ends 显示更多 whatever it says.
          return { videos: [...have, ...added], more: res.more && (cur === null || added.length > 0), n };
        });
      })
      .catch((e) => setError(searchErrorText(e)))
      .finally(() => {
        busy.current = false;
        setLoading(false);
      });
  }

  function toggle() {
    const next = !open;
    setOpen(next);
    if (next && entries === null && !busy.current) load(ENTRIES_PAGE);
  }

  return (
    <li className="yt-playlist">
      <div className="row playlist-card">
        <button className="pl-toggle" aria-expanded={open} aria-controls={panelId} aria-label={label} onClick={toggle}>
          <span className="pl-chevron" aria-hidden="true">{open ? "▾" : "▸"}</span>
          <Cover seed={p.id} label={p.title} size={48} src={isSafeThumbnailUrl(p.thumbnail) ? p.thumbnail : undefined} noReferrer />
          <span className="yt-text">
            <span className="ellipsis">{p.title}</span>
            <span className="ellipsis muted small">{p.channel}</span>
          </span>
        </button>
        <ListDownloadButton listId={p.id} title={p.title} />
      </div>
      <div id={panelId} role="region" aria-label={label} className="pl-tracks" hidden={!open}>
        {open && (
          <>
            {entries && entries.videos.length > 0 && (
              <ul className="rows yt-results yt-tracks">
                {entries.videos.map((v) => (
                  <li key={v.id} className="row">
                    <Cover seed={v.id} label={v.title} size={48} src={isSafeThumbnailUrl(v.thumbnail) ? v.thumbnail : undefined} noReferrer />
                    <span className="yt-text">
                      <span className="row-title">{v.title}</span>
                      {errs[v.id] ? (
                        <RowErrorLine error={errs[v.id]} />
                      ) : (
                        <span className="ellipsis muted small">{v.channel} · {duration(v.duration_s * 1000)}</span>
                      )}
                    </span>
                    <span className="job-actions row-actions">
                      <PreviewButton icon target={{ videoId: v.id, media: "audio", title: v.title, channel: v.channel, durationS: v.duration_s, keepTo: "music" }} />
                      <VideoDownload video={v} onError={(e) => setRowError(v.id, e)} />
                    </span>
                  </li>
                ))}
              </ul>
            )}
            {entries && entries.videos.length === 0 && !loading && !error && <p className="muted small">{t("yt.playlistEmpty")}</p>}
            {loading && <p className="muted small" role="status">{t("common.loading")}</p>}
            {error && (
              <p className="pl-error">
                <span className="error small">{error}</span>{" "}
                <button className="secondary compact" onClick={() => load(entries ? entries.n + ENTRIES_PAGE : ENTRIES_PAGE)}>{t("common.retry")}</button>
              </p>
            )}
            {entries?.more && !loading && !error && (
              <button className="secondary compact yt-more" onClick={() => load(entries.n + ENTRIES_PAGE)}>{t("yt.showMore")}</button>
            )}
          </>
        )}
      </div>
    </li>
  );
}
