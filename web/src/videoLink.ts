// Recognises a pasted YouTube or Bilibili link. Mirrors the server's rules
// (internal/ytdlp: ValidURL host allow-list, VideoIDFromURL, IsBilibiliListURL)
// so a link is only treated as one when the server would accept it.
// b23.tv short links are not accepted: the server never follows a redirect.
import { parseYouTubeLink, watchUrl } from "./youtubeLink";

export type VideoSource = "youtube" | "bilibili";

export interface VideoLink {
  source: VideoSource;
  videoId: string | null;
  listId: string | null; // a YouTube list id
  listUrl: string | null; // a Bilibili list page, posted as is
  isMix: boolean;
}

const BILI_HOSTS = new Set(["bilibili.com", "www.bilibili.com", "m.bilibili.com", "space.bilibili.com"]);
const BILI_ID = /^BV[0-9A-Za-z]{10}$/;

export const sourceOfId = (id: string): VideoSource => (BILI_ID.test(id) ? "bilibili" : "youtube");
export const bilibiliWatchUrl = (id: string) => `https://www.bilibili.com/video/${id}`;
export const watchUrlFor = (id: string) => (sourceOfId(id) === "bilibili" ? bilibiliWatchUrl(id) : watchUrl(id));

function parseBilibiliLink(raw: string): VideoLink | null {
  let u: URL;
  try {
    u = new URL(raw.trim());
  } catch {
    return null;
  }
  const host = u.hostname.toLowerCase();
  if ((u.protocol !== "http:" && u.protocol !== "https:") || u.port !== "" || !BILI_HOSTS.has(host)) return null;
  const segs = u.pathname.replace(/^\/+|\/+$/g, "").split("/");
  const link = { source: "bilibili" as const, videoId: null, listId: null, listUrl: null, isMix: false };
  if (host === "space.bilibili.com") return segs.length >= 2 ? { ...link, listUrl: u.toString() } : null;
  if (segs.length === 2 && segs[0] === "video" && BILI_ID.test(segs[1])) return { ...link, videoId: segs[1] };
  if (segs[0] === "medialist" || segs[0] === "list") return { ...link, listUrl: u.toString() };
  return null;
}

export function parseVideoLink(raw: string): VideoLink | null {
  const yt = parseYouTubeLink(raw);
  if (yt) return { source: "youtube", ...yt, listUrl: null };
  return parseBilibiliLink(raw);
}
