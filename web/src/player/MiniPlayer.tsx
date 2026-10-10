import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Cover, coverUrl } from "../components/Cover";
import { BackwardFillIcon, ForwardFillIcon, PauseFillIcon, PlayFillIcon } from "../components/icons";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { CurrentLine, syncedLines, useLyrics } from "./Lyrics";
import { NowPlaying } from "./NowPlaying";
import { usePlayer, usePlayerProgress } from "./PlayerProvider";
import { useTrackSwipe } from "./swipe";

export function MiniPlayer() {
  const t = useT();
  const p = usePlayer();
  const { position, duration } = usePlayerProgress();
  const [open, setOpen] = useState(false);
  const track = p.current;
  // A sideways swipe on the cover and title: left = next, right = previous.
  const swipeRef = useTrackSwipe(".mini-info", { next: p.next, prev: p.prev });
  // Fetched here, not in Now Playing: the mini player shows the current line
  // too, and Now Playing opens straight onto lyrics it already has.
  const { lyrics, reload } = useLyrics(track?.id);
  // The queue can empty out from under an open NowPlaying (e.g. removing
  // the last track) — without this the overlay would linger, dialog-shaped
  // but with nothing to show.
  useEffect(() => {
    if (!track) setOpen(false);
  }, [track]);
  if (!track) {
    if (!p.ready) return null; // boot (resume) still settling: a shortcut would race it
    const go = () => p.shuffleFavorites().catch((e) => p.showNotice(errorMessage(e, "home.shuffleUnavailable")));
    // Nothing loaded: offer the most-used action instead of an empty bar.
    return (
      <div className="mini-empty">
        {p.notice && <p className="mini-notice" role="status">{p.notice}</p>}
        <button type="button" className="mini-shuffle" onClick={() => void go()}>🔀 {t("home.shuffleFavorites")}</button>
      </div>
    );
  }
  const pct = duration > 0 ? Math.min(100, (position / duration) * 100) : 0;
  return (
    <>
      <div className="mini" ref={swipeRef}>
        <div className="mini-track"><div className="mini-progress" style={{ width: `${pct}%` }} /></div>
        {p.notice && <p className="mini-notice" role="status">{p.notice}</p>}
        {p.needsTap && (
          // Deliberately no onClick, and not inside .mini-info: tapping it is
          // just "a tap anywhere", which PlayerProvider's capture-phase
          // prime() turns into the start — exactly once. A handler here would
          // be a second start (or, via toggle(), a pause).
          <button type="button" className="mini-tap">{t("player.tapToPlayFavorites")}</button>
        )}
        <button className="mini-info" data-swipe-surface="" onClick={() => setOpen(true)}>
          <Cover seed={track.album || track.title} label={track.title} size={44} src={coverUrl("track", track.id)} />
          <span className="track-text">
            <span className="ellipsis">{track.title}</span>
            <span className="ellipsis muted small">{p.error ?? <CurrentLine lines={syncedLines(lyrics)} offset={lyrics?.offset_ms ?? 0} fallback={track.artist || t("common.unknownArtist")} />}</span>
          </span>
        </button>
        {/* ⏮ as in Now Playing: the previous track in the first 3 s, else a restart. */}
        <button className="icon mini-btn" aria-label={t("common.previous")} onClick={p.prev}><BackwardFillIcon size={22} /></button>
        <button className="icon mini-btn mini-play" aria-label={p.playing ? t("common.pause") : t("common.play")} onClick={p.toggle}>
          {p.playing ? <PauseFillIcon size={26} /> : <PlayFillIcon size={26} />}
        </button>
        <button className="icon mini-btn" aria-label={t("common.next")} onClick={p.next}><ForwardFillIcon size={22} /></button>
      </div>
      {/* At the top of the page: inside the bottom bars (a stacking context) anything in the page could paint over it. */}
      {open && createPortal(<NowPlaying lyrics={lyrics} reloadLyrics={reload} onClose={() => setOpen(false)} />, document.body)}
    </>
  );
}
