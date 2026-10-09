import { useEffect, useState } from "react";
import { Cover } from "../components/Cover";
import { useT } from "../i18n/i18n";
import { EpisodeNowPlaying } from "./EpisodeNowPlaying";
import { useEpisodes, useEpisodesProgress } from "./EpisodesProvider";

/** The mini player while a channel episode owns playback (replaces the music one). */
export function EpisodeMini() {
  const t = useT();
  const ep = useEpisodes();
  const { position, duration } = useEpisodesProgress();
  const [open, setOpen] = useState(false);
  const cur = ep.current;
  const shown = !!cur && ep.active;
  useEffect(() => {
    if (!shown) setOpen(false);
  }, [shown]);
  if (!cur || !shown) return null;
  const pct = duration > 0 ? Math.min(100, (position / duration) * 100) : 0;
  return (
    <>
    <div className="mini episode-mini" data-no-music-prime="">
      <div className="mini-progress" style={{ width: `${pct}%` }} />
      {ep.error && <p className="mini-notice" role="status">{ep.error}</p>}
      <button className="mini-info" aria-label={t("episodes.openNowPlaying", { title: cur.title })} onClick={() => setOpen(true)}>
        <Cover seed={cur.channel_id} label={cur.channel_title} size={44} src={cur.thumbnail} />
        <span className="track-text">
          <span className="ellipsis">{cur.title}</span>
          <span className="ellipsis muted small">{cur.channel_title}</span>
        </span>
      </button>
      <button className="icon" aria-label={t("episodes.back15")} onClick={() => ep.skip(-15)}>↺</button>
      <button className="icon" aria-label={ep.playing ? t("common.pause") : t("common.play")} onClick={ep.toggle}>{ep.playing ? "⏸" : "▶"}</button>
      <button className="icon" aria-label={t("episodes.forward30")} onClick={() => ep.skip(30)}>↻</button>
      <button className="icon" aria-label={t("episodes.close")} onClick={ep.close}>✕</button>
    </div>
    {open && <EpisodeNowPlaying onClose={() => setOpen(false)} />}
    </>
  );
}
