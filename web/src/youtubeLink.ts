// Recognises a pasted YouTube link. Mirrors the server's rules
// (internal/ytdlp: ValidURL host allow-list, VideoIDFromURL, ListIDFromURL,
// IsMixID) so a link is only treated as one when the server would accept it.

export interface YouTubeLink {
  videoId: string | null;
  listId: string | null;
  isMix: boolean;
}

const HOSTS = new Set(["youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com", "youtu.be"]);
const VIDEO_ID = /^[A-Za-z0-9_-]{11}$/;
const LIST_ID = /^[A-Za-z0-9_-]{12,64}$/;
const PATH_ID_PREFIXES = ["shorts", "live", "embed"];

export const watchUrl = (videoId: string) => `https://www.youtube.com/watch?v=${videoId}`;
export const playlistUrl = (listId: string) => `https://www.youtube.com/playlist?list=${listId}`;

export function parseYouTubeLink(raw: string): YouTubeLink | null {
  let u: URL;
  try {
    u = new URL(raw.trim());
  } catch {
    return null;
  }
  if ((u.protocol !== "http:" && u.protocol !== "https:") || u.port !== "" || !HOSTS.has(u.hostname.toLowerCase())) {
    return null;
  }
  const segs = u.pathname.replace(/^\/+|\/+$/g, "").split("/");
  let id = "";
  if (u.hostname.toLowerCase() === "youtu.be") id = segs[0];
  else if (u.searchParams.get("v")) id = u.searchParams.get("v")!;
  else if (segs.length === 2 && PATH_ID_PREFIXES.includes(segs[0])) id = segs[1];
  const videoId = VIDEO_ID.test(id) ? id : null;
  const list = u.searchParams.get("list") ?? "";
  const listId = LIST_ID.test(list) ? list : null;
  const isMix = listId !== null && listId.startsWith("RD");
  // A mix is only ever "this song": on its own there is nothing to offer.
  if (videoId === null && (listId === null || isMix)) return null;
  return { videoId, listId, isMix };
}
