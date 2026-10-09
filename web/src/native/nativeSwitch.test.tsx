import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import type { Track } from "../api/types";
import { AuthProvider } from "../auth/AuthProvider";
import { useEpisodes } from "../channels/EpisodesProvider";
import { offlineOnFrom } from "../offline/register";
import { usePlayer } from "../player/PlayerProvider";
import { resetSessionOwner } from "../player/sessionOwner";
import { renderWithApp } from "../test/render";
import { installFakeNative, type FakeNative } from "./fakeNative";

const track1 = { id: 1, title: "t1", artist: "a", album: "b", duration_ms: 200_000 } as Track;
const track2 = { id: 2, title: "t2", artist: "a", album: "b", duration_ms: 180_000 } as Track;

function PlayFirstButton() {
  const p = usePlayer();
  return <button onClick={() => p.playList([track1, track2], 0)}>play first</button>;
}

let n: FakeNative | null = null;
afterEach(() => {
  n?.uninstall();
  n = null;
  resetSessionOwner();
  vi.restoreAllMocks();
});

const calls = (f: ReturnType<typeof renderWithApp>["f"]) =>
  f.mock.calls.map(([u, init]) => `${(init?.method ?? "GET").toUpperCase()} ${String(u).split("?")[0]}`);

it("without window.larkNative the web transport is used (an <audio> src is set)", async () => {
  const { audio } = renderWithApp(<PlayFirstButton />);
  await userEvent.click(await screen.findByRole("button", { name: "play first" }));
  expect(audio.src).toContain("/api/v1/tracks/1/stream");
});

it("with window.larkNative no audio element is created and nothing is posted to /events/play or PUT /queue", async () => {
  n = installFakeNative();
  const audioCtor = vi.spyOn(window, "Audio");
  const { audio, f } = renderWithApp(<PlayFirstButton />);
  await userEvent.click(await screen.findByRole("button", { name: "play first" }));
  expect(audioCtor).not.toHaveBeenCalled();
  expect(audio.src).toBe("");
  expect(audio.play).not.toHaveBeenCalled();
  expect(n.post).toHaveBeenCalledWith(expect.objectContaining({ type: "setQueue", kind: "track", index: 0, positionMs: 0, play: true }));
  // The debounced queue save and the event flush would have run by now.
  await act(() => new Promise((r) => setTimeout(r, 1200)));
  expect(calls(f).filter((c) => c.startsWith("POST /api/v1/events/play") || c.startsWith("PUT /api/v1/queue") || c.startsWith("GET /api/v1/queue"))).toEqual([]);
});

it("with window.larkNative an episode never gets an <audio> src either", async () => {
  n = installFakeNative();
  function PlayEpisode() {
    const e = useEpisodes();
    const ep = { video_id: "v1", channel_id: "c", channel_title: "C", title: "E1", published_at: 1, duration_s: 600, kind: "video", thumbnail: "", audio: null, video: null, position_s: 0, played: false, kept: false };
    return <button onClick={() => e.play([ep], 0)}>play episode</button>;
  }
  const { episodeAudio, audio } = renderWithApp(<PlayEpisode />);
  await userEvent.click(await screen.findByRole("button", { name: "play episode" }));
  expect(episodeAudio.src).toBe("");
  expect(audio.src).toBe("");
  expect(n.post).toHaveBeenCalledWith(expect.objectContaining({ type: "setQueue", kind: "episode", play: true }));
});

it("native mode does not register the service worker", () => {
  expect(offlineOnFrom({})).toBe(true);
  expect(offlineOnFrom({ offline_cache: false })).toBe(false);
  n = installFakeNative();
  expect(offlineOnFrom({})).toBe(false);
  expect(offlineOnFrom({ offline_cache: true })).toBe(false);
});

it("without window.larkNative nothing listens for native events", async () => {
  const add = vi.spyOn(window, "addEventListener");
  const { audio } = renderWithApp(<PlayFirstButton />);
  render(<AuthProvider>x</AuthProvider>);
  await userEvent.click(await screen.findByRole("button", { name: "play first" }));
  expect(audio.src).toContain("/stream");
  expect(add.mock.calls.filter(([type]) => type === "lark-native")).toEqual([]);
});
