import type { ReactNode } from "react";
import { Link } from "react-router";
import type { Episode } from "../../api/types";
import type { EpisodeOrder } from "../../channels/episodeQueue";
import { useEpisodes } from "../../channels/EpisodesProvider";
import { Cover } from "../../components/Cover";
import { PlayIcon, ShuffleIcon } from "../../components/icons";
import { duration, formatDate } from "../../format";
import { useT } from "../../i18n/i18n";

/** The status line of an episode that can't be played yet ("" when it can). */
export function episodeStatus(ep: Episode, t: ReturnType<typeof useT>): string {
  switch (ep.audio?.status) {
    case "done":
      return "";
    case "queued":
      return t("episodes.queued");
    case "downloading":
      return t("episodes.downloading", { pct: Math.round(ep.audio.progress) });
    case "failed":
      return ep.audio.error === "lark:too_long" ? t("episodes.tooLong") : t("episodes.failed");
    case "expired":
      return t("episodes.expired");
  }
  return t("episodes.notDownloaded");
}

const playableOf = (items: Episode[]) => items.filter((e) => e.audio?.status === "done");

/**
 * ▶ 全部播放 / 🔀 随机 for a list: its downloaded episodes as the episode
 * queue, played ones left out (all of them, when every one is played).
 */
export function PlayAll({ items, order = "newest" }: { items: Episode[]; order?: Exclude<EpisodeOrder, "shuffle"> }) {
  const t = useT();
  const eps = useEpisodes();
  const playable = playableOf(items);
  const includePlayed = playable.every((e) => e.played);
  return (
    <div className="play-all" data-no-music-prime="">
      <button className="pill-btn" aria-label={t("episodes.playAll")} disabled={playable.length === 0} onClick={() => eps.playList(playable, -1, { order, includePlayed })}>
        <PlayIcon size={14} />
        <span>{t("episodes.playAll")}</span>
      </button>
      <button className="pill-btn" aria-label={t("episodes.shuffleAll")} disabled={playable.length === 0} onClick={() => eps.playList(playable, -1, { order: "shuffle", includePlayed })}>
        <ShuffleIcon />
        <span>{t("episodes.order.shuffle")}</span>
      </button>
    </div>
  );
}

/**
 * Episode rows; ▶ plays that episode and queues the rest of the list's
 * downloaded episodes (queueFrom, when the rows show only part of it) in
 * the list's order.
 */
export function EpisodeList({ items, extra, queueFrom, order = "newest" }: { items: Episode[]; extra?: (ep: Episode) => ReactNode; queueFrom?: Episode[]; order?: Exclude<EpisodeOrder, "shuffle"> }) {
  const t = useT();
  const eps = useEpisodes();
  const playable = playableOf(queueFrom ?? items);
  return (
    <ul className="rows episodes">
      {items.map((ep) => {
        const status = episodeStatus(ep, t);
        const pct = ep.duration_s > 0 && ep.position_s > 0 && !ep.played ? Math.min(100, (ep.position_s / ep.duration_s) * 100) : 0;
        return (
          <li key={ep.video_id} className="row episode-row">
            <Link to={`/episodes/${ep.video_id}`} className="episode-link">
              <Cover seed={ep.channel_id} label={ep.channel_title} size={48} src={ep.thumbnail} />
              <span className="yt-text">
                <span className="ellipsis">
                  {!ep.played && !status && <span className="unplayed-dot" aria-hidden="true" />}
                  {ep.title}
                </span>
                <span className="ellipsis muted small">
                  {ep.channel_title} · {formatDate(ep.published_at)}
                  {ep.duration_s > 0 && ` · ${duration(ep.duration_s * 1000)}`}
                  {ep.played && ` · ${t("episodes.played")}`}
                </span>
                {status && <span className="ellipsis muted small">{status}</span>}
                {pct > 0 && <span className="episode-progress"><span style={{ width: `${pct}%` }} /></span>}
              </span>
            </Link>
            <span className="job-actions">
              {extra?.(ep)}
              {!status && (
                <button className="icon" data-no-music-prime="" aria-label={t("episodes.listen")} onClick={() => eps.playList(playable, playable.findIndex((e) => e.video_id === ep.video_id), { order })}>▶</button>
              )}
            </span>
          </li>
        );
      })}
    </ul>
  );
}
