import { useEffect, useRef, useState } from "react";
import { api } from "../api/client";
import type { Album, Artist, Track } from "../api/types";
import { AlbumCards, ArtistChips } from "../components/Cards";
import { LinkCard } from "../components/LinkCard";
import { TrackList } from "../components/TrackList";
import { YouTubeResults } from "../components/YouTubeResults";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { parseYouTubeLink } from "../youtubeLink";

// query is the exact string the local search below resolved for — not
// necessarily the latest keystroke.
type Result = { query: string; tracks: Track[]; albums: Album[]; artists: Artist[] };

// yt is the query YouTube results are shown for. A YouTube search is slow
// and expensive (yt-dlp hitting YouTube, capped server-side), so it follows
// the input only once it has been still for YT_SETTLE_MS — much longer than
// the local search's debounce, which a typing pause easily exceeds — and
// fires at most once per settled query. Enter settles it immediately
// (forced: search YouTube even when the library has matches).
type YT = { query: string; forced: boolean };

const MIN_YOUTUBE_QUERY = 2;
const LOCAL_DEBOUNCE_MS = 250;
const YT_SETTLE_MS = 900;

export default function SearchPage() {
  const t = useT();
  const [q, setQ] = useState("");
  const [res, setRes] = useState<Result | null>(null);
  const [error, setError] = useState("");
  const [yt, setYt] = useState<YT>({ query: "", forced: false });
  const ytTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => {
    const query = q.trim();
    if (!query || parseYouTubeLink(query)) {
      setRes(null);
      return;
    }
    let cancelled = false;
    const timer = setTimeout(() => {
      api.search(query)
        .then((r) => !cancelled && (setRes({ query, ...r }), setError("")))
        .catch((e) => !cancelled && setError(errorMessage(e, "search.failed")));
    }, LOCAL_DEBOUNCE_MS);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [q]);

  useEffect(() => {
    const query = q.trim();
    if (parseYouTubeLink(query)) return; // a pasted link is offered for download, not searched
    ytTimer.current = setTimeout(() => {
      setYt((prev) => (prev.query === query ? prev : { query, forced: false }));
    }, YT_SETTLE_MS);
    return () => clearTimeout(ytTimer.current);
  }, [q]);

  const [history, setHistory] = useState<string[]>([]);
  useEffect(() => {
    api.searchHistory().then(setHistory).catch(() => setHistory([]));
  }, []);

  // remember records a search the user actually made. Same rule as the
  // server (internal/personal/history.go): trim, collapse whitespace, dedupe
  // on the lower-cased text (Unicode-aware), newest spelling kept, last 20.
  function remember(raw: string) {
    const query = raw.replace(/\s+/g, " ").trim();
    if (!query || parseYouTubeLink(query)) return;
    setHistory((h) => [query, ...h.filter((x) => x.toLowerCase() !== query.toLowerCase())].slice(0, 20));
    void api.recordSearch(query).catch(() => {});
  }

  function searchAgain(query: string) {
    setQ(query);
    clearTimeout(ytTimer.current);
    setYt({ query, forced: true });
    remember(query);
  }

  function clearHistory() {
    setHistory([]);
    void api.clearSearchHistory().catch(() => {});
  }

  const link = parseYouTubeLink(q.trim());

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    // Enter that confirms an IME composition (pinyin → 汉字) is not a search.
    if (e.key !== "Enter" || e.nativeEvent.isComposing || link) return;
    remember(q);
    clearTimeout(ytTimer.current);
    setYt({ query: q.trim(), forced: true });
  }

  const empty = res && res.tracks.length + res.albums.length + res.artists.length === 0;
  return (
    <>
      <input
        type="search"
        className="search-input"
        placeholder={t("search.placeholder")}
        value={q}
        onChange={(e) => setQ(e.target.value)}
        onKeyDown={onKeyDown}
        enterKeyHint="search"
        autoFocus
      />
      {!q.trim() && history.length > 0 && (
        <section>
          <h2 className="section-title" id="search-history-title">{t("search.history")}</h2>
          <div className="chips" role="group" aria-labelledby="search-history-title">
            {history.map((h) => <button key={h} className="chip tag-chip" onClick={() => searchAgain(h)}>{h}</button>)}
            <button className="chip tag-chip chip-clear" onClick={clearHistory}>{t("search.clearHistory")}</button>
          </div>
        </section>
      )}
      {link ? <LinkCard link={link} /> : <div onClickCapture={(e) => { if ((e.target as Element).closest("a, button")) remember(q); }}>
      {error && <p className="error">{error}</p>}
      {empty && <p className="muted">{t("search.noResults", { query: q.trim() })}</p>}
      {res && res.artists.length > 0 && (<><h2 className="section-title">{t("search.artistsHeading")}</h2><ArtistChips artists={res.artists} /></>)}
      {res && res.albums.length > 0 && (<><h2 className="section-title">{t("search.albumsHeading")}</h2><AlbumCards albums={res.albums} /></>)}
      {res && res.tracks.length > 0 && (
        <>
          <h2 className="section-title">{t("search.songsHeading")}</h2>
          <TrackList
            tracks={res.tracks}
            onRemoved={(id) => setRes((r) => r && { ...r, tracks: r.tracks.filter((track) => track.id !== id) })}
            onChange={(track) => setRes((r) => r && { ...r, tracks: r.tracks.map((x) => (x.id === track.id ? track : x)) })}
          />
        </>
      )}
      {yt.query.length >= MIN_YOUTUBE_QUERY && (yt.forced || res?.query === yt.query) && (
        <>
          <h2 className="section-title">{t("search.youtubeHeading")}</h2>
          <YouTubeResults key={yt.query} query={yt.query} auto={yt.forced || (res?.query === yt.query && !!empty)} />
        </>
      )}
      </div>}
    </>
  );
}
