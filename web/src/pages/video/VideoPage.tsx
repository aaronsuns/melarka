import { useEffect, useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { api } from "../../api/client";
import type { VideoHistory, VideoRecs, YTVideo } from "../../api/types";
import { useVisiblePoll } from "../../channels/useVisiblePoll";
import { asVideo, VideoCard } from "../../components/VideoCard";
import { errorMessage, isTransient, searchErrorText } from "../../i18n/errors";
import { useT } from "../../i18n/i18n";

const RECENT_MAX = 12;
const RECS_POLL_MS = 10_000;
// The last few searches' results, so going back from a video shows them at
// once instead of asking YouTube again.
const CACHE_MAX = 5;
const cache = new Map<string, YTVideo[]>();
function remember(q: string, videos: YTVideo[]) {
  cache.delete(q);
  cache.set(q, videos);
  if (cache.size > CACHE_MAX) cache.delete(cache.keys().next().value!);
}

/**
 * 视频: search YouTube; with an empty box, the user's searches (tap
 * to search again), 为你推荐 and 最近观看. The query lives in ?q=, so Back
 * from a video returns to its results. Everything shown is YouTube's or the
 * user's text, rendered as text.
 */
export default function VideoPage() {
  const t = useT();
  const [params, setParams] = useSearchParams();
  const q = (params.get("q") ?? "").trim();
  const [text, setText] = useState(q);
  const [results, setResults] = useState<YTVideo[] | null>(null);
  const [searchErr, setSearchErr] = useState("");
  const [history, setHistory] = useState<VideoHistory | null>(null);
  const [historyErr, setHistoryErr] = useState("");
  const [recs, setRecs] = useState<VideoRecs | null>(null);
  const [recsErr, setRecsErr] = useState("");
  const [recsStopped, setRecsStopped] = useState(false); // a failure worth no retry (Channels off)
  // Bumped to ask a failed search again with the same words.
  const [again, setAgain] = useState(0);
  // The next search was submitted (or a chip tapped): the server records it.
  const record = useRef(false);
  const empty = text.trim() === "";

  // The box follows the URL (Back, a chip).
  useEffect(() => setText(q), [q]);

  useEffect(() => {
    setSearchErr("");
    const rec = record.current;
    record.current = false;
    if (!q) {
      setResults(null);
      return;
    }
    const cached = cache.get(q);
    if (cached && !rec) {
      setResults(cached);
      return;
    }
    setResults(null);
    let live = true;
    api.videoSearch(q, rec).then(
      (r) => {
        const videos = r.videos ?? [];
        remember(q, videos);
        if (live) setResults(videos);
      },
      (e) => live && setSearchErr(searchErrorText(e)),
    );
    return () => {
      live = false;
    };
  }, [q, again]);

  // The history, each time the box is emptied (a search just added to it).
  useEffect(() => {
    if (!empty) return;
    let live = true;
    api.videoHistory().then(
      (h) => {
        if (!live) return;
        setHistory(h);
        setHistoryErr("");
      },
      (e) => live && setHistoryErr(errorMessage(e, "common.loadFailed")),
    );
    return () => {
      live = false;
    };
  }, [empty]);

  // 为你推荐: asked once, then every 10 s while the server refreshes it.
  useVisiblePoll(
    () => {
      api.videoRecommendations().then(
        (r) => {
          setRecs(r);
          setRecsErr("");
        },
        (e) => {
          setRecsErr(errorMessage(e, "common.loadFailed"));
          // A blip on a slow link: the next 10 s asks again. Anything else stops.
          if (!isTransient(e)) setRecsStopped(true);
        },
      );
    },
    RECS_POLL_MS,
    null,
    empty && !recsStopped && (recs === null || recs.refreshing),
  );

  function search(query: string) {
    const v = query.trim();
    setText(v);
    if (!v) return;
    if (v === q) {
      // The same words: ask again only when the last try failed (a timeout on a slow link).
      if (!searchErr) return;
      record.current = true;
      setAgain((n) => n + 1);
      return;
    }
    record.current = true;
    setParams({ q: v });
  }
  function onSubmit(e: FormEvent) {
    e.preventDefault();
    (document.activeElement as HTMLElement | null)?.blur?.(); // the phone's keyboard goes away
    search(text);
  }
  function onChange(v: string) {
    setText(v);
    // An emptied box shows the history again; Back still returns to the results.
    if (v.trim() === "" && q) setParams({}, { replace: true });
  }
  async function clearHistory() {
    setHistoryErr("");
    try {
      await api.clearVideoHistory();
      setHistory({ watches: [], searches: [] });
      setRecs(null); // the server drops the suggestions with it: ask again
      setRecsErr("");
      setRecsStopped(false);
    } catch (e) {
      setHistoryErr(errorMessage(e, "common.actionFailed"));
    }
  }

  const searches = history?.searches ?? [];
  const watches = (history?.watches ?? []).slice(0, RECENT_MAX);
  const items = recs?.items ?? [];
  return (
    <section className="video-page" data-no-music-prime="">
      <h1 className="page-title">{t("nav.video")}</h1>
      <form className="inline-form" role="search" onSubmit={onSubmit}>
        <input
          type="search"
          aria-label={t("video.searchLabel")}
          placeholder={t("video.searchLabel")}
          value={text}
          onChange={(e) => onChange(e.target.value)}
          enterKeyHint="search"
        />
        <button className="primary" type="submit">{t("video.search")}</button>
      </form>
      {empty ? (
        <>
          {historyErr && <p className="error">{historyErr}</p>}
          {(searches.length > 0 || watches.length > 0) && (
            <div className="chips" role="group" aria-label={t("video.searchLabel")}>
              {searches.map((s) => <button key={s} className="chip tag-chip" onClick={() => search(s)}>{s}</button>)}
              <button className="chip tag-chip chip-clear" onClick={clearHistory}>{t("video.clearHistory")}</button>
            </div>
          )}
          <h2 className="section-title">{t("video.forYou")}</h2>
          {recsErr && recsErr !== historyErr && <p className="error">{recsErr}</p>}
          {recs?.refreshing && <p className="muted small">{t("video.refreshing")}</p>}
          {items.length > 0 ? (
            <div className="video-grid">
              {items.map((r) => (
                <VideoCard
                  key={r.video_id}
                  v={asVideo(r)}
                  reason={r.reason_kind === "search" ? t("video.becauseSearched", { query: r.reason }) : t("video.becauseWatched", { title: r.reason })}
                />
              ))}
            </div>
          ) : (
            recs && !recs.refreshing && <p className="muted">{t("video.forYouEmpty")}</p>
          )}
          {watches.length > 0 && (
            <>
              <h2 className="section-title">{t("video.recent")}</h2>
              <div className="video-grid">
                {watches.map((w) => <VideoCard key={w.video_id} v={asVideo(w)} />)}
              </div>
            </>
          )}
        </>
      ) : (
        <>
          {searchErr && <p className="error">{searchErr}</p>}
          {!searchErr && results === null && q && <p className="muted">{t("common.loading")}</p>}
          {results && results.length === 0 && <p className="muted">{t("video.noResults")}</p>}
          {results && results.length > 0 && (
            <div className="video-grid">
              {results.map((v) => <VideoCard key={v.id} v={v} />)}
            </div>
          )}
        </>
      )}
    </section>
  );
}
