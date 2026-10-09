import { act, render, waitFor } from "@testing-library/react";
import { PlayerProvider, usePlayer, type Player } from "./PlayerProvider";
import { FakeAudio, mockFetch } from "../test/setup";
import type { Track } from "../api/types";
import { connectOffline, isPlayerBuffering } from "../offline/bridge";

const tr = (id: number) => ({ id, title: `t${id}`, artist: "a", album: "b", duration_ms: 200000 }) as Track;

function setup() {
  mockFetch({
    "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] } }),
    "PUT /api/v1/queue": () => ({ body: {} }),
    "POST /api/v1/events/play": () => ({ body: { accepted: 1 } }),
    "GET /api/v1/radio/next": () => ({ body: [] }),
  });
  const audio = new FakeAudio();
  let p!: Player;
  function Probe() {
    p = usePlayer();
    return null;
  }
  render(<PlayerProvider audio={audio as unknown as HTMLAudioElement}><Probe /></PlayerProvider>);
  return { audio, player: () => p };
}

let disconnect: (() => void) | null = null;
afterEach(() => {
  disconnect?.();
  disconnect = null;
});

test("offline, the queue skips tracks that aren't cached, with a notice", async () => {
  const cached = new Set([1, 4]);
  disconnect = connectOffline({ played: () => {}, playable: (id) => cached.has(id) });
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2), tr(3), tr(4)], 0));
  act(() => audio.fire("ended"));
  await waitFor(() => expect(player().current?.id).toBe(4));
  expect(audio.src).toContain("/tracks/4/");
  expect(player().notice).toBeTruthy();
});

test("tapping an uncached track offline starts the next cached one instead", () => {
  disconnect = connectOffline({ played: () => {}, playable: (id) => id === 3 });
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2), tr(3)], 1));
  expect(audio.src).toContain("/tracks/3/");
  expect(player().current?.id).toBe(3);
  expect(audio.play).toHaveBeenCalledTimes(1);
});

test("with nothing cached ahead, it tries the track as before", () => {
  disconnect = connectOffline({ played: () => {}, playable: () => false });
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2)], 0));
  expect(audio.src).toContain("/tracks/1/");
  expect(player().notice).toBeNull();
});

test("tells the offline cache what was played, and whether to the end", async () => {
  const played: [number, boolean][] = [];
  disconnect = connectOffline({ played: (t, f) => played.push([t.id, f]), playable: () => true });
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2)], 0));
  act(() => audio.fire("ended"));
  await waitFor(() => expect(player().current?.id).toBe(2));
  expect(played).toEqual([[1, false], [1, true], [2, false]]);
});

test("reports buffering of the current track until it can play through", () => {
  const { audio, player } = setup();
  expect(isPlayerBuffering()).toBe(false);
  act(() => player().playList([tr(1)], 0));
  expect(isPlayerBuffering()).toBe(true);
  act(() => audio.fire("canplaythrough"));
  expect(isPlayerBuffering()).toBe(false);
  act(() => audio.fire("waiting"));
  expect(isPlayerBuffering()).toBe(true);
  act(() => audio.fire("error"));
  expect(isPlayerBuffering()).toBe(false);
});

test("the notice shows in the mini player", async () => {
  const { MiniPlayer } = await import("./MiniPlayer");
  disconnect = connectOffline({ played: () => {}, playable: (id) => id === 2 });
  mockFetch({
    "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] } }),
    "PUT /api/v1/queue": () => ({ body: {} }),
    "POST /api/v1/events/play": () => ({ body: { accepted: 1 } }),
    "GET /api/v1/radio/next": () => ({ body: [] }),
    "GET /api/v1/tracks/2/lyrics": () => ({ body: { found: false } }),
  });
  const audio = new FakeAudio();
  let p!: Player;
  function Probe() {
    p = usePlayer();
    return null;
  }
  const { findByRole } = render(<PlayerProvider audio={audio as unknown as HTMLAudioElement}><Probe /><MiniPlayer /></PlayerProvider>);
  act(() => p.playList([tr(1), tr(2)], 0));
  expect((await findByRole("status")).textContent).toBe("离线中：已跳过未缓存的歌曲");
});

test("the media element's own emptied event (from assigning src) doesn't end buffering", () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1)], 0));
  act(() => audio.fire("emptied"));
  expect(isPlayerBuffering()).toBe(true);
});

test("a finished song is reported while the next one counts as buffering", async () => {
  const seen: [number, boolean, boolean][] = [];
  disconnect = connectOffline({ played: (t, f) => seen.push([t.id, f, isPlayerBuffering()]), playable: () => true });
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2)], 0));
  act(() => audio.fire("canplaythrough"));
  act(() => audio.fire("ended"));
  await waitFor(() => expect(player().current?.id).toBe(2));
  expect(seen).toContainEqual([1, true, true]);
});

test("a stall while playing counts as buffering; pausing ends it", () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1)], 0));
  act(() => audio.fire("canplaythrough"));
  act(() => audio.fire("stalled"));
  expect(isPlayerBuffering()).toBe(true);
  act(() => player().pause());
  expect(isPlayerBuffering()).toBe(false);
});
