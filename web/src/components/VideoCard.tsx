import { Link } from "react-router";
import type { YTVideo } from "../api/types";
import { duration } from "../format";
import { Cover } from "./Cover";

/** Melarka's thumbnail proxy for a video: the browser never asks YouTube directly (some networks block i.ytimg.com). */
export function videoThumbnail(id: string): string {
  return `/api/v1/videos/${encodeURIComponent(id)}/thumbnail`;
}

/** A 历史 / 推荐 row as the card a search returns. */
export function asVideo(v: { video_id: string; title: string; channel: string; channel_id: string; duration_s: number; thumbnail: string }): YTVideo {
  return {
    id: v.video_id, title: v.title, channel: v.channel, channel_id: v.channel_id, duration_s: v.duration_s, thumbnail: v.thumbnail,
    url: `https://www.youtube.com/watch?v=${v.video_id}`,
  };
}

/**
 * One 视频 card: the thumbnail in a 16:9 box (the initials tile when it has
 * none), then the title, channel and why it is suggested. All of it is
 * YouTube's text, rendered as text. Opening it hands the card to the watch
 * page, which starts the preview before anything else loads.
 */
export function VideoCard({ v, reason }: { v: YTVideo; reason?: string }) {
  // Only Lark's own proxy path is loaded; anything else (a YouTube URL) is rebuilt as one.
  const src = v.thumbnail.startsWith("/api/v1/videos/") ? v.thumbnail : videoThumbnail(v.id);
  return (
    <div className="video-card" data-no-music-prime="">
      <Link to={`/watch/${encodeURIComponent(v.id)}`} state={{ video: v }}>
        <div className="video-thumb">
          <Cover seed={v.channel_id || v.channel || v.id} label={v.channel || v.title} size={64} src={src} />
          {v.duration_s > 0 && <span className="video-duration">{duration(v.duration_s * 1000)}</span>}
        </div>
        <span className="video-title">{v.title}</span>
        <span className="muted small ellipsis">{v.channel}</span>
        {reason && <span className="muted small video-reason">{reason}</span>}
      </Link>
    </div>
  );
}
