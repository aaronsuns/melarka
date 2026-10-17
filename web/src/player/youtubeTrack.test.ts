import { isYouTube, libraryIds, trackCoverUrl, trackStreamUrl, youtubeTrack } from "./youtubeTrack";
import type { Track, YTVideo } from "../api/types";

const v = (id: string): YTVideo => ({ id, title: "流年", channel: "王菲", url: "", thumbnail: "", duration_s: 263 });

test("a YouTube track: stable negative id, its own stream and cover, left out of library ids", () => {
  const a = youtubeTrack(v("mjuS9shGYhE"));
  expect(a.id).toBeLessThan(0);
  expect(youtubeTrack(v("mjuS9shGYhE")).id).toBe(a.id);
  expect(youtubeTrack(v("Nkwwb2v2rAE")).id).not.toBe(a.id);
  expect(isYouTube(a)).toBe(true);
  expect(trackStreamUrl(a, "high")).toBe("/api/v1/previews/by-video/mjuS9shGYhE/stream?title=%E6%B5%81%E5%B9%B4&channel=%E7%8E%8B%E8%8F%B2&duration_s=263");
  expect(trackCoverUrl(a)).toBe("/api/v1/videos/mjuS9shGYhE/thumbnail");
  const lib = { ...a, id: 7, youtube_id: undefined } as Track;
  expect(isYouTube(lib)).toBe(false);
  expect(trackStreamUrl(lib, "high")).toBe("/api/v1/tracks/7/stream?quality=high");
  expect(trackCoverUrl(lib, 1000)).toBe("/api/v1/tracks/7/cover?size=1000");
  expect(libraryIds([a, lib])).toEqual([7]);
});
