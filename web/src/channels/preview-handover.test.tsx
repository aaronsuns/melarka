import { act, fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";
import { usePlayer, type Player } from "../player/PlayerProvider";
import { usePreviews } from "./PreviewProvider";
import { renderWithApp } from "../test/render";
import type { Track } from "../api/types";

let p!: Player;
let preview!: ReturnType<typeof usePreviews>;
function Grab() { p = usePlayer(); preview = usePreviews(); return null; }
const song = (id: number, title: string) =>
  ({ id, title, artist: "歌手", album: "", duration_ms: 200000, codec: "aac", lossless: false, bitrate: 256, favorite: true, disliked: false, broken: false, broken_reason: "" }) as Track;
const pv = (extra: object = {}) => ({ id: 2, video_id: "recvideo001", media: "audio", status: "done", title: "新歌", channel: "歌手", duration_s: 200,
  stream_url: "/api/v1/previews/2/stream", progress: 100, error: "", ...extra });
const target = { videoId: "recvideo001", media: "audio" as const, title: "新歌", channel: "歌手", durationS: 200, keepTo: "music" as const };

test("保留到音乐 while it plays: the song goes on in the music player from the same second, then shuffled favorites", async () => {
  const { audio, previewAudio } = renderWithApp(<Grab />, {
    routes: {
      "POST /api/v1/previews": () => ({ status: 201, body: pv() }),
      "GET /api/v1/previews/2": () => ({ body: pv() }),
      "POST /api/v1/previews/2/keep": () => ({ body: { job: { id: 7, track_id: 42, status: "done" } } }),
      "GET /api/v1/tracks/42": () => ({ body: song(42, "新歌") }),
    },
  });
  act(() => preview.open(target));
  const sheet = await screen.findByRole("dialog", { name: "新歌" });
  await vi.waitFor(() => expect(previewAudio.src).toContain("/previews/2/stream"));
  previewAudio.currentTime = 73.5;
  act(() => { previewAudio.paused = false; previewAudio.fire("play"); previewAudio.fire("playing"); });
  await userEvent.click(await within(sheet).findByRole("button", { name: "保留到音乐" }));
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/42/stream"));
  expect(p.queue.tracks[0].id).toBe(42);
  expect(p.queue.source).toBe("favorites");
  await vi.waitFor(() => expect(audio.currentTime).toBeCloseTo(73.5, 0));
  expect(previewAudio.pause).toHaveBeenCalled();
  expect(screen.queryByRole("dialog", { name: "新歌" })).toBeNull();
});

test("closing a preview without keeping it brings back the music that was playing", async () => {
  const { audio, previewAudio } = renderWithApp(<Grab />, {
    routes: {
      "POST /api/v1/previews": () => ({ status: 201, body: pv() }),
      "GET /api/v1/previews/2": () => ({ body: pv() }),
    },
  });
  act(() => p.playList([song(1, "原来的歌")], 0));
  act(() => { audio.paused = false; audio.fire("playing"); });
  expect(p.playing).toBe(true);
  act(() => preview.open(target));
  const sheet = await screen.findByRole("dialog", { name: "新歌" });
  await vi.waitFor(() => expect(previewAudio.src).toContain("/previews/2/stream"));
  act(() => { previewAudio.paused = false; previewAudio.fire("play"); previewAudio.fire("playing"); });
  await vi.waitFor(() => expect(audio.pause).toHaveBeenCalled()); // music stepped aside
  const plays = (audio.play as ReturnType<typeof vi.fn>).mock.calls.length;
  fireEvent.click(within(sheet).getByRole("button", { name: "关闭试听" }));
  await vi.waitFor(() => expect((audio.play as ReturnType<typeof vi.fn>).mock.calls.length).toBeGreaterThan(plays));
  expect(audio.src).toContain("/tracks/1/stream");
});
