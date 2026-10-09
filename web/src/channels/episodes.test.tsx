import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { MemoryRouter } from "react-router";
import type { Episode, OnOpen, Track } from "../api/types";
import { PlayerProvider, usePlayer, type Player } from "../player/PlayerProvider";
import { claimSession, ownsSession, resetSessionOwner } from "../player/sessionOwner";
import { FakeAudio, mockFetch } from "../test/setup";
import { ShellPlayer } from "../player/ShellPlayer";
import { EpisodeMini } from "./EpisodeMini";
import { EpisodesProvider, resumeAt, useEpisodes, type EpisodesPlayer } from "./EpisodesProvider";

// 11-character ids like real ones: episode0001, episode0002, …
const ep = (n: number, extra: Partial<Episode> = {}): Episode => ({
  video_id: `episode000${n}`, channel_id: "UC0e5c4U67Vm6sAVK0vxN3Uw", channel_title: "刘翔的投资频道",
  title: `第${n}集`, published_at: 1_790_000_000 + n, duration_s: 1800, kind: "video",
  thumbnail: `/api/v1/episodes/episode000${n}/thumbnail`,
  audio: { status: "done", progress: 100, bytes: 1, error: "" }, video: null, position_s: 0, played: false, kept: false, ...extra,
});
const tr = (id: number) => ({ id, title: `t${id}`, artist: "a", album: "b", duration_ms: 200000 }) as Track;

function stubMediaSession() {
  const ms = { setActionHandler: vi.fn(), setPositionState: vi.fn(), metadata: null as unknown, playbackState: "none" };
  Object.defineProperty(navigator, "mediaSession", { value: ms, configurable: true, writable: true });
  vi.stubGlobal("MediaMetadata", class { constructor(public init: MediaMetadataInit) {} });
  return ms;
}

type Routes = Parameters<typeof mockFetch>[0];

function setup(opts: { onOpen?: OnOpen; routes?: Routes; tweak?: (a: FakeAudio) => void; shell?: boolean } = {}) {
  const f = mockFetch({
    "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] } }),
    "PUT /api/v1/queue": () => ({ body: {} }),
    "GET /api/v1/radio/next": () => ({ body: [] }),
    "POST /api/v1/events/play": () => ({ body: { accepted: 0 } }),
    "PUT /api/v1/episodes/episode0001/progress": () => ({ status: 204 }),
    "PUT /api/v1/episodes/episode0002/progress": () => ({ status: 204 }),
    ...opts.routes,
  });
  const music = new FakeAudio();
  opts.tweak?.(music);
  const episode = new FakeAudio();
  let p!: Player;
  let e!: EpisodesPlayer;
  function Probe() {
    p = usePlayer();
    e = useEpisodes();
    return null;
  }
  const { unmount } = render(
    <MemoryRouter>
      <PlayerProvider audio={music as unknown as HTMLAudioElement} onOpen={opts.onOpen}>
        <EpisodesProvider audio={episode as unknown as HTMLAudioElement}>
          <Probe />
          {opts.shell ? <ShellPlayer /> : <EpisodeMini />}
        </EpisodesProvider>
      </PlayerProvider>
    </MemoryRouter>,
  );
  return { f, music, episode, player: () => p, eps: () => e, unmount };
}

afterEach(() => {
  resetSessionOwner();
  delete (navigator as unknown as { mediaSession?: unknown }).mediaSession;
  vi.useRealTimers();
});

const progressCalls = (f: ReturnType<typeof mockFetch>) =>
  f.mock.calls.filter(([u]) => String(u).includes("/progress")).map(([u, init]) => [String(u), JSON.parse((init as RequestInit).body as string)]);

test("an episode pauses music, owns the lock screen, and music takes it back when it really plays", () => {
  const ms = stubMediaSession();
  const { music, episode, player, eps } = setup();
  act(() => player().playList([tr(1), tr(2)], 0));
  act(() => music.fire("playing"));
  expect(ownsSession("music")).toBe(true);
  act(() => eps().play([ep(1), ep(2)], 0));
  expect(music.pause).toHaveBeenCalled();
  expect(episode.src).toContain("/api/v1/episodes/episode0001/stream?kind=audio");
  expect(ownsSession("episode")).toBe(true);
  const md = () => (ms.metadata as { init: MediaMetadataInit }).init;
  expect(md().title).toBe("第1集");
  expect(md().artist).toBe("刘翔的投资频道");
  expect(md().album).toBe("频道");
  // A change in the (paused) music queue must not touch the lock screen now.
  act(() => player().next());
  expect(md().title).toBe("第1集");
  // ⏭ from the car moves within the episodes.
  const handlers = Object.fromEntries(ms.setActionHandler.mock.calls.map(([a, h]) => [a, h]));
  act(() => (handlers.nexttrack as () => void)());
  expect(episode.src).toContain("episode0002");
  // Music plays for real again: the episode pauses and the song is back on the lock screen.
  act(() => music.fire("playing"));
  expect(episode.pause).toHaveBeenCalled();
  expect(eps().active).toBe(false);
  expect(md().title).toBe("t2");
});

test("the silent unlock clip or a muted prime never takes the session from an episode", () => {
  const { music, eps } = setup();
  act(() => eps().play([ep(1)], 0));
  music.src = "data:audio/wav;base64,xx";
  act(() => music.fire("playing"));
  music.src = "/api/v1/tracks/1/stream?quality=high";
  music.muted = true;
  act(() => music.fire("playing"));
  expect(ownsSession("episode")).toBe(true);
});

test("resumes at the saved position, saves every 15 s, on pause, and marks played at the end", () => {
  vi.useFakeTimers({ now: 1_000_000, toFake: ["Date"] });
  const { f, episode, eps } = setup();
  act(() => eps().play([ep(1, { position_s: 600 }), ep(2)], 0));
  act(() => episode.fire("loadedmetadata"));
  expect(episode.currentTime).toBe(600);
  episode.currentTime = 610;
  vi.setSystemTime(1_010_000);
  act(() => episode.fire("timeupdate"));
  expect(progressCalls(f)).toHaveLength(0);
  episode.currentTime = 616.4;
  vi.setSystemTime(1_016_000);
  act(() => episode.fire("timeupdate"));
  expect(progressCalls(f)).toEqual([["/api/v1/episodes/episode0001/progress", { position_s: 616 }]]);
  act(() => eps().pause());
  expect(progressCalls(f).at(-1)?.[1]).toEqual({ position_s: 616 });
  act(() => episode.fire("ended"));
  expect(progressCalls(f).at(-1)).toEqual(["/api/v1/episodes/episode0001/progress", { position_s: 0, played: true }]);
  expect(episode.src).toContain("episode0002");
});

test("resumeAt starts over for played, unstarted or nearly finished episodes", () => {
  expect(resumeAt(ep(1, { position_s: 120 }))).toBe(120);
  expect(resumeAt(ep(1, { position_s: 120, played: true }))).toBe(0);
  expect(resumeAt(ep(1, { position_s: 1790 }))).toBe(0);
  expect(resumeAt(ep(1))).toBe(0);
});

test("speed is applied, kept across loads and remembered", () => {
  const { episode, eps } = setup();
  act(() => eps().play([ep(1), ep(2)], 0));
  act(() => eps().setRate(1.5));
  expect(episode.playbackRate).toBe(1.5);
  act(() => eps().next());
  act(() => episode.fire("loadedmetadata"));
  expect(episode.playbackRate).toBe(1.5);
  expect(localStorage.getItem("lark.episodeRate")).toBe("1.5");
});

test("the episode mini player skips, pauses and closes back to music", async () => {
  const { episode, eps } = setup();
  act(() => eps().play([ep(1)], 0));
  episode.currentTime = 100;
  act(() => screen.getByRole("button", { name: "后退 15 秒" }).click());
  expect(episode.currentTime).toBe(85);
  act(() => screen.getByRole("button", { name: "前进 30 秒" }).click());
  expect(episode.currentTime).toBe(115);
  expect(screen.getByText("第1集")).toBeInTheDocument();
  act(() => screen.getByRole("button", { name: "关闭节目播放器" }).click());
  expect(episode.pause).toHaveBeenCalled();
  expect(ownsSession("music")).toBe(true);
  expect(eps().active).toBe(false);
});

// ---- Once an episode owns the session, only an explicit music action brings music back ----

const notAllowed = () => Promise.reject(Object.assign(new Error("blocked"), { name: "NotAllowedError" }));
const favs = (...ids: number[]): Routes => ({ "GET /api/v1/tracks/random": () => ({ body: { source: "favorites", tracks: ids.map(tr) } }) });
const restored = (...ids: number[]): Routes => ({
  "GET /api/v1/queue": () => ({ body: { queue: { track_ids: ids, current_index: 0, position_ms: 0, version: 1, updated_by: "", updated_at: 0 }, tracks: ids.map(tr) } }),
});

test("an episode clears music's tap-to-start prompt, so the next tap elsewhere starts no music", async () => {
  const { music, player, eps } = setup({ onOpen: "shuffle_favorites", routes: favs(7, 8), tweak: (a) => a.play.mockImplementationOnce(notAllowed) });
  await waitFor(() => expect(player().needsTap).toBe(true));
  act(() => eps().play([ep(1)], 0));
  expect(player().needsTap).toBe(false);
  music.play.mockClear();
  act(() => document.body.click()); // e.g. a nav tab
  expect(music.play).not.toHaveBeenCalled();
  expect(ownsSession("episode")).toBe(true);
  expect(eps().active).toBe(true);
});

test("a pending music network retry is dropped when an episode starts, even with music not sounding", () => {
  vi.useFakeTimers();
  const { music, player, eps } = setup();
  act(() => player().playList([tr(1), tr(2)], 0));
  music.error = { code: 2 }; // MEDIA_ERR_NETWORK: a retry is scheduled
  act(() => music.fire("error"));
  music.paused = true;
  act(() => eps().play([ep(1)], 0));
  music.play.mockClear();
  const src = music.src;
  act(() => vi.advanceTimersByTime(20_000));
  act(() => window.dispatchEvent(new Event("online")));
  expect(music.play).not.toHaveBeenCalled();
  expect(music.src).toBe(src);
  expect(ownsSession("episode")).toBe(true);
});

test("a shuffle-on-open that lands after an episode started stays silent", async () => {
  const { music, player, eps } = setup({ onOpen: "shuffle_favorites", routes: favs(7, 8) });
  act(() => eps().play([ep(1)], 0));
  await waitFor(() => expect(player().current?.id).toBe(7));
  expect(music.play).not.toHaveBeenCalled();
  expect(player().needsTap).toBe(false);
  expect(ownsSession("episode")).toBe(true);
});

test("music takes the session back only through an explicit music action", () => {
  const { music, episode, player, eps } = setup();
  act(() => player().playList([tr(1), tr(2)], 0));
  act(() => music.fire("playing"));
  act(() => eps().play([ep(1)], 0));
  music.play.mockClear();
  act(() => document.body.click());
  act(() => player().next()); // moves the paused queue, plays nothing
  expect(music.play).not.toHaveBeenCalled();
  expect(ownsSession("episode")).toBe(true);
  act(() => player().play());
  expect(music.play).toHaveBeenCalled();
  act(() => music.fire("playing"));
  expect(ownsSession("music")).toBe(true);
  expect(episode.pause).toHaveBeenCalled();
});

// The tap that starts an episode from a control without data-no-music-prime:
// prime() runs first (capture phase), plays the restored track muted and
// pauses it at once, and restores muted — a real browser delivers that
// blip's "playing" afterwards, when the element is no longer muted.
test("the muted prime of the tap that starts an episode never takes the session, even its late 'playing'", async () => {
  const { music, player, eps } = setup({ routes: restored(1) });
  await waitFor(() => expect(player().current?.id).toBe(1));
  const btn = document.createElement("button");
  btn.addEventListener("click", () => eps().play([ep(1)], 0));
  document.body.appendChild(btn);
  act(() => btn.click());
  btn.remove();
  expect(music.play).toHaveBeenCalledTimes(1); // the muted blip
  expect(music.muted).toBe(false);
  act(() => music.fire("playing"));
  expect(ownsSession("episode")).toBe(true);
  expect(eps().active).toBe(true);
  // The next real start still takes it back.
  act(() => player().play());
  act(() => music.fire("playing"));
  expect(ownsSession("music")).toBe(true);
});

test("a muted prime whose 'playing' never came doesn't stop a later real start from taking the session", async () => {
  const { music, player, eps } = setup({ routes: restored(1) });
  await waitFor(() => expect(player().current?.id).toBe(1));
  const btn = document.createElement("button");
  btn.addEventListener("click", () => eps().play([ep(1)], 0));
  document.body.appendChild(btn);
  act(() => btn.click());
  btn.remove();
  act(() => player().play());
  act(() => music.fire("playing"));
  expect(ownsSession("music")).toBe(true);
  expect(eps().active).toBe(false);
});

// ---- Unmounting, the car, the lock screen and hiding the page ----

test("unmounting (logout, user switch) during an episode saves it, stops it and hands the session back to music", async () => {
  const { f, episode, eps, unmount } = setup();
  act(() => eps().play([ep(1)], 0));
  episode.currentTime = 321;
  unmount();
  expect(ownsSession("music")).toBe(true);
  expect(episode.paused).toBe(true);
  expect(episode.removeAttribute).toHaveBeenCalledWith("src");
  expect(episode.load).toHaveBeenCalled();
  expect(progressCalls(f)).toEqual([["/api/v1/episodes/episode0001/progress", { position_s: 321 }]]);
  // The next user's player starts as the owner: tap-to-start works as usual.
  const next = setup({ onOpen: "shuffle_favorites", routes: favs(7, 8), tweak: (a) => a.play.mockImplementationOnce(notAllowed) });
  await waitFor(() => expect(next.player().needsTap).toBe(true));
  const plays = next.music.play.mock.calls.length;
  act(() => document.body.click());
  expect(next.music.play.mock.calls.length).toBe(plays + 1);
  expect(next.music.paused).toBe(false);
});

test("a car's Media Session \"play\" on connect never pauses a playing episode", () => {
  const ms = stubMediaSession();
  const { episode, eps } = setup();
  act(() => eps().play([ep(1)], 0));
  const handler = (a: string) => ms.setActionHandler.mock.calls.filter(([x]) => x === a).at(-1)![1] as () => void;
  episode.pause.mockClear();
  act(() => handler("play")());
  expect(episode.pause).not.toHaveBeenCalled();
  expect(episode.paused).toBe(false);
  act(() => handler("pause")());
  expect(episode.paused).toBe(true);
  act(() => handler("play")());
  expect(episode.paused).toBe(false);
});

test("closing the episode player clears the lock screen when music has nothing to show", () => {
  const ms = stubMediaSession();
  const { eps } = setup();
  act(() => eps().play([ep(1)], 0));
  expect(ms.metadata).not.toBeNull();
  act(() => eps().close());
  expect(ms.metadata).toBeNull();
});

test("closing the episode player puts music's song back on the lock screen", () => {
  const ms = stubMediaSession();
  const { music, player, eps } = setup();
  act(() => player().playList([tr(1)], 0));
  act(() => music.fire("playing"));
  act(() => eps().play([ep(1)], 0));
  act(() => eps().close());
  expect((ms.metadata as { init: MediaMetadataInit }).init.title).toBe("t1");
});

test("an AbortError from resuming (a quick pause) shows no error", async () => {
  const { episode, eps } = setup();
  act(() => eps().play([ep(1)], 0));
  act(() => eps().pause());
  episode.play.mockImplementationOnce(() => Promise.reject(Object.assign(new Error("aborted"), { name: "AbortError" })));
  act(() => eps().toggle());
  await act(async () => {});
  expect(eps().error).toBeNull();
  episode.play.mockImplementationOnce(() => Promise.reject(Object.assign(new Error("bad"), { name: "NotSupportedError" })));
  act(() => eps().toggle());
  await waitFor(() => expect(eps().error).toBe("无法播放这一集"));
});

test("hiding the page saves the position once, with keepalive (visibilitychange then pagehide)", () => {
  const { f, episode, eps } = setup();
  act(() => eps().play([ep(1)], 0));
  episode.currentTime = 77;
  const hidden = (v: "hidden" | "visible") => Object.defineProperty(document, "visibilityState", { value: v, configurable: true });
  try {
    hidden("hidden");
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    act(() => window.dispatchEvent(new Event("pagehide")));
    const calls = f.mock.calls.filter(([u]) => String(u).includes("/progress"));
    expect(calls).toHaveLength(1);
    expect((calls[0][1] as RequestInit).keepalive).toBe(true);
    expect(JSON.parse((calls[0][1] as RequestInit).body as string)).toEqual({ position_s: 77 });
    // Back, then hidden again: saved again.
    hidden("visible");
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    episode.currentTime = 90;
    hidden("hidden");
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    expect(progressCalls(f).at(-1)).toEqual(["/api/v1/episodes/episode0001/progress", { position_s: 90 }]);
    expect(progressCalls(f)).toHaveLength(2);
  } finally {
    delete (document as unknown as { visibilityState?: unknown }).visibilityState;
  }
});

// ---- The episode queue ----

const progressOk = (...ns: number[]): Routes =>
  Object.fromEntries(ns.map((n) => [`PUT /api/v1/episodes/episode000${n}/progress`, () => ({ status: 204 })]));
const titles = (l: Episode[]) => l.map((x) => x.title).join(",");

test("playList queues the rest of the list without played episodes, in the chosen order", () => {
  const { episode, eps } = setup({ routes: progressOk(3, 4) });
  const list = [ep(4), ep(3, { played: true }), ep(2), ep(1, { played: true })];
  act(() => eps().playList(list, 2)); // tapped 第2集, newest first
  expect(titles(eps().queue)).toBe("第4集,第2集");
  expect(eps().current?.title).toBe("第2集");
  expect(episode.src).toContain("episode0002");
  // A played episode tapped directly still plays.
  act(() => eps().playList(list, 1, { order: "oldest" }));
  expect(titles(eps().queue)).toBe("第2集,第3集,第4集");
  expect(eps().current?.title).toBe("第3集");
  // Play all (no tapped episode), including played, oldest first.
  act(() => eps().playList(list, -1, { order: "oldest", includePlayed: true }));
  expect(titles(eps().queue)).toBe("第1集,第2集,第3集,第4集");
  expect(eps().index).toBe(0);
  expect(eps().order).toBe("oldest");
  expect(eps().includePlayed).toBe(true);
});

test("ended moves to the next queued episode at its saved position, or the start when played", () => {
  const { f, episode, eps } = setup({ routes: progressOk(3) });
  act(() => eps().playList([ep(1), ep(2, { position_s: 300 }), ep(3, { played: true, position_s: 50 })], -1, { order: "oldest", includePlayed: true }));
  act(() => episode.fire("ended"));
  expect(progressCalls(f).at(-1)).toEqual(["/api/v1/episodes/episode0001/progress", { position_s: 0, played: true }]);
  expect(episode.src).toContain("episode0002");
  act(() => episode.fire("loadedmetadata"));
  expect(episode.currentTime).toBe(300);
  expect(eps().queue[0].played).toBe(true); // the finished one now counts as played
  act(() => episode.fire("ended"));
  expect(episode.src).toContain("episode0003");
  act(() => episode.fire("loadedmetadata"));
  expect(episode.currentTime).toBe(0);
  // The last one ending stops there.
  act(() => episode.fire("ended"));
  expect(episode.src).toContain("episode0003");
  expect(eps().index).toBe(2);
});

test("lock-screen ⏮/⏭ move in the queue; ⏮ more than 3 s in restarts the episode", () => {
  const ms = stubMediaSession();
  const { episode, eps } = setup({ routes: progressOk(3) });
  act(() => eps().playList([ep(3), ep(2), ep(1)], 0));
  const handler = (a: string) => ms.setActionHandler.mock.calls.filter(([x]) => x === a).at(-1)![1] as () => void;
  act(() => handler("nexttrack")());
  expect(eps().current?.title).toBe("第2集");
  episode.currentTime = 3.5;
  act(() => handler("previoustrack")());
  expect(eps().current?.title).toBe("第2集");
  expect(episode.currentTime).toBe(0);
  episode.currentTime = 2.5;
  act(() => handler("previoustrack")());
  expect(eps().current?.title).toBe("第3集");
  // "play" from the car still only resumes.
  episode.pause.mockClear();
  act(() => handler("play")());
  expect(episode.pause).not.toHaveBeenCalled();
});

test("changing the order or including played keeps the current episode playing", () => {
  const { episode, eps } = setup({ routes: progressOk(3) });
  act(() => eps().playList([ep(3), ep(2, { played: true }), ep(1)], 0));
  expect(titles(eps().queue)).toBe("第3集,第1集");
  const src = episode.src;
  act(() => eps().setOrder("oldest"));
  expect(titles(eps().queue)).toBe("第1集,第3集");
  expect(eps().current?.title).toBe("第3集");
  act(() => eps().setIncludePlayed(true));
  expect(titles(eps().queue)).toBe("第1集,第2集,第3集");
  expect(eps().current?.title).toBe("第3集");
  act(() => eps().setOrder("shuffle"));
  expect(eps().index).toBe(0);
  expect(eps().current?.title).toBe("第3集");
  expect(episode.src).toBe(src); // never reloaded
  act(() => eps().jump(2));
  expect(episode.src).not.toBe(src);
  expect(eps().index).toBe(2);
});

// A queue stored by an earlier visit: 第1-3集 oldest first, 第2集 current.
function storeQueue() {
  const first = setup({ routes: progressOk(3) });
  act(() => first.eps().playList([ep(3), ep(2), ep(1)], 1, { order: "oldest" }));
  first.unmount();
  resetSessionOwner();
  cleanup();
}
const fresh2: Routes = { ...progressOk(3), "GET /api/v1/episodes/episode0002": () => ({ body: ep(2, { position_s: 444 }) }) };

test("the queue survives a reload: restored inactive (music keeps its player), 继续收听 resumes it", async () => {
  storeQueue();
  const { episode, eps, music } = setup({ routes: fresh2, shell: true });
  await waitFor(() => expect(eps().current?.position_s).toBe(444));
  expect(titles(eps().queue)).toBe("第1集,第2集,第3集");
  expect(eps().order).toBe("oldest");
  expect(eps().active).toBe(false);
  expect(eps().playing).toBe(false);
  expect(episode.play).not.toHaveBeenCalled();
  expect(ownsSession("music")).toBe(true);
  expect(document.querySelector(".episode-mini")).toBeNull();
  const resume = screen.getByRole("button", { name: "继续收听 第2集" });
  expect(resume.closest("[data-no-music-prime]")).not.toBeNull();
  act(() => resume.click());
  expect(episode.src).toContain("episode0002");
  expect(ownsSession("episode")).toBe(true);
  expect(eps().active).toBe(true);
  expect(music.play).not.toHaveBeenCalled();
  expect(document.querySelector(".episode-mini")).not.toBeNull();
  act(() => episode.fire("loadedmetadata"));
  expect(episode.currentTime).toBe(444);
  // Closing forgets it.
  act(() => eps().close());
  expect(localStorage.getItem("lark.episodeQueue.0")).toBeNull();
});

test("a restored queue never hides music that starts on open (shuffle favorites)", async () => {
  storeQueue();
  const { music, player, eps } = setup({ onOpen: "shuffle_favorites", routes: { ...fresh2, ...favs(7, 8) }, shell: true });
  await waitFor(() => expect(player().current?.id).toBe(7));
  act(() => music.fire("playing"));
  expect(eps().active).toBe(false);
  expect(document.querySelector(".episode-mini")).toBeNull();
  expect(screen.getByText("t7")).toBeInTheDocument(); // the music mini player
  expect(screen.queryByRole("button", { name: /继续收听/ })).toBeNull(); // music is playing
});

test("继续收听 shows only while music owns the session (not over a preview or a video)", async () => {
  storeQueue();
  setup({ routes: fresh2, shell: true });
  expect(await screen.findByRole("button", { name: "继续收听 第2集" })).toBeInTheDocument();
  act(() => claimSession("preview"));
  expect(screen.queryByRole("button", { name: /继续收听/ })).toBeNull();
  act(() => claimSession("music"));
  expect(screen.getByRole("button", { name: "继续收听 第2集" })).toBeInTheDocument();
  act(() => claimSession("video"));
  expect(screen.queryByRole("button", { name: /继续收听/ })).toBeNull();
});

test("a restored queue never hides music resumed by a tap", async () => {
  storeQueue();
  const { music, player, eps } = setup({ routes: { ...fresh2, ...restored(1) }, shell: true });
  await waitFor(() => expect(player().current?.id).toBe(1));
  expect(screen.getByText("t1")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "继续收听 第2集" })).toBeInTheDocument();
  act(() => player().play());
  act(() => music.fire("playing"));
  expect(eps().active).toBe(false);
  expect(screen.getByText("t1")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /继续收听/ })).toBeNull();
});

test("music really playing makes a stale active episode step aside even when music already owns the session", () => {
  const { music, episode, eps } = setup({ shell: true });
  act(() => eps().playList([ep(1)], 0));
  act(() => episode.pause());
  resetSessionOwner(); // the owner says music, the episode UI still shows
  expect(eps().active).toBe(true);
  music.src = "/api/v1/tracks/1/stream?quality=high";
  act(() => music.fire("playing"));
  expect(eps().active).toBe(false);
});

const gone = (...ns: number[]): Routes =>
  Object.fromEntries(ns.map((n) => [`GET /api/v1/episodes/episode000${n}/stream`, () => ({ status: 410, body: { code: "episode_expired" } })]));
const streamChecks = (f: ReturnType<typeof mockFetch>) => f.mock.calls.filter(([u]) => String(u).includes("/stream")).length;

test("an episode whose file is gone (410) is skipped; one that fails mid-way is not", async () => {
  const { episode, eps } = setup({ routes: { ...progressOk(3), ...gone(1) } });
  act(() => eps().playList([ep(1), ep(2), ep(3)], 0, { order: "oldest" }));
  act(() => episode.fire("error"));
  await waitFor(() => expect(episode.src).toContain("episode0002"));
  act(() => episode.fire("playing"));
  act(() => episode.fire("error"));
  await act(async () => {});
  expect(episode.src).toContain("episode0002");
  expect(eps().error).toBe("无法播放这一集");
});

test("a network error (offline, a blip) keeps the episode and asks nothing", async () => {
  const { f, episode, eps } = setup({ routes: { ...progressOk(3), ...gone(1, 2) } });
  act(() => eps().playList([ep(1), ep(2), ep(3)], 0, { order: "oldest" }));
  episode.error = { code: 2 }; // MEDIA_ERR_NETWORK
  act(() => episode.dispatchEvent(new Event("error")));
  await act(async () => {});
  expect(episode.src).toContain("episode0001");
  expect(streamChecks(f)).toBe(0);
  expect(eps().error).toBe("无法播放这一集");
});

test("when the check itself fails (offline), or the file is fine, the episode stays", async () => {
  const { episode, eps } = setup({ routes: progressOk(3) }); // no stream route: the check fails
  act(() => eps().playList([ep(1), ep(2)], 0, { order: "oldest" }));
  act(() => episode.fire("error"));
  await act(async () => {});
  expect(episode.src).toContain("episode0001");
});

test("at most 3 episodes in a row are skipped", async () => {
  const many = [1, 2, 3, 4, 5].map((n) => ep(n));
  const { episode, eps } = setup({ routes: { ...progressOk(3, 4, 5), ...gone(1, 2, 3, 4, 5) } });
  act(() => eps().playList(many, 0, { order: "oldest" }));
  for (const next of ["episode0002", "episode0003", "episode0004"]) {
    act(() => episode.fire("error"));
    await waitFor(() => expect(episode.src).toContain(next));
  }
  act(() => episode.fire("error"));
  await act(async () => {});
  await act(async () => {});
  expect(episode.src).toContain("episode0004");
  expect(eps().index).toBe(3);
});

test("the lock screen shows the episode the queue moved on to", () => {
  const ms = stubMediaSession();
  const { episode, eps } = setup({ routes: progressOk(3) });
  act(() => eps().playList([ep(1), ep(2)], 0, { order: "oldest" }));
  act(() => episode.fire("ended"));
  act(() => episode.fire("play"));
  expect((ms.metadata as { init: MediaMetadataInit }).init.title).toBe("第2集");
});

test("a queue step after music took over claims the session back: music and an episode never play together", () => {
  const { music, episode, player, eps } = setup({ routes: progressOk(3) });
  act(() => eps().playList([ep(1), ep(2)], 0, { order: "oldest" }));
  act(() => player().playList([tr(1)], 0));
  act(() => music.fire("playing"));
  expect(episode.paused).toBe(true);
  expect(eps().active).toBe(false);
  music.pause.mockClear();
  act(() => eps().next());
  expect(ownsSession("episode")).toBe(true);
  expect(music.pause).toHaveBeenCalled();
  expect(music.paused).toBe(true);
  expect(eps().current?.title).toBe("第2集");
});

test("the episode queue sheet's move and remove keep the current episode playing and are stored", () => {
  const { episode, eps } = setup({ routes: progressOk(3) });
  act(() => eps().playList([ep(3), ep(2), ep(1)], 1, { order: "oldest" })); // 第1集,第2集,第3集 on 第2集
  const src = episode.src;
  act(() => eps().move(1, 2)); // the current one, to the end
  expect(titles(eps().queue)).toBe("第1集,第3集,第2集");
  expect(eps().index).toBe(2);
  act(() => eps().removeAt(0));
  expect(titles(eps().queue)).toBe("第3集,第2集");
  expect(eps().index).toBe(1);
  act(() => eps().removeAt(1)); // the current one: kept
  expect(titles(eps().queue)).toBe("第3集,第2集");
  expect(eps().current?.title).toBe("第2集");
  expect(episode.src).toBe(src); // never reloaded
  const stored = JSON.parse(localStorage.getItem("lark.episodeQueue.0")!) as { ids: string[]; index: number };
  expect(stored.ids).toEqual(["episode0003", "episode0002"]);
  expect(stored.index).toBe(1);
  act(() => eps().setOrder("oldest")); // the order rebuilds the list from scratch, edits gone
  expect(titles(eps().queue)).toBe("第1集,第2集,第3集");
});
