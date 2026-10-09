import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { Cover } from "../components/Cover";
import { duration } from "../format";
import { useT } from "../i18n/i18n";
import { QueueRows, entryKeys } from "../player/QueueSheet";
import { SleepTimerMenu } from "../player/SleepTimerMenu";
import { EPISODE_ORDERS } from "./episodeQueue";
import { EPISODE_RATES, useEpisodes, useEpisodesProgress } from "./EpisodesProvider";

/**
 * The episode queue: the current one marked, a tap jumps, rows drag to reorder and swipe (or ✕) away;
 * its order and "include played" (either rebuilds the queue from its list).
 */
function EpisodeQueueSheet() {
  const t = useT();
  const ep = useEpisodes();
  // By id: saving progress replaces the episode objects every few seconds.
  const keys = entryKeys(ep.queue, (e) => e.video_id);
  const list = useRef<HTMLDivElement>(null);
  useEffect(() => {
    list.current?.querySelector<HTMLElement>("[aria-current]")?.scrollIntoView?.({ block: "nearest" });
  }, []);
  return (
    <div className="episode-queue" ref={list}>
      <div className="seg" role="group" aria-label={t("episodes.order")}>
        {EPISODE_ORDERS.map((o) => (
          <button key={o} aria-pressed={ep.order === o} onClick={() => ep.setOrder(o)}>{t(`episodes.order.${o}`)}</button>
        ))}
      </div>
      <label className="check">
        <input type="checkbox" className="switch" checked={ep.includePlayed} onChange={(e) => ep.setIncludePlayed(e.target.checked)} />
        {t("episodes.includePlayed")}
      </label>
      <QueueRows
        id="episode-queue"
        label={t("episodes.queue")}
        items={ep.queue.map((q, i) => ({
          key: keys[i],
          title: q.title,
          sub: `${q.channel_title}${q.played ? ` · ${t("episodes.played")}` : ""}`,
          current: i === ep.index,
        }))}
        onJump={(i) => ep.jump(i)}
        onMove={ep.move}
        onRemove={ep.removeAt}
        footer={ep.queue.length === 0 && <li className="muted">{t("episodes.queueEmpty")}</li>}
      />
    </div>
  );
}

/**
 * Full-screen episode player (opened from the episode mini player): looks
 * like music's Now Playing. Every control sits inside data-no-music-prime,
 * so no tap here primes music.
 */
export function EpisodeNowPlaying({ onClose }: { onClose: () => void }) {
  const t = useT();
  const ep = useEpisodes();
  const progress = useEpisodesProgress();
  const [showQueue, setShowQueue] = useState(false);
  const [scrub, setScrub] = useState<number | null>(null);
  const sliderRef = useRef<HTMLInputElement>(null);
  const scrubEnd = useRef<ReturnType<typeof setTimeout> | null>(null);
  const cur = ep.current;
  const seekRef = useRef(ep.seek);
  seekRef.current = ep.seek;

  // Like music's seek slider: onInput only previews; the native "change"
  // (the end of the gesture) commits one seek.
  useEffect(() => {
    const el = sliderRef.current;
    if (!el) return;
    const commit = (e: Event) => {
      seekRef.current(Number((e.target as HTMLInputElement).value));
      setScrub(null);
    };
    el.addEventListener("change", commit);
    return () => el.removeEventListener("change", commit);
  }, [cur?.video_id]);
  useEffect(() => {
    setScrub(null);
  }, [cur?.video_id]);
  useEffect(
    () => () => {
      if (scrubEnd.current) clearTimeout(scrubEnd.current);
    },
    [],
  );
  // A drag that ends where it began fires no "change": drop the preview on
  // the next tick (after a "change" from the same release has committed).
  function endScrub() {
    if (scrubEnd.current) clearTimeout(scrubEnd.current);
    scrubEnd.current = setTimeout(() => {
      scrubEnd.current = null;
      setScrub(null);
    }, 0);
  }

  if (!cur) return null;
  const dur = progress.duration > 0 ? progress.duration : cur.duration_s;
  const pos = scrub ?? progress.position;
  return (
    <div className="now episode-now" role="dialog" aria-label={t("episodes.nowPlaying")} data-no-music-prime="">
      <div className="now-top">
        <button className="icon" aria-label={t("now.close")} onClick={onClose}>⌄</button>
        <span className="badge">{t("channels.title")}</span>
        <span className="now-top-end">
          <button className="icon" aria-label={t("episodes.queue")} aria-pressed={showQueue} onClick={() => setShowQueue((s) => !s)}>☰</button>
          <SleepTimerMenu />
        </span>
      </div>
      {showQueue ? (
        <EpisodeQueueSheet />
      ) : (
        <div className="now-art">
          <Cover seed={cur.channel_id} label={cur.channel_title} size={320} src={cur.thumbnail} />
        </div>
      )}
      <div className="now-meta">
        <h2 className="ellipsis"><Link to={`/episodes/${cur.video_id}`} onClick={onClose}>{cur.title}</Link></h2>
        <p className="ellipsis muted"><Link to={`/channels/${cur.channel_id}`} onClick={onClose}>{cur.channel_title}</Link></p>
      </div>
      {ep.error && <p className="error">{ep.error}</p>}
      <input
        ref={sliderRef}
        type="range"
        aria-label={t("now.position")}
        min={0}
        max={Math.max(1, Math.round(dur))}
        step={1}
        value={Math.round(pos)}
        onInput={(e) => setScrub(Number((e.target as HTMLInputElement).value))}
        onPointerUp={endScrub}
        onTouchEnd={endScrub}
        onBlur={endScrub}
      />
      <div className="now-times muted small"><span>{duration(pos * 1000)}</span><span>-{duration(Math.max(0, dur - pos) * 1000)}</span></div>
      <div className="now-controls">
        <button className="icon big" aria-label={t("episodes.previous")} onClick={ep.prev}>⏮</button>
        <button className="icon big" aria-label={t("episodes.back15")} onClick={() => ep.skip(-15)}>↺</button>
        <button className="icon huge" aria-label={ep.playing ? t("common.pause") : t("common.play")} onClick={ep.toggle}>{ep.playing ? "⏸" : "▶"}</button>
        <button className="icon big" aria-label={t("episodes.forward30")} onClick={() => ep.skip(30)}>↻</button>
        <button className="icon big" aria-label={t("episodes.next")} disabled={ep.index >= ep.queue.length - 1} onClick={ep.next}>⏭</button>
      </div>
      <div className="segmented now-speed" role="group" aria-label={t("episodes.speed")}>
        {EPISODE_RATES.map((r) => (
          <button key={r} aria-pressed={ep.rate === r} onClick={() => ep.setRate(r)}>{r}×</button>
        ))}
      </div>
    </div>
  );
}
