import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../api/client";
import type { YTPlaylist, YTVideo } from "../api/types";
import { duration, isSafeThumbnailUrl } from "../format";
import { searchErrorText } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { Cover } from "./Cover";
import { FollowButton } from "./FollowButton";
import { PreviewButton } from "./PreviewButton";
import { RowErrorLine, useRowErrors } from "./rowError";
import { VideoDownload } from "./VideoDownload";
import { YouTubePlaylistRow } from "./YouTubePlaylistRow";

/** The first page's size and the step 显示更多 grows it by (the server caps it at 50). */
export const SEARCH_PAGE = 10;

/**
 * YouTube search results inside SearchPage. In `auto` mode it searches as
 * soon as it mounts (the library had no match); otherwise it shows a button
 * and only searches once tapped. A slow response for a query the caller has
 * since moved away from is discarded (seq-guarded) rather than overwriting
 * whatever the current query already found. 显示更多 asks for a larger page
 * (YouTube search has no offset) and appends only the videos not shown yet.
 */
export function YouTubeResults({ query, auto }: { query: string; auto: boolean }) {
  const t = useT();
  const [videos, setVideos] = useState<YTVideo[] | null>(null);
  const [playlists, setPlaylists] = useState<YTPlaylist[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  // 显示更多: whether a larger page may find more, the page size shown, and that page's own state.
  const [more, setMore] = useState(false);
  const [pageN, setPageN] = useState(SEARCH_PAGE);
  const [moreLoading, setMoreLoading] = useState(false);
  const [moreError, setMoreError] = useState("");
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
        setMore(!!res.more);
        setPageN(SEARCH_PAGE);
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

  const loadMore = useCallback(() => {
    const mySeq = seq.current; // a new query invalidates this page too
    const n = pageN + SEARCH_PAGE;
    setMoreLoading(true);
    setMoreError("");
    api
      .youtubeSearch(query, n)
      .then((res) => {
        if (seq.current !== mySeq) return;
        const have = videos ?? [];
        const seen = new Set(have.map((v) => v.id));
        const fresh = res.videos.filter((v) => !seen.has(v.id));
        setVideos([...have, ...fresh]);
        setPageN(n);
        // A page that found nothing new ends it, whatever it says.
        setMore(!!res.more && fresh.length > 0);
      })
      .catch((e) => {
        if (seq.current !== mySeq) return;
        setMoreError(searchErrorText(e));
      })
      .finally(() => {
        if (seq.current !== mySeq) return;
        setMoreLoading(false);
      });
  }, [query, pageN, videos]);

  useEffect(() => {
    // Invalidate any in-flight fetch for the query this replaces, and reset
    // this component back to a clean slate for the new one.
    seq.current++;
    setVideos(null);
    setPlaylists([]);
    setError("");
    setLoading(false);
    setMore(false);
    setPageN(SEARCH_PAGE);
    setMoreLoading(false);
    setMoreError("");
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
                  <PreviewButton icon target={{ videoId: v.id, media: "audio", title: v.title, channel: v.channel, durationS: v.duration_s, keepTo: "music" }} />
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
      {videos && videos.length > 0 && (more || moreError) && (
        <div className="yt-more-wrap">
          {moreError && <p className="error small">{moreError}</p>}
          <button className="secondary compact yt-more" disabled={moreLoading} onClick={loadMore}>
            {moreLoading ? t("yt.searching") : t("yt.showMore")}
          </button>
        </div>
      )}
      {playlists.length > 0 && (
        <>
          <h2 className="section-title">{t("yt.playlists")}</h2>
          <ul className="rows yt-playlists">
            {playlists.map((p) => (
              <YouTubePlaylistRow key={p.id} playlist={p} />
            ))}
          </ul>
        </>
      )}
    </>
  );
}
