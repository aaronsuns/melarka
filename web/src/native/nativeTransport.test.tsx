import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { Episode, Track } from "../api/types";
import { AuthProvider, useAuth } from "../auth/AuthProvider";
import { EpisodesProvider, useEpisodes, useEpisodesProgress, type EpisodesPlayer } from "../channels/EpisodesProvider";
import { loadStoredQueue, saveStoredQueue } from "../channels/episodeQueue";
import SettingsPage from "../pages/SettingsPage";
import { MiniPlayer } from "../player/MiniPlayer";
import { PlayerProvider, usePlayer, usePlayerProgress, type Player } from "../player/PlayerProvider";
import { claimSession, ownsSession, resetSessionOwner } from "../player/sessionOwner";
import { renderWithApp } from "../test/render";
import { FakeAudio, mockFetch } from "../test/setup";
import { installFakeNative, stateEvent, type FakeNative } from "./fakeNative";
import { episodeItem, fromItem, trackItem } from "./items";

const tr = (id: number, extra: Partial<Track> = {}) => ({ id, title: `song ${id}`, artist: "a", album: "b", duration_ms: 200_000, favorite: false, ...extra }) as Track;
const track1 = tr(1);
const track2 = tr(2);
const ep = (id: string, extra: Partial<Episode> = {}): Episode => ({
  video_id: id, channel_id: "c", channel_title: "Chan", title: `ep ${id}`, published_at: 100, duration_s: 3600, kind: "video",
  thumbnail: "", audio: null, video: null, position_s: 0, played: false, kept: false, ...extra,
});

let n!: FakeNative;
beforeEach(() => {
  n = installFakeNative();
});
afterEach(() => {
  n.uninstall();
  resetSessionOwner();
  vi.useRealTimers();
});

let p!: Player;
let prog!: { position: number; duration: number };
function Probe() {
  p = usePlayer();
  prog = usePlayerProgress();
  return null;
}
let e!: EpisodesPlayer;
let eprog!: { position: number; duration: number };
function EProbe() {
  e = useEpisodes();
  eprog = useEpisodesProgress();
  return null;
}

function renderPlayer(children: ReactNode = <Probe />, props: { carLyrics?: boolean } = {}) {
  const audio = new FakeAudio();
  const episodeAudio = new FakeAudio();
  const r = render(
    <PlayerProvider audio={audio as unknown as HTMLAudioElement} userId={1} onOpen="shuffle_favorites" carLyrics={props.carLyrics}>
      <EpisodesProvider audio={episodeAudio as unknown as HTMLAudioElement} userId={1}>{children}</EpisodesProvider>
    </PlayerProvider>,
  );
  return { ...r, audio, episodeAudio };
}

it("says hello with the open preference and sends the prefs once on mount, then on a change", () => {
  localStorage.setItem("lark.quality", "lossless");
  const { rerender } = renderPlayer();
  expect(n.sent("hello")).toEqual([{ type: "hello", onOpen: "shuffle_favorites" }]);
  expect(n.sent("setPrefs")).toEqual([{ type: "setPrefs", quality: "lossless", carLyrics: true, loudness: true }]);
  rerender(
    <PlayerProvider userId={1} onOpen="shuffle_favorites" carLyrics={false}>
      <EpisodesProvider userId={1}><Probe /></EpisodesProvider>
    </PlayerProvider>,
  );
  act(() => p.setQuality("saver"));
  expect(n.sent("setPrefs").slice(1)).toEqual([
    { type: "setPrefs", quality: "lossless", carLyrics: false, loudness: true },
    { type: "setPrefs", quality: "saver", carLyrics: false, loudness: true },
  ]);
  expect(localStorage.getItem("lark.quality")).toBe("saver");
  expect(n.sent("hello")).toHaveLength(1);
});

it("sends the loudness switch with the prefs, so native applies each track's gain", () => {
  const { rerender } = render(
    <PlayerProvider userId={1} onOpen="resume" loudness={false}>
      <EpisodesProvider userId={1}><Probe /></EpisodesProvider>
    </PlayerProvider>,
  );
  expect(n.sent("setPrefs")).toEqual([{ type: "setPrefs", quality: "high", carLyrics: true, loudness: false }]);
  rerender(
    <PlayerProvider userId={1} onOpen="resume" loudness={true}>
      <EpisodesProvider userId={1}><Probe /></EpisodesProvider>
    </PlayerProvider>,
  );
  expect(n.sent("setPrefs").slice(1)).toEqual([{ type: "setPrefs", quality: "high", carLyrics: true, loudness: true }]);
});

it("the saved loudness preference reaches native from the app", async () => {
  renderWithApp(<Probe />, {
    routes: { "GET /api/v1/me/preferences": () => ({ body: { language: null, on_open: "resume", normalize_loudness: false } }) },
  });
  await waitFor(() => expect(n.sent("setPrefs").at(-1)).toMatchObject({ loudness: false }));
});

it("renders Now Playing from native state and queue events", async () => {
  mockFetch({ "GET /api/v1/tracks/2/lyrics": () => ({ body: { found: false } }) });
  renderPlayer(<><Probe /><MiniPlayer /></>);
  n.emit({ type: "queue", kind: "track", items: [trackItem(track1), trackItem(track2)], index: 1, source: "favorites" });
  n.emit(stateEvent({ kind: "track", itemId: "2", index: 1, playing: true, positionMs: 30_000, durationMs: 200_000 }));
  expect(await screen.findByText(track2.title)).toBeVisible();
  expect(screen.getByLabelText("暂停")).toBeVisible();
  expect(p.queue).toEqual({ tracks: [track1, track2], index: 1, source: "favorites" });
  expect(p.current).toEqual(track2);
  expect(p.playing).toBe(true);
  expect(prog.position).toBeCloseTo(30, 1);
  expect(prog.duration).toBe(200);
  n.emit(stateEvent({ kind: "track", itemId: "2", index: 1, playing: false, positionMs: 31_000, durationMs: 200_000, error: "boom" }));
  expect(p.playing).toBe(false);
  expect(p.error).toBe("boom");
});

it("an episode's state never touches the music player", () => {
  renderPlayer();
  n.emit({ type: "queue", kind: "track", items: [trackItem(track1)], index: 0, source: "list" });
  n.emit(stateEvent({ kind: "episode", itemId: "v1", playing: true, positionMs: 5000, durationMs: 9000 }));
  expect(p.playing).toBe(false);
  expect(prog.position).toBe(0);
});

it("follows native's index from state events (an automatic advance)", () => {
  renderPlayer();
  n.emit({ type: "queue", kind: "track", items: [trackItem(track1), trackItem(track2)], index: 0, source: "list" });
  n.emit(stateEvent({ kind: "track", itemId: "2", index: 1, playing: true }));
  expect(p.current?.id).toBe(2);
});

it("position interpolates between state events while playing", () => {
  vi.useFakeTimers();
  renderPlayer();
  n.emit({ type: "queue", kind: "track", items: [trackItem(track1)], index: 0, source: "list" });
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true, positionMs: 30_000, durationMs: 200_000 }));
  act(() => vi.advanceTimersByTime(1000));
  expect(prog.position).toBeCloseTo(31, 1);
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: false, positionMs: 31_200, durationMs: 200_000 }));
  act(() => vi.advanceTimersByTime(2000));
  expect(prog.position).toBeCloseTo(31.2, 3); // paused: no interval, no drift
});

it("the transport messages name their kind", () => {
  renderPlayer();
  n.emit({ type: "queue", kind: "track", items: [trackItem(track1), trackItem(track2)], index: 0, source: "list" });
  n.post.mockClear();
  act(() => p.play());
  act(() => p.toggle()); // not playing per native: plays
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true }));
  act(() => p.toggle());
  act(() => p.pause());
  act(() => p.next());
  act(() => p.prev());
  act(() => p.seek(12.5));
  expect(n.post.mock.calls.map(([m]) => m)).toEqual([
    { type: "play", kind: "track" },
    { type: "play", kind: "track" },
    { type: "pause", kind: "track" },
    { type: "pause", kind: "track" },
    { type: "next", kind: "track" },
    { type: "prev", kind: "track" },
    { type: "seek", kind: "track", ms: 12_500 },
  ]);
  expect(prog.position).toBeCloseTo(12.5, 1);
  expect(p.needsTap).toBe(false);
  act(() => p.prime());
  expect(n.post).toHaveBeenCalledTimes(7);
});

it("playList and jump start a new queue at 0; enqueueNext keeps the current item playing (setQueue without positionMs)", () => {
  renderPlayer();
  n.post.mockClear();
  act(() => p.playList([track1, track2], 1, { source: "shuffle" }));
  expect(n.post).toHaveBeenLastCalledWith({ type: "setQueue", kind: "track", items: [trackItem(track1), trackItem(track2)], index: 1, positionMs: 0, play: true, source: "shuffle" });
  expect(p.current?.id).toBe(2); // the mirror moves at once
  n.emit(stateEvent({ kind: "track", itemId: "2", index: 1, playing: true }));
  const t3 = tr(3);
  act(() => p.enqueueNext(t3));
  const last = n.post.mock.calls.at(-1)![0];
  expect(last).toEqual({ type: "setQueue", kind: "track", items: [trackItem(track1), trackItem(track2), trackItem(t3)], index: 1, play: true, source: "shuffle" });
  expect("positionMs" in last).toBe(false);
  act(() => p.jump(0));
  expect(n.post).toHaveBeenLastCalledWith(expect.objectContaining({ type: "setQueue", index: 0, positionMs: 0, play: true }));
});

it("queue sheet edits and add to queue keep the current item playing (setQueue without positionMs)", () => {
  renderPlayer();
  const [t3, t4, t7] = [tr(3), tr(4), tr(7)];
  act(() => p.playList([track1, track2, t3, t4], 1));
  n.emit(stateEvent({ kind: "track", itemId: "2", index: 1, playing: true }));
  act(() => p.move(1, 3));
  const moved = n.post.mock.calls.at(-1)![0];
  expect(moved).toEqual({ type: "setQueue", kind: "track", items: [track1, t3, t4, track2].map(trackItem), index: 3, play: true, source: "list" });
  expect("positionMs" in moved).toBe(false);
  expect(p.current?.id).toBe(2);
  act(() => p.removeAt(0));
  expect(n.post).toHaveBeenLastCalledWith({ type: "setQueue", kind: "track", items: [t3, t4, track2].map(trackItem), index: 2, play: true, source: "list" });
  n.post.mockClear();
  act(() => p.removeAt(2)); // the current one: nothing sent
  expect(n.post).not.toHaveBeenCalled();
  act(() => p.move(2, 0));
  act(() => p.addToQueue(t7));
  const added = n.post.mock.calls.at(-1)![0];
  expect(added).toEqual({ type: "setQueue", kind: "track", items: [track2, t7, t3, t4].map(trackItem), index: 0, play: true, source: "list" });
  expect("positionMs" in added).toBe(false);
});

it("the app's first queue gets the block stored on this device when it is the same queue", () => {
  localStorage.setItem("lark.upNext.1", JSON.stringify({ ids: [1, 7, 2], index: 0, upNext: 1 }));
  renderPlayer();
  n.emit({ type: "queue", kind: "track", items: [track1, tr(7), track2].map(trackItem), index: 0, source: "list" });
  act(() => p.addToQueue(tr(9)));
  expect(p.queue.tracks.map((x) => x.id)).toEqual([1, 7, 9, 2]);
  localStorage.removeItem("lark.upNext.1");
});

it("a stored block for another queue is not applied to the app's first queue", () => {
  localStorage.setItem("lark.upNext.1", JSON.stringify({ ids: [1, 7, 3], index: 0, upNext: 1 }));
  renderPlayer();
  n.emit({ type: "queue", kind: "track", items: [track1, tr(7), track2].map(trackItem), index: 0, source: "list" });
  act(() => p.addToQueue(tr(9)));
  expect(p.queue.tracks.map((x) => x.id)).toEqual([1, 9, 7, 2]);
  localStorage.removeItem("lark.upNext.1");
});

it("native's echo of an edited queue keeps the queued tracks, so a second add goes after the first", () => {
  renderPlayer();
  const [t3, t7, t8] = [tr(3), tr(7), tr(8)];
  act(() => p.playList([track1, track2, t3], 0));
  act(() => p.addToQueue(t7));
  // Native answers every setQueue with its queue.
  n.emit({ type: "queue", kind: "track", items: [track1, t7, track2, t3].map(trackItem), index: 0, source: "list" });
  act(() => p.addToQueue(t8));
  expect(p.queue.tracks.map((x) => x.id)).toEqual([1, 7, 8, 2, 3]);
  // Native moved on by itself: the queued block shrinks with it.
  n.emit({ type: "queue", kind: "track", items: [track1, t7, t8, track2, t3].map(trackItem), index: 1, source: "list" });
  act(() => p.addToQueue(tr(9)));
  expect(p.queue.tracks.map((x) => x.id)).toEqual([1, 7, 8, 9, 2, 3]);
  // A different queue from native (another device's, a refill): nothing is queued any more.
  n.emit({ type: "queue", kind: "track", items: [track1, track2].map(trackItem), index: 0, source: "list" });
  act(() => p.addToQueue(tr(5)));
  expect(p.queue.tracks.map((x) => x.id)).toEqual([1, 5, 2]);
});

it("remove and updateTrack edit the queue; removing the last entry stops music", () => {
  renderPlayer();
  act(() => p.playList([track1, track2], 0));
  n.emit(stateEvent({ kind: "track", itemId: "1", index: 0, playing: true }));
  act(() => p.updateTrack({ ...track2, title: "renamed" }));
  expect(n.post).toHaveBeenLastCalledWith(expect.objectContaining({ type: "setQueue", index: 0, play: true }));
  expect(p.queue.tracks[1].title).toBe("renamed");
  act(() => p.remove(2));
  expect(n.post).toHaveBeenLastCalledWith({ type: "setQueue", kind: "track", items: [trackItem(track1)], index: 0, play: true, source: "list" });
  act(() => p.remove(1));
  expect(n.post).toHaveBeenLastCalledWith({ type: "stop", kind: "track" });
  expect(p.current).toBeNull();
});

it("a preview claiming the session posts pauseForWeb; native starting music claims it back", () => {
  renderPlayer();
  act(() => claimSession("preview"));
  expect(n.post).toHaveBeenLastCalledWith({ type: "pauseForWeb" });
  act(() => claimSession("video"));
  expect(n.post).toHaveBeenLastCalledWith({ type: "pauseForWeb" });
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true }));
  expect(ownsSession("music")).toBe(true);
  n.post.mockClear();
  act(() => claimSession("episode"));
  expect(n.sent("pauseForWeb")).toEqual([]); // native episodes are native's own business
  n.emit(stateEvent({ kind: "episode", itemId: "v1", playing: true }));
  expect(ownsSession("episode")).toBe(true);
});

it("shows native's notices", () => {
  renderPlayer();
  n.emit({ type: "notice", text: "offline: cached favorites" });
  expect(p.notice).toBe("offline: cached favorites");
});

it("is ready after the first music queue event, or after 1.5 s", () => {
  vi.useFakeTimers();
  const { unmount } = renderPlayer();
  expect(p.ready).toBe(false);
  n.emit({ type: "queue", kind: "track", items: [], index: 0, source: "list" });
  expect(p.ready).toBe(true);
  unmount();
  renderPlayer();
  expect(p.ready).toBe(false);
  act(() => vi.advanceTimersByTime(1500));
  expect(p.ready).toBe(true);
});

it("shuffleFavorites asks the server as the web does, then hands native the queue", async () => {
  mockFetch({ "GET /api/v1/tracks/random": () => ({ body: { source: "favorites", tracks: [track2, track1] } }) });
  renderPlayer();
  await act(() => p.shuffleFavorites());
  expect(n.post).toHaveBeenLastCalledWith(expect.objectContaining({ type: "setQueue", kind: "track", index: 0, positionMs: 0, play: true, source: "favorites" }));
});

it("flushEvents resolves on flushed or after 3 s", async () => {
  vi.useFakeTimers();
  renderPlayer();
  let done = false;
  act(() => void p.flushEvents().then(() => (done = true)));
  const [{ id }] = n.sent("flushEvents");
  expect(typeof id).toBe("string");
  await act(async () => void (await Promise.resolve()));
  expect(done).toBe(false);
  n.emit({ type: "flushed", id: "someone else" });
  await act(async () => void (await Promise.resolve()));
  expect(done).toBe(false);
  n.emit({ type: "flushed", id });
  await act(async () => void (await Promise.resolve()));
  expect(done).toBe(true);

  let second = false;
  act(() => void p.flushEvents().then(() => (second = true)));
  await act(async () => void vi.advanceTimersByTime(2900));
  expect(second).toBe(false);
  await act(async () => void vi.advanceTimersByTime(200));
  expect(second).toBe(true);
});

it("favorite toggles are forwarded so native re-syncs its cache", async () => {
  mockFetch({ "PUT /api/v1/favorites/7": () => ({ status: 204 }), "DELETE /api/v1/favorites/7": () => ({ status: 204 }) });
  renderPlayer();
  await act(() => api.setFavorite(7, true));
  await act(() => api.setFavorite(7, false));
  expect(n.sent("favoriteChanged")).toEqual([
    { type: "favoriteChanged", trackId: 7, on: true },
    { type: "favoriteChanged", trackId: 7, on: false },
  ]);
});

it("episodes: ▶ on a list posts an episode queue at the resume position, never a music one", () => {
  const { episodeAudio } = renderPlayer(<><Probe /><EProbe /></>);
  const list = [ep("a", { published_at: 300, position_s: 120 }), ep("b", { published_at: 200, played: true }), ep("c", { published_at: 100 })];
  n.post.mockClear();
  act(() => e.playList(list, 0));
  expect(n.post.mock.calls.map(([m]) => m)).toEqual([
    { type: "setQueue", kind: "episode", items: [episodeItem(list[0]), episodeItem(list[2])], index: 0, positionMs: 120_000, play: true, source: "list" },
  ]);
  expect(episodeAudio.src).toBe("");
  expect(e.queue.map((x) => x.video_id)).toEqual(["a", "c"]);
  // An episode page's ▶ 听 at a chosen moment.
  act(() => e.play([list[2]], 0, { startAt: 42 }));
  expect(n.post).toHaveBeenLastCalledWith(expect.objectContaining({ kind: "episode", positionMs: 42_000, index: 0 }));
  expect(n.sent("setQueue").every((m) => m.kind === "episode")).toBe(true);
});

it("episodes: state and queue events drive the player; active follows what native plays", () => {
  vi.useFakeTimers();
  renderPlayer(<><Probe /><EProbe /></>);
  const a = ep("a", { position_s: 30 });
  const b = ep("b");
  n.emit({ type: "queue", kind: "episode", items: [episodeItem(a), episodeItem(b)], index: 0, source: "list" });
  n.emit(stateEvent({ kind: "episode", itemId: "a", index: 0, playing: false, positionMs: 30_000, durationMs: 3_600_000, rate: 1.5 }));
  expect(e.current?.video_id).toBe("a");
  expect(e.active).toBe(false); // restored, nothing played: music's player stays
  expect(e.rate).toBe(1.5);
  n.emit(stateEvent({ kind: "episode", itemId: "a", index: 0, playing: true, positionMs: 30_000, durationMs: 3_600_000, rate: 1.5 }));
  expect(e.active).toBe(true);
  expect(e.playing).toBe(true);
  act(() => vi.advanceTimersByTime(2000));
  expect(eprog.position).toBeCloseTo(33, 1); // 2 s at 1.5×
  expect(eprog.duration).toBe(3600);
  // Music starts natively (the car, a remote command): the episode steps aside.
  n.emit(stateEvent({ kind: "episode", itemId: "a", index: 0, playing: false, positionMs: 33_000, durationMs: 3_600_000, rate: 1.5 }));
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true }));
  expect(e.active).toBe(false);
});

it("episodes: the controls map to episode messages; close stops native's episode queue", () => {
  renderPlayer(<><Probe /><EProbe /></>);
  const a = ep("a");
  const b = ep("b", { published_at: 50 });
  n.emit({ type: "queue", kind: "episode", items: [episodeItem(a), episodeItem(b)], index: 0, source: "list" });
  n.emit(stateEvent({ kind: "episode", itemId: "a", playing: true }));
  n.post.mockClear();
  act(() => e.toggle());
  act(() => e.pause());
  act(() => e.skip(-15));
  act(() => e.seek(100));
  act(() => e.next());
  act(() => e.prev());
  act(() => e.setRate(2));
  expect(n.post.mock.calls.map(([m]) => m)).toEqual([
    { type: "pause", kind: "episode" },
    { type: "pause", kind: "episode" },
    { type: "skip", kind: "episode", ms: -15_000 },
    { type: "seek", kind: "episode", ms: 100_000 },
    { type: "next", kind: "episode" },
    { type: "prev", kind: "episode" },
    { type: "setRate", rate: 2 },
  ]);
  expect(e.rate).toBe(2);
  expect(localStorage.getItem("lark.episodeRate")).toBe("2");
  n.post.mockClear();
  act(() => e.setOrder("oldest"));
  const sq = n.sent("setQueue")[0];
  expect(sq).toMatchObject({ kind: "episode", index: 1, play: true });
  expect(sq.items.map((i) => i.id)).toEqual(["b", "a"]);
  expect("positionMs" in sq).toBe(false);
  act(() => e.close());
  expect(n.post).toHaveBeenLastCalledWith({ type: "stop", kind: "episode" });
  expect(e.queue).toEqual([]);
  expect(e.active).toBe(false);
  expect(loadStoredQueue(1)).toBeNull();
});

it("episodes: a stored queue shows at once; native's queue event is the truth", () => {
  const a = ep("a", { position_s: 90 });
  saveStoredQueue(1, { source: [a], ids: ["a"], index: 0, order: "newest", includePlayed: false, active: false });
  renderPlayer(<><Probe /><EProbe /></>);
  expect(e.current?.video_id).toBe("a");
  n.post.mockClear();
  act(() => e.toggle());
  expect(n.post).toHaveBeenLastCalledWith({ type: "play", kind: "episode" });
  // Native's queue is the truth: empty there means empty here.
  n.emit({ type: "queue", kind: "episode", items: [], index: 0, source: "list" });
  expect(e.queue).toEqual([]);
  expect(loadStoredQueue(1)).toBeNull();
});

it("native plays one kind at a time: the other kind's playing state is cleared, so its buttons and the session stay right", () => {
  renderPlayer(<><Probe /><EProbe /></>);
  const a = ep("a");
  n.emit({ type: "queue", kind: "track", items: [trackItem(track1)], index: 0, source: "list" });
  n.emit({ type: "queue", kind: "episode", items: [episodeItem(a)], index: 0, source: "list" });
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true }));
  // ▶ on an episode: native switches and (an older native) reports only the episode's state.
  n.emit(stateEvent({ kind: "episode", itemId: "a", playing: true }));
  expect(p.playing).toBe(false);
  expect(e.playing).toBe(true);
  // A preview pauses native's episode.
  act(() => claimSession("preview"));
  expect(n.post).toHaveBeenLastCalledWith({ type: "pauseForWeb" });
  n.emit(stateEvent({ kind: "episode", itemId: "a", playing: false }));
  // Music's ▶ plays (not a pause native would ignore)…
  n.post.mockClear();
  act(() => p.toggle());
  expect(n.post).toHaveBeenLastCalledWith({ type: "play", kind: "track" });
  // …and music sounding takes the session back from the preview.
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true }));
  expect(ownsSession("music")).toBe(true);
  expect(e.playing).toBe(false);
  // 继续收听 on the episode plays it.
  n.post.mockClear();
  act(() => e.toggle());
  expect(n.post).toHaveBeenLastCalledWith({ type: "play", kind: "episode" });
});

it("episodes: unmounting gives the session back to music (the owner is module state)", () => {
  const { unmount } = renderPlayer(<><Probe /><EProbe /></>);
  n.emit({ type: "queue", kind: "episode", items: [episodeItem(ep("a"))], index: 0, source: "list" });
  n.emit(stateEvent({ kind: "episode", itemId: "a", playing: true }));
  expect(ownsSession("episode")).toBe(true);
  unmount();
  expect(ownsSession("music")).toBe(true);
});

it("items: an episode goes without its description; an item without meta still becomes a web object", () => {
  const withText = ep("d", { description: "a long text" });
  const item = episodeItem(withText);
  expect(item).toMatchObject({ kind: "episode", id: "d", title: "ep d", artist: "Chan", durationMs: 3_600_000 });
  expect("description" in (item.meta as Episode)).toBe(false);
  expect(trackItem(track1)).toEqual({ kind: "track", id: "1", title: "song 1", artist: "a", album: "b", durationMs: 200_000, meta: track1 });
  expect(fromItem(trackItem(track1))).toBe(track1);
  const bare = fromItem({ kind: "track", id: "9", title: "x", artist: "y", album: "z", durationMs: 1000, meta: null as unknown as Track }) as Track;
  expect(bare).toMatchObject({ id: 9, title: "x", artist: "y", album: "z", duration_ms: 1000 });
});

function AuthProbe() {
  const a = useAuth();
  return <>{a.loading ? "loading" : a.user ? `in:${a.user.id}` : "out"}<button onClick={() => void a.logout()}>logout</button></>;
}

it("auth: sign-in posts auth{signedIn:true,userId}, sign-out posts signedIn:false; authRequired signs the web out", async () => {
  mockFetch({
    "GET /api/v1/me": () => ({ body: { id: 5, username: "u", role: "member" } }),
    "POST /api/v1/auth/logout": () => ({ status: 204 }),
  });
  render(<AuthProvider><AuthProbe /></AuthProvider>);
  expect(await screen.findByText("in:5")).toBeInTheDocument();
  // The post comes from an effect that runs after the render shows in:5: wait for it.
  await waitFor(() => expect(n.sent("auth")).toEqual([{ type: "auth", signedIn: true, userId: 5 }])); // nothing while loading
  await userEvent.click(screen.getByRole("button", { name: "logout" }));
  await screen.findByText("out");
  await waitFor(() => expect(n.sent("auth").at(-1)).toEqual({ type: "auth", signedIn: false }));
});

it("auth: native's authRequired signs the web out", async () => {
  mockFetch({ "GET /api/v1/me": () => ({ body: { id: 5, username: "u", role: "member" } }) });
  render(<AuthProvider><AuthProbe /></AuthProvider>);
  await screen.findByText("in:5");
  n.post.mockClear();
  n.emit({ type: "authRequired" });
  expect(await screen.findByText("out")).toBeInTheDocument();
  expect(n.sent("auth")).toEqual([]); // native signed itself out: no echo back
});

it("auth: a page that opens signed out tells native nothing (it may be offline, not signed out)", async () => {
  mockFetch({ "GET /api/v1/me": () => ({ status: 401, body: { error: "unauthorized" } }) });
  render(<AuthProvider><AuthProbe /></AuthProvider>);
  await screen.findByText("out");
  expect(n.sent("auth")).toEqual([]);
});

it("settings: an iPhone app row opens the native settings; the web offline cache section is not shown", async () => {
  renderWithApp(<SettingsPage />);
  const row = await screen.findByRole("button", { name: "iPhone App 设置" });
  await userEvent.click(row);
  expect(n.sent("openSettings")).toEqual([{ type: "openSettings" }]);
  expect(screen.queryByText("离线缓存")).toBeNull();
});

it("logout in native mode flushes native's events before signing out", async () => {
  renderWithApp(<SettingsPage />, { routes: { "POST /api/v1/auth/logout": () => ({ status: 204 }) } });
  await userEvent.click(await screen.findByRole("button", { name: "退出登录" }));
  const [{ id }] = n.sent("flushEvents");
  n.emit({ type: "flushed", id });
  await waitFor(() => expect(n.sent("auth").at(-1)).toEqual({ type: "auth", signedIn: false }));
  const order = n.post.mock.calls.map(([m]) => m.type);
  expect(order.indexOf("flushEvents")).toBeLessThan(order.lastIndexOf("auth"));
});

it("episode queue sheet edits keep the current episode playing (setQueue without positionMs)", () => {
  renderPlayer(<><Probe /><EProbe /></>);
  const list = [ep("a"), ep("b"), ep("c")];
  act(() => e.play(list, 1));
  n.emit(stateEvent({ kind: "episode", itemId: "b", index: 1, playing: true }));
  act(() => e.move(1, 0));
  const moved = n.post.mock.calls.at(-1)![0];
  expect(moved).toEqual({ type: "setQueue", kind: "episode", items: [list[1], list[0], list[2]].map(episodeItem), index: 0, play: true, source: "list" });
  expect("positionMs" in moved).toBe(false);
  act(() => e.removeAt(2));
  expect(n.post).toHaveBeenLastCalledWith({ type: "setQueue", kind: "episode", items: [list[1], list[0]].map(episodeItem), index: 0, play: true, source: "list" });
  n.post.mockClear();
  act(() => e.removeAt(0)); // the current one
  expect(n.post).not.toHaveBeenCalled();
  expect(e.current?.video_id).toBe("b");
});

// Shuffle and repeat run in the native engine: the web posts setModes and
// mirrors the modes from native's music state.
it("the modes mirror native's music state; setShuffle and cycleRepeat post setModes and change nothing locally", () => {
  renderPlayer();
  n.emit({ type: "queue", kind: "track", items: [track1, track2, tr(3)].map(trackItem), index: 0, source: "list" });
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true }));
  expect(p.modesAvailable).toBe(true);
  expect(p.modes).toEqual({ shuffle: false, repeat: "off" });
  n.post.mockClear();
  act(() => p.setShuffle(true));
  expect(n.post.mock.calls.map(([m]) => m)).toEqual([{ type: "setModes", shuffle: true, repeat: "off" }]);
  expect(p.queue.tracks.map((x) => x.id)).toEqual([1, 2, 3]); // native answers with the shuffled queue
  act(() => p.setShuffle(true)); // already on (as far as the web knows): nothing more
  expect(n.sent("setModes")).toHaveLength(1);
  n.emit({ type: "queue", kind: "track", items: [track1, tr(3), track2].map(trackItem), index: 0, source: "list" });
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true, shuffle: true }));
  expect(p.modes).toEqual({ shuffle: true, repeat: "off" });
  expect(p.queue.tracks.map((x) => x.id)).toEqual([1, 3, 2]);
  act(() => p.cycleRepeat());
  act(() => p.cycleRepeat()); // a second tap before native answers still steps on
  expect(n.sent("setModes").slice(1)).toEqual([
    { type: "setModes", shuffle: true, repeat: "all" },
    { type: "setModes", shuffle: true, repeat: "one" },
  ]);
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true, shuffle: true, repeat: "one" }));
  expect(p.modes).toEqual({ shuffle: true, repeat: "one" });
  // An episode's state (always false/"off") never touches music's modes.
  n.emit(stateEvent({ kind: "episode", itemId: "v1", playing: false }));
  expect(p.modes).toEqual({ shuffle: true, repeat: "one" });
  // The lock screen changed them: the next state says so.
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true, shuffle: false, repeat: "all" }));
  expect(p.modes).toEqual({ shuffle: false, repeat: "all" });
  expect(n.sent("setQueue")).toEqual([]);
});

it("an app from before the modes: no modes, the buttons stay hidden; a state with them shows the buttons", async () => {
  renderWithApp(<><Probe /><MiniPlayer /></>);
  const audio = { codec: "flac", bitrate: 900, lossless: true } as Partial<Track>;
  n.emit({ type: "queue", kind: "track", items: [tr(1, audio), tr(2, audio)].map(trackItem), index: 0, source: "list" });
  n.emit(stateEvent({ kind: "track", itemId: "1", shuffle: undefined, repeat: undefined }));
  expect(p.modesAvailable).toBe(false);
  await userEvent.click(await screen.findByText(track1.title));
  const dialog = await screen.findByRole("dialog", { name: "正在播放" });
  expect(screen.queryByRole("button", { name: "随机播放" })).toBeNull();
  expect(screen.queryByRole("button", { name: /循环/ })).toBeNull();
  n.emit(stateEvent({ kind: "track", itemId: "1", shuffle: false, repeat: "all" }));
  expect(p.modesAvailable).toBe(true);
  const shuffle = await screen.findByRole("button", { name: "随机播放" });
  expect(shuffle).toHaveAttribute("aria-pressed", "false");
  expect(dialog.querySelector('[data-repeat="all"]')).not.toBeNull();
  n.post.mockClear();
  await userEvent.click(shuffle);
  expect(n.sent("setModes")).toEqual([{ type: "setModes", shuffle: true, repeat: "all" }]);
  expect(shuffle).toHaveAttribute("aria-pressed", "false"); // until native says so
  n.emit(stateEvent({ kind: "track", itemId: "1", shuffle: true, repeat: "all" }));
  expect(shuffle).toHaveAttribute("aria-pressed", "true");
});

it("turning shuffle on drops the queued block; native's unshuffled queue keeps it when it still follows the current track", () => {
  renderPlayer();
  const [t3, t4, t7, t8] = [tr(3), tr(4), tr(7), tr(8)];
  n.emit({ type: "queue", kind: "track", items: [track1, track2, t3, t4].map(trackItem), index: 0, source: "list" });
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true }));
  act(() => p.addToQueue(t7)); // queued: [1 | 7] 2 3 4
  act(() => p.setShuffle(true));
  expect(p.queue.upNext).toBeUndefined(); // as the web player: everything after the current track is shuffled
  n.emit({ type: "queue", kind: "track", items: [track1, t4, t7, track2, t3].map(trackItem), index: 0, source: "list" });
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true, shuffle: true }));
  act(() => p.enqueueNext(t8)); // play next while shuffled: [1 | 8] 4 7 2 3
  expect(p.queue.upNext).toBe(1);
  act(() => p.setShuffle(false));
  // Native restores the order; the play-next track stays in front.
  n.emit({ type: "queue", kind: "track", items: [track1, t8, track2, t3, t4, t7].map(trackItem), index: 0, source: "list" });
  expect(p.queue.upNext).toBe(1);
  act(() => p.addToQueue(tr(9)));
  expect(p.queue.tracks.map((x) => x.id)).toEqual([1, 8, 9, 2, 3, 4, 7]);
});

it("a state already in flight before native handled a tap doesn't roll the next tap back", () => {
  vi.useFakeTimers();
  renderPlayer();
  n.emit({ type: "queue", kind: "track", items: [track1, track2].map(trackItem), index: 0, source: "list" });
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true }));
  act(() => p.cycleRepeat()); // asks for "all"
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true, positionMs: 500 })); // a tick sent before that: still "off"
  act(() => p.cycleRepeat());
  expect(n.sent("setModes").map((m) => m.repeat)).toEqual(["all", "one"]);
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true, repeat: "one" })); // confirmed
  expect(p.modes.repeat).toBe("one");
  // Native never confirmed a request (an old reply lost, the lock screen won): its state rules again soon.
  act(() => p.setShuffle(true));
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true, repeat: "one" }));
  act(() => vi.advanceTimersByTime(3000));
  n.emit(stateEvent({ kind: "track", itemId: "1", playing: true, repeat: "one" }));
  act(() => p.setShuffle(true));
  expect(n.sent("setModes").slice(-2).map((m) => m.shuffle)).toEqual([true, true]);
});
