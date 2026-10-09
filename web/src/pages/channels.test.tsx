import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router";
import { afterEach, expect, test, vi } from "vitest";
import type { ChannelHit, Episode, MyChannel } from "../api/types";
import { claimSession, ownsSession, resetSessionOwner } from "../player/sessionOwner";
import { renderWithApp } from "../test/render";
import AddChannelPage from "./channels/AddChannelPage";
import ChannelsPage from "./channels/ChannelsPage";
import EpisodePage from "./channels/EpisodePage";

const LX = "UC0e5c4U67Vm6sAVK0vxN3Uw";
const ep = (n: number, extra: Partial<Episode> = {}): Episode => ({
  video_id: `episode000${n}`, channel_id: LX, channel_title: "刘翔的投资频道", title: `第${n}集`, published_at: 1_790_000_000 - n * 3600,
  duration_s: 1800, kind: "video", thumbnail: `/api/v1/episodes/episode000${n}/thumbnail`,
  audio: { status: "done", progress: 100, bytes: 1, error: "" }, video: null, position_s: 0, played: false, kept: false, ...extra,
});
const hit = (extra: Partial<ChannelHit> = {}): ChannelHit => ({
  id: LX, title: "刘翔的投资频道", handle: "@liu-xiang", avatar: "", description: "投资", followers: 138000, following: false, ...extra,
});
const mine = (extra: Partial<MyChannel> = {}): MyChannel => ({
  channel: { id: LX, title: "刘翔的投资频道", handle: "@liu-xiang", avatar: "", description: "", polled_at: 1, last_error: "" },
  settings: { media: "audio", keep_days: null, paused: false, include_shorts: false, include_live: false },
  followed_at: 1, unplayed: 2, latest_published_at: 1, ...extra,
});

afterEach(() => resetSessionOwner());

test("最新 lists episodes with their status; ▶ plays the downloaded ones as an episode queue", async () => {
  const { episodeAudio } = renderWithApp(<ChannelsPage />, {
    path: "/channels",
    routes: {
      "GET /api/v1/episodes/latest": () => ({ body: { items: [ep(1), ep(2, { audio: { status: "downloading", progress: 42, bytes: 0, error: "" } }), ep(3, { position_s: 300 })], unplayed: 2 } }),
      "PUT /api/v1/episodes/episode0001/progress": () => ({ status: 204 }),
    },
  });
  const row2 = (await screen.findByText("第2集")).closest("li")!;
  expect(within(row2).getByText("下载中 42%")).toBeInTheDocument();
  expect(within(row2).queryByRole("button", { name: "听" })).toBeNull();
  const row3 = screen.getByText("第3集").closest("li")!;
  expect(row3.querySelector(".episode-progress")).not.toBeNull();
  fireEvent.click(within(screen.getByText("第1集").closest("li")!).getByRole("button", { name: "听" }));
  expect(episodeAudio.src).toContain("/api/v1/episodes/episode0001/stream?kind=audio");
});

test("添加频道: a pasted link resolves to a card that follows by id; words search channels", async () => {
  const user = userEvent.setup();
  const { f } = renderWithApp(<AddChannelPage />, {
    path: "/channels/add",
    routes: {
      "POST /api/v1/channels/resolve": () => ({ body: hit() }),
      "POST /api/v1/channels/follow": () => ({ status: 201, body: mine() }),
      "GET /api/v1/channels/search": () => ({ body: { channels: [hit({ id: "UCRABK12_6Ie2X549K9cXS0g", title: "刘翔", following: true })] } }),
    },
  });
  await user.type(screen.getByLabelText("搜索或粘贴频道链接"), "https://www.youtube.com/@liu-xiang");
  await user.click(screen.getByRole("button", { name: "查找" }));
  const card = (await screen.findByText("刘翔的投资频道")).closest("li")!;
  expect(within(card).getByText(/@liu-xiang/)).toBeInTheDocument();
  await user.click(within(card).getByRole("button", { name: "关注" }));
  await within(card).findByRole("button", { name: "已关注" });
  const post = f.mock.calls.find(([u, i]) => String(u).endsWith("/channels/follow") && (i as RequestInit).method === "POST")!;
  expect(JSON.parse((post[1] as RequestInit).body as string)).toEqual({ id: LX });
  await user.clear(screen.getByLabelText("搜索或粘贴频道链接"));
  await user.type(screen.getByLabelText("搜索或粘贴频道链接"), "刘翔");
  await user.click(screen.getByRole("button", { name: "查找" }));
  const other = (await screen.findByText("刘翔")).closest("li")!;
  expect(within(other).getByRole("button", { name: "已关注" })).toBeInTheDocument();
});

test("我的频道: settings are saved, unfollow asks first", async () => {
  const user = userEvent.setup();
  vi.stubGlobal("confirm", vi.fn(() => true));
  const { f } = renderWithApp(<ChannelsPage />, {
    path: "/channels?tab=mine",
    routes: {
      "GET /api/v1/channels": () => ({ body: { channels: [mine()], usage: { bytes: 5 * 1024 ** 3, files: 3, max_bytes: 200 * 1024 ** 3 }, default_keep_days: 15 } }),
      "PUT /api/v1/channels/UC0e5c4U67Vm6sAVK0vxN3Uw/settings": (init) => ({ body: JSON.parse(init.body as string) }),
      "DELETE /api/v1/channels/UC0e5c4U67Vm6sAVK0vxN3Uw/follow": () => ({ status: 204 }),
    },
  });
  const row = (await screen.findByText("刘翔的投资频道")).closest("li")!;
  expect(screen.getByText("已用 5 GB，上限 200 GB")).toBeInTheDocument();
  expect(within(row).getByText("2 个新节目")).toBeInTheDocument();
  await user.click(within(row).getByRole("button", { name: "设置" }));
  await user.click(within(row).getByLabelText("音频和视频（720p）"));
  await user.selectOptions(within(row).getByLabelText("节目保留"), "30");
  await user.click(within(row).getByLabelText("包括 Shorts"));
  await user.click(within(row).getByRole("button", { name: "保存" }));
  await within(row).findByText("已保存");
  const put = f.mock.calls.find(([u, i]) => String(u).endsWith("/settings") && (i as RequestInit).method === "PUT")!;
  expect(JSON.parse((put[1] as RequestInit).body as string)).toEqual({ media: "video", keep_days: 30, paused: false, include_shorts: true, include_live: false });
  await user.click(within(row).getByRole("button", { name: "取消关注" }));
  expect(window.confirm).toHaveBeenCalledWith("不再关注 刘翔的投资频道？");
  await waitFor(() => expect(screen.queryByText("刘翔的投资频道")).toBeNull());
});

function renderEpisode(e: Episode, routes = {}) {
  return renderWithApp(<Routes><Route path="/episodes/:id" element={<EpisodePage />} /></Routes>, {
    path: `/episodes/${e.video_id}`,
    routes: {
      [`GET /api/v1/episodes/${e.video_id}`]: () => ({ body: e }),
      [`PUT /api/v1/episodes/${e.video_id}/progress`]: () => ({ status: 204 }),
      [`PUT /api/v1/episodes/${e.video_id}/keep`]: () => ({ status: 204 }),
      [`PUT /api/v1/episodes/${e.video_id}/hide`]: () => ({ status: 204 }),
      ...routes,
    },
  });
}

test("the episode page resumes, keeps, marks played and sets the speed", async () => {
  const user = userEvent.setup();
  const { f, episodeAudio } = renderEpisode(ep(1, { position_s: 754, description: "第一行\n第二行\n第三行\n第四行" }));
  const listen = await screen.findByRole("button", { name: "▶ 听 · 从 12:34 继续" });
  expect(screen.getByText(/第一行/)).toBeInTheDocument();
  expect(screen.queryByText(/第四行/)).toBeNull();
  await user.click(screen.getByRole("button", { name: "更多" }));
  expect(screen.getByText(/第四行/)).toBeInTheDocument();
  fireEvent.click(listen);
  act(() => episodeAudio.fire("loadedmetadata"));
  expect(episodeAudio.currentTime).toBe(754);
  await user.click(screen.getByRole("button", { name: "1.5×" }));
  expect(episodeAudio.playbackRate).toBe(1.5);
  await user.click(screen.getByRole("button", { name: "保留" }));
  await screen.findByRole("button", { name: "已保留" });
  await user.click(screen.getByRole("button", { name: "标记为已播放" }));
  const last = f.mock.calls.filter(([u]) => String(u).endsWith("/progress")).at(-1)!;
  expect(JSON.parse((last[1] as RequestInit).body as string)).toEqual({ position_s: 0, played: true });
  await screen.findByRole("button", { name: "标记为未播放" });
});

test("▶ 看 plays the video; hidden on an iPhone it offers to continue as audio from the same second", async () => {
  const { episodeAudio } = renderEpisode(ep(1, { video: { status: "done", progress: 100, bytes: 1, error: "" } }));
  episodeAudio.play = vi.fn(() => Promise.reject(Object.assign(new Error("not allowed"), { name: "NotAllowedError" })));
  fireEvent.click(await screen.findByRole("button", { name: "▶ 看" }));
  const video = document.querySelector("video")!;
  expect(video.getAttribute("src")).toBe("/api/v1/episodes/episode0001/stream?kind=video");
  video.currentTime = 321;
  fireEvent.play(video);
  Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
  fireEvent(document, new Event("visibilitychange"));
  fireEvent.pause(video);
  expect(episodeAudio.src).toContain("/episodes/episode0001/stream?kind=audio"); // tried at once
  Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
  fireEvent(document, new Event("visibilitychange"));
  const offer = await screen.findByRole("button", { name: "继续以音频播放" });
  expect(screen.getByText("视频在后台停止了。")).toBeInTheDocument();
  fireEvent.click(offer);
  act(() => episodeAudio.fire("loadedmetadata"));
  expect(episodeAudio.currentTime).toBe(321);
  expect(episodeAudio.play).toHaveBeenCalledTimes(2);
  delete (document as unknown as { visibilityState?: string }).visibilityState;
});

test("不再显示 hides the episode for me and goes back", async () => {
  const user = userEvent.setup();
  const { f } = renderEpisode(ep(1));
  await user.click(await screen.findByRole("button", { name: "不再显示" }));
  expect(f.mock.calls.some(([u, i]) => String(u).endsWith("/episodes/episode0001/hide") && (i as RequestInit).method === "PUT")).toBe(true);
});

test("an episode not downloaded yet says so instead of offering ▶ 听", async () => {
  renderEpisode(ep(1, { audio: { status: "queued", progress: 0, bytes: 0, error: "" } }));
  expect(await screen.findByText("等待下载")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /^▶ 听/ })).toBeNull();
  expect(screen.getByRole("button", { name: "▶ 试听" })).toBeInTheDocument();
});

test("the episode video owns the lock screen while it plays, and gives the session back when the page goes", async () => {
  const ms = { setActionHandler: vi.fn(), setPositionState: vi.fn(), metadata: null as unknown, playbackState: "none" };
  Object.defineProperty(navigator, "mediaSession", { value: ms, configurable: true, writable: true });
  vi.stubGlobal("MediaMetadata", class { constructor(public init: MediaMetadataInit) {} });
  try {
    renderEpisode(ep(1, { video: { status: "done", progress: 100, bytes: 1, error: "" } }));
    fireEvent.click(await screen.findByRole("button", { name: "▶ 看" }));
    const video = document.querySelector("video")!;
    fireEvent.play(video);
    expect(ownsSession("video")).toBe(true);
    expect((ms.metadata as { init: MediaMetadataInit }).init.title).toBe("第1集");
    const handler = (a: string) => ms.setActionHandler.mock.calls.filter(([n]) => n === a).at(-1)?.[1] as (() => void) | null;
    vi.mocked(video.pause).mockClear();
    handler("pause")!();
    expect(video.pause).toHaveBeenCalled();
    expect(handler("nexttrack")).toBeNull();
    cleanup();
    expect(ownsSession("music")).toBe(true);
    expect(ms.metadata).toBeNull();
  } finally {
    delete (navigator as unknown as { mediaSession?: unknown }).mediaSession;
  }
});

const videoDone = { video: { status: "done" as const, progress: 100, bytes: 1, error: "" } };
const progressBodies = (f: ReturnType<typeof renderWithApp>["f"]) =>
  f.mock.calls.filter(([u]) => String(u).endsWith("/progress")).map(([, i]) => ({ ...JSON.parse((i as RequestInit).body as string), keepalive: (i as RequestInit).keepalive === true }));
const setVisibility = (v: "hidden" | "visible") => {
  Object.defineProperty(document, "visibilityState", { value: v, configurable: true });
  fireEvent(document, new Event("visibilitychange"));
};

// A long episode watched as video keeps its place like a listened one.
test("watching resumes at the saved second and saves every 15 s, on pause and once per hide; ▶ 听 goes on from there", async () => {
  const { f, episodeAudio } = renderEpisode(ep(1, { position_s: 100, ...videoDone }));
  fireEvent.click(await screen.findByRole("button", { name: "▶ 看" }));
  let now = 1_000_000;
  vi.spyOn(Date, "now").mockImplementation(() => now);
  try {
    const video = document.querySelector("video")!;
    fireEvent.loadedMetadata(video);
    expect(video.currentTime).toBe(100);
    fireEvent.play(video);
    video.currentTime = 110;
    fireEvent.timeUpdate(video);
    expect(progressBodies(f)).toEqual([]); // not 15 s yet
    now += 16_000;
    video.currentTime = 130;
    fireEvent.timeUpdate(video);
    expect(progressBodies(f)).toEqual([{ position_s: 130, keepalive: false }]);
    // Page hide: visibilitychange and pagehide both fire, one keepalive save.
    video.currentTime = 135;
    setVisibility("hidden");
    fireEvent(window, new Event("pagehide"));
    setVisibility("visible");
    expect(progressBodies(f).slice(1)).toEqual([{ position_s: 135, keepalive: true }]);
    video.currentTime = 140;
    fireEvent.pause(video);
    expect(progressBodies(f).at(-1)).toEqual({ position_s: 140, keepalive: false });
    expect(episodeAudio.src).toBe(""); // a pause in the foreground is just a pause
  } finally {
    vi.mocked(Date.now).mockRestore();
    delete (document as unknown as { visibilityState?: string }).visibilityState;
  }
  fireEvent.click(screen.getByRole("button", { name: "▶ 听 · 从 2:20 继续" }));
  act(() => episodeAudio.fire("loadedmetadata"));
  expect(episodeAudio.currentTime).toBe(140);
});

test("a video watched to the end is marked played", async () => {
  const { f } = renderEpisode(ep(1, { position_s: 100, ...videoDone }));
  fireEvent.click(await screen.findByRole("button", { name: "▶ 看" }));
  const video = document.querySelector("video")!;
  fireEvent.play(video);
  fireEvent.ended(video);
  expect(progressBodies(f).at(-1)).toEqual({ position_s: 0, played: true, keepalive: false });
  expect(await screen.findByRole("button", { name: "标记为未播放" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "▶ 听" })).toBeInTheDocument();
});

test("iOS pausing the video just before it reports the page hidden still goes on as audio", async () => {
  const { episodeAudio } = renderEpisode(ep(1, videoDone));
  fireEvent.click(await screen.findByRole("button", { name: "▶ 看" }));
  const video = document.querySelector("video")!;
  fireEvent.play(video);
  video.currentTime = 200;
  fireEvent.pause(video);
  expect(episodeAudio.src).toBe("");
  try {
    setVisibility("hidden");
    expect(episodeAudio.src).toContain("/episodes/episode0001/stream?kind=audio");
  } finally {
    delete (document as unknown as { visibilityState?: string }).visibilityState;
  }
  act(() => episodeAudio.fire("loadedmetadata"));
  expect(episodeAudio.currentTime).toBe(200);
});

test("a failed 保留 shows an error but keeps the page and the playing video", async () => {
  renderEpisode(ep(1, videoDone), { "PUT /api/v1/episodes/episode0001/keep": () => ({ status: 500, body: { error: "boom", code: "server_error" } }) });
  fireEvent.click(await screen.findByRole("button", { name: "▶ 看" }));
  const video = document.querySelector("video")!;
  fireEvent.play(video);
  fireEvent.click(screen.getByRole("button", { name: "保留" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("服务器出错了");
  expect(document.querySelector("video")).toBe(video);
  expect(ownsSession("video")).toBe(true);
});

test("不再显示 on an episode opened directly goes to 频道", async () => {
  const user = userEvent.setup();
  const e = ep(1);
  renderWithApp(
    <Routes>
      <Route path="/episodes/:id" element={<EpisodePage />} />
      <Route path="/channels" element={<p>频道页</p>} />
    </Routes>,
    { path: "/episodes/episode0001", routes: { "GET /api/v1/episodes/episode0001": () => ({ body: e }), "PUT /api/v1/episodes/episode0001/hide": () => ({ status: 204 }) } },
  );
  await user.click(await screen.findByRole("button", { name: "不再显示" }));
  expect(await screen.findByText("频道页")).toBeInTheDocument();
});

test("最新 asks again only while the page is visible, and at once on coming back", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    const { f } = renderWithApp(<ChannelsPage />, {
      path: "/channels",
      routes: { "GET /api/v1/episodes/latest": () => ({ body: { items: [ep(1)], unplayed: 1 } }) },
    });
    const latest = () => f.mock.calls.filter(([u]) => String(u).includes("/episodes/latest")).length;
    await screen.findByText("第1集");
    expect(latest()).toBe(1);
    setVisibility("hidden");
    await act(() => vi.advanceTimersByTimeAsync(65_000));
    expect(latest()).toBe(1);
    setVisibility("visible");
    expect(latest()).toBe(2);
    await act(() => vi.advanceTimersByTimeAsync(30_000));
    expect(latest()).toBe(3);
  } finally {
    delete (document as unknown as { visibilityState?: string }).visibilityState;
    vi.useRealTimers();
  }
});

// Music taking over an already paused video must not leave a
// stale "our own pause" behind, or the next background handoff is lost.
test("a video paused before music played still goes on as audio when the page hides later", async () => {
  const { episodeAudio } = renderEpisode(ep(1, videoDone));
  fireEvent.click(await screen.findByRole("button", { name: "▶ 看" }));
  const video = document.querySelector("video")!;
  let paused = false;
  Object.defineProperty(video, "paused", { get: () => paused, configurable: true });
  fireEvent.play(video);
  video.currentTime = 50;
  paused = true;
  fireEvent.pause(video); // the user pauses it
  act(() => claimSession("music")); // music plays meanwhile
  paused = false;
  video.currentTime = 60;
  fireEvent.play(video); // back to the video: it reclaims the session
  expect(ownsSession("video")).toBe(true);
  try {
    video.currentTime = 75;
    setVisibility("hidden");
    paused = true;
    fireEvent.pause(video); // iOS stops it in the background
    expect(episodeAudio.src).toContain("/episodes/episode0001/stream?kind=audio");
  } finally {
    delete (document as unknown as { visibilityState?: string }).visibilityState;
  }
  act(() => episodeAudio.fire("loadedmetadata"));
  expect(episodeAudio.currentTime).toBe(75);
});

// After the video ended, ▶ 听 and 继续以音频播放 start from the top, not the end.
test("once the video has ended, 继续以音频播放 starts from the beginning", async () => {
  const { episodeAudio } = renderEpisode(ep(1, videoDone));
  fireEvent.click(await screen.findByRole("button", { name: "▶ 看" }));
  const video = document.querySelector("video")!;
  Object.defineProperty(video, "ended", { get: () => true, configurable: true });
  fireEvent.play(video);
  video.currentTime = 1800;
  fireEvent.ended(video);
  fireEvent.click(await screen.findByRole("button", { name: "继续以音频播放" }));
  act(() => episodeAudio.fire("loadedmetadata"));
  expect(episodeAudio.currentTime).toBe(0);
});

test("once the video has ended, ▶ 听 starts from the beginning", async () => {
  const { episodeAudio } = renderEpisode(ep(1, videoDone));
  fireEvent.click(await screen.findByRole("button", { name: "▶ 看" }));
  const video = document.querySelector("video")!;
  Object.defineProperty(video, "ended", { get: () => true, configurable: true });
  fireEvent.play(video);
  video.currentTime = 1800;
  fireEvent.ended(video);
  fireEvent.click(await screen.findByRole("button", { name: "▶ 听" }));
  act(() => episodeAudio.fire("loadedmetadata"));
  expect(episodeAudio.currentTime).toBe(0);
});

// A file the server gave up on as too long to download says so.
test("an episode too long to download says so", async () => {
  renderEpisode(ep(1, { audio: { status: "failed", progress: 0, bytes: 0, error: "lark:too_long" } }));
  expect(await screen.findByText("下载失败：视频太长，无法及时下载")).toBeInTheDocument();
});
