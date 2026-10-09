import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../api/client";
import type { YTPlaylist, YTVideo } from "../api/types";
import { duration, isSafeThumbnailUrl } from "../format";
import { searchErrorText } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { Cover } from "./Cover";
import { FollowButton } from "./FollowButton";
import { RowErrorLine, useRowErrors } from "./rowError";
import { ListDownloadButton } from "./ListDownloadButton";
import { VideoDownload } from "./VideoDownload";

/**
 * YouTube search results inside SearchPage. In `auto` mode it searches as
 * soon as it mounts (the library had no match); otherwise it shows a button
 * and only searches once tapped. A slow response for a query the caller has
 * since moved away from is discarded (seq-guarded) rather than overwriting
 * whatever the current query already found.
 */
export function YouTubeResults({ query, auto }: { query: string; auto: boolean }) {
  const t = useT();
  const [videos, setVideos] = useState<YTVideo[] | null>(null);
  const [playlists, setPlaylists] = useState<YTPlaylist[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const seq = useRef(0);
  const [errs, setRowError] = useRowErrors();
  // A row shows one error: its download's, else its follow's.
  const rowErr = (id: string) => errs[id] ?? errs[`follow:${id}`];
  // Channels this user follows, read once a result offers 关注频道 (null until known).
  const [followed, setFollowed] = useState<Set<string> | null>(null);
  const wantFollowed = followed === null && (videos ?? []).some((v) => v.channel_id);
  useEffect(() => {
    if (!wantFollowed) return;
    let live = true;
    api.myChannels().then(
      (m) => live && setFollowed(new Set(m.channels.map((c) => c.channel.id))),
      () => {}, // unknown: rows offer 关注频道, and following again is harmless
    );
    return () => {
      live = false;
    };
  }, [wantFollowed]);

  const runSearch = useCallback((q: string) => {
    const mySeq = ++seq.current;
    setLoading(true);
    setError("");
    api
      .youtubeSearch(q)
      .then((res) => {
        if (seq.current !== mySeq) return; // a newer query has since started
        setVideos(res.videos);
        setPlaylists(res.playlists ?? []);
      })
      .catch((e) => {
        if (seq.current !== mySeq) return;
        setError(searchErrorText(e));
      })
      .finally(() => {
        if (seq.current !== mySeq) return;
        setLoading(false);
      });
  }, []);

  useEffect(() => {
    // Invalidate any in-flight fetch for the query this replaces, and reset
    // this component back to a clean slate for the new one.
    seq.current++;
    setVideos(null);
    setPlaylists([]);
    setError("");
    setLoading(false);
    if (auto && query) runSearch(query);
  }, [query, auto, runSearch]);

  if (!auto && videos === null && !loading) {
    return (
      <button className="secondary" onClick={() => runSearch(query)}>
        {t("yt.searchButton", { query })}
      </button>
    );
  }

  return (
    <>
      {loading && <p className="muted">{t("yt.searching")}</p>}
      {error && <p className="error">{error}</p>}
      {videos && videos.length === 0 && !loading && <p className="muted">{t("yt.noResults")}</p>}
      {videos && videos.length > 0 && (
        <ul className="rows yt-results">
          {videos.map((v) => {
            return (
              <li key={v.id} className="row">
                <Cover seed={v.id} label={v.title} size={48} src={isSafeThumbnailUrl(v.thumbnail) ? v.thumbnail : undefined} noReferrer />
                <span className="yt-text">
                  <span className="row-title">{v.title}</span>
                  {rowErr(v.id) ? (
                    <RowErrorLine error={rowErr(v.id)} />
                  ) : (
                    <span className="ellipsis muted small">{v.channel} · {duration(v.duration_s * 1000)}</span>
                  )}
                </span>
                <span className="job-actions row-actions">
                  {v.channel_id && (
                    <FollowButton
                      icon
                      onError={(m) => setRowError(`follow:${v.id}`, m ? { text: m } : null)}
                      channelId={v.channel_id}
                      following={followed?.has(v.channel_id) ?? false}
                      label={t("channels.followChannel")}
                      onChange={(on) => {
                        const id = v.channel_id!;
                        setFollowed((f) => {
                          const next = new Set(f ?? []);
                          if (on) next.add(id);
                          else next.delete(id);
                          return next;
                        });
                      }}
                    />
                  )}
                  <VideoDownload key={`${query}:${v.id}`} video={v} onError={(e) => setRowError(v.id, e)} />
                </span>
              </li>
            );
          })}
        </ul>
      )}
      {playlists.length > 0 && (
        <>
          <h2 className="section-title">{t("yt.playlists")}</h2>
          <ul className="rows yt-playlists">
            {playlists.map((p) => (
              <li key={p.id} className="row playlist-card">
                <Cover seed={p.id} label={p.title} size={48} src={isSafeThumbnailUrl(p.thumbnail) ? p.thumbnail : undefined} noReferrer />
                <span className="yt-text">
                  <span className="ellipsis">{p.title}</span>
                  <span className="ellipsis muted small">{p.channel}</span>
                </span>
                <ListDownloadButton listId={p.id} title={p.title} />
              </li>
            ))}
          </ul>
        </>
      )}
    </>
  );
}
