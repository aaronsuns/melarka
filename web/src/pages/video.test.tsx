import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { Route, Routes } from "react-router";
import { afterEach, expect, test, vi } from "vitest";
import type { PreviewInfo, VideoHistory, VideoRecs, YTVideo } from "../api/types";
import { claimSession, ownsSession, resetSessionOwner } from "../player/sessionOwner";
import { renderWithApp } from "../test/render";
import VideoPage from "./video/VideoPage";
import WatchPage from "./video/WatchPage";

// 视频 (§18.2): the search page, 为你推荐 / 最近观看, and /watch/:id with 高清, ▶ 听, 保留.

const LX = "UC0e5c4U67Vm6sAVK0vxN3Uw";
const ID = "rvOZh8idOrU";
const card = (id: string, title: string, extra: Partial<YTVideo> = {}): YTVideo => ({
  id, title, channel: "刘翔的投资频道", channel_id: LX, url: `https://www.youtube.com/watch?v=${id}`,
  thumbnail: `/api/v1/videos/${id}/thumbnail`, duration_s: 754, ...extra,
});
const preview = (extra: Partial<PreviewInfo> = {}): PreviewInfo => ({
  id: 7, video_id: ID, media: "video", status: "downloading", title: "", channel: "", channel_id: "", duration_s: 0,
  error: "", progress: 0, queued: false, merged: false, description: "", stream_url: `/api/v1/previews/${extra.id ?? 7}/stream`, ...extra,
});
const calls = (f: { mock: { calls: unknown[][] } }, method: string, suffix: string) =>
  f.mock.calls
    .filter(([u, i]) => String(u).split("?")[0].endsWith(suffix) && ((i as RequestInit | undefined)?.method ?? "GET") === method)
    .map(([u, i]) => ({ url: String(u), body: (i as RequestInit | undefined)?.body ? JSON.parse((i as RequestInit).body as string) : undefined }));

afterEach(() => {
  resetSessionOwner();
  vi.useRealTimers();
});

const pages = (
  <Routes>
    <Route path="/video" element={<VideoPage />} />
    <Route path="/watch/:id" element={<WatchPage />} />
    <Route path="/episodes/:id" element={<p>episode page</p>} />
  </Routes>
);

const emptyHistory: VideoHistory = { watches: [], searches: [] };
const emptyRecs: VideoRecs = { items: [], refreshed_at: null, refreshing: false };

test("search shows cards; a submitted search records history; titles are text", async () => {
  const user = userEvent.setup();
  const { f } = renderWithApp(pages, {
    path: "/video",
    routes: {
      "GET /api/v1/me/video-history": () => ({ body: emptyHistory }),
      "GET /api/v1/me/video-recommendations": () => ({ body: emptyRecs }),
      "GET /api/v1/videos/search": () => ({ body: { videos: [card(ID, '<img src=x onerror="alert(1)">'), card("PncPZ-E1GjE", "美债")] } }),
    },
  });
  const input = screen.getByLabelText("搜索视频");
  expect(input.closest("[data-no-music-prime]")).not.toBeNull();
  await user.type(input, "刘翔{Enter}");
  const first = await screen.findByText('<img src=x onerror="alert(1)">'); // literal text
  const search = calls(f, "GET", "/videos/search");
  expect(search).toHaveLength(1);
  expect(search[0].url).toContain(`q=${encodeURIComponent("刘翔")}`);
  expect(search[0].url).toContain("record=1");
  expect(first.closest("a")?.getAttribute("href")).toBe(`/watch/${ID}`);
  expect(first.closest("[data-no-music-prime]")).not.toBeNull();
  expect(document.querySelector("img[onerror]")).toBeNull();
  expect(document.querySelector('img[src^="https://i.ytimg.com"]')).toBeNull();
  expect(document.querySelector(`img[src="/api/v1/videos/${ID}/thumbnail"]`)).not.toBeNull();
  expect(screen.getByText("美债").closest("a")).toHaveTextContent("12:34");
});

test("a search with no results says so; a failing one shows its error", async () => {
  const user = userEvent.setup();
  let fail = false;
  renderWithApp(pages, {
    path: "/video",
    routes: {
      "GET /api/v1/me/video-history": () => ({ body: emptyHistory }),
      "GET /api/v1/me/video-recommendations": () => ({ body: emptyRecs }),
      "GET /api/v1/videos/search": () => (fail ? { status: 409, body: { error: "channels off", code: "channels_off" } } : { body: { videos: [] } }),
    },
  });
  await user.type(screen.getByLabelText("搜索视频"), "无{Enter}");
  expect(await screen.findByText("没有找到视频")).toBeInTheDocument();
  fail = true;
  await user.clear(screen.getByLabelText("搜索视频"));
  await user.type(screen.getByLabelText("搜索视频"), "再试{Enter}");
  expect(await screen.findByText("此服务器已关闭频道功能")).toBeInTheDocument();
});

test("an empty box shows history chips, 为你推荐 with its reason, and 最近观看", async () => {
  const user = userEvent.setup();
  let cleared = false;
  const { f } = renderWithApp(pages, {
    path: "/video",
    routes: {
      "GET /api/v1/me/video-history": () => ({
        body: cleared ? emptyHistory : {
          searches: ["刘翔", "<b>美债</b>"],
          watches: [{ video_id: "watched0001", title: "看过的视频", channel: "频道甲", channel_id: LX, duration_s: 60, thumbnail: "/api/v1/videos/watched0001/thumbnail", last_at: 1 }],
        },
      }),
      "GET /api/v1/me/video-recommendations": () => ({
        body: {
          items: [
            { video_id: "rec00000001", title: "推荐一", channel: "频道乙", channel_id: LX, duration_s: 120, thumbnail: "/api/v1/videos/rec00000001/thumbnail", score: 2, reason_kind: "watch", reason: "看过的视频" },
            { video_id: "rec00000002", title: "推荐二", channel: "频道丙", channel_id: LX, duration_s: 120, thumbnail: "/api/v1/videos/rec00000002/thumbnail", score: 1, reason_kind: "search", reason: "刘翔" },
          ],
          refreshed_at: 1, refreshing: false,
        },
      }),
      "GET /api/v1/videos/search": () => ({ body: { videos: [card(ID, "结果")] } }),
      "DELETE /api/v1/me/video-history": () => ({ status: 204 }),
    },
  });
  expect(await screen.findByRole("button", { name: "<b>美债</b>" })).toBeInTheDocument();
  expect(screen.getByRole("heading", { name: "为你推荐" })).toBeInTheDocument();
  expect(await screen.findByText("因为你看过 看过的视频")).toBeInTheDocument();
  expect(screen.getByText("因为你搜索过 刘翔")).toBeInTheDocument();
  expect(screen.getByText("推荐一").closest("a")?.getAttribute("href")).toBe("/watch/rec00000001");
  expect(screen.getByRole("heading", { name: "最近观看" })).toBeInTheDocument();
  expect(screen.getByText("看过的视频", { selector: ".video-title" }).closest("a")?.getAttribute("href")).toBe("/watch/watched0001");
  // Tapping a chip searches it (and records it, like a submitted search).
  await user.click(screen.getByRole("button", { name: "刘翔" }));
  expect(await screen.findByText("结果")).toBeInTheDocument();
  expect(calls(f, "GET", "/videos/search")[0].url).toContain("record=1");
  expect(screen.getByLabelText("搜索视频")).toHaveValue("刘翔");
  // Clearing the box goes back to the history; 清除记录 clears it.
  await user.clear(screen.getByLabelText("搜索视频"));
  await user.click(await screen.findByRole("button", { name: "清除记录" }));
  cleared = true;
  await waitFor(() => expect(calls(f, "DELETE", "/me/video-history")).toHaveLength(1));
  await waitFor(() => expect(screen.queryByRole("button", { name: "刘翔" })).toBeNull());
  expect(screen.queryByRole("heading", { name: "最近观看" })).toBeNull();
});

test("为你推荐 is asked again every 10 s while it refreshes, and stops after", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  let refreshing = true;
  const { f } = renderWithApp(pages, {
    path: "/video",
    routes: {
      "GET /api/v1/me/video-history": () => ({ body: emptyHistory }),
      "GET /api/v1/me/video-recommendations": () => ({ body: { ...emptyRecs, refreshing } }),
    },
  });
  expect(await screen.findByText("正在更新推荐…")).toBeInTheDocument();
  const gets = () => calls(f, "GET", "/me/video-recommendations").length;
  const before = gets();
  refreshing = false;
  await act(() => vi.advanceTimersByTimeAsync(10_000));
  expect(gets()).toBe(before + 1);
  expect(await screen.findByText("看几个视频或搜索一下，这里就会有推荐")).toBeInTheDocument();
  await act(() => vi.advanceTimersByTimeAsync(30_000));
  expect(gets()).toBe(before + 1);
});

test("with Channels off the 视频 page shows the server's error", async () => {
  const off = () => ({ status: 409, body: { error: "channels off", code: "channels_off" } });
  renderWithApp(pages, { path: "/video", routes: { "GET /api/v1/me/video-history": off, "GET /api/v1/me/video-recommendations": off } });
  expect((await screen.findAllByText("此服务器已关闭频道功能")).length).toBeGreaterThan(0);
});

// The watch page's routes: the 360p preview 7, the HD preview 8.
function watchRoutes(o: { video?: () => PreviewInfo; hd?: () => PreviewInfo; hdStart?: () => { status?: number; body?: unknown }; videoStart?: () => { status?: number; body?: unknown } } = {}) {
  return {
    "POST /api/v1/previews": (init: RequestInit) => {
      const media = JSON.parse(init.body as string).media;
      if (media === "hd") return o.hdStart?.() ?? { status: 201, body: preview({ id: 8, media: "hd", status: "downloading", progress: 0 }) };
      if (media === "audio") return { status: 201, body: preview({ id: 9, media: "audio", status: "done" }) };
      return o.videoStart?.() ?? { status: 201, body: preview({ id: 7, status: "downloading" }) };
    },
    "GET /api/v1/previews/7": () => ({ body: o.video?.() ?? preview({ status: "done", title: "T", channel: "刘翔的投资频道", channel_id: LX, description: "简介" }) }),
    "GET /api/v1/previews/8": () => ({ body: o.hd?.() ?? preview({ id: 8, media: "hd", status: "downloading", progress: 40 }) }),
    "GET /api/v1/previews/9": () => ({ body: preview({ id: 9, media: "audio", status: "done" }) }),
    "POST /api/v1/me/video-history/watches": () => ({ status: 204 }),
    "GET /api/v1/channels": () => ({ body: { channels: [], usage: { bytes: 0, files: 0, max_bytes: 0 }, default_keep_days: 30 } }),
    [`GET /api/v1/videos/${ID}/related`]: () => ({ body: { videos: [card("PncPZ-E1GjE", "相关的")] } }),
    "POST /api/v1/previews/7/keep": () => ({ body: { episode_id: ID } }),
    "POST /api/v1/previews/8/keep": () => ({ body: { episode_id: ID } }),
  };
}

async function waitForVideo(): Promise<HTMLVideoElement> {
  await waitFor(() => expect(document.querySelector("video.watch-video")).not.toBeNull());
  return document.querySelector("video.watch-video") as HTMLVideoElement;
}

// A <video> playing at a second: jsdom has no playback, so paused is driven here.
function playingAt(v: HTMLVideoElement, second: number) {
  let paused = false;
  Object.defineProperty(v, "paused", { get: () => paused, configurable: true });
  v.play = vi.fn(() => {
    paused = false;
    return Promise.resolve();
  });
  v.pause = vi.fn(() => {
    paused = true;
  });
  v.currentTime = second;
  fireEvent.play(v);
}

test("the watch page plays the 360p preview, records the watch, renders the description as text", async () => {
  const { f } = renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: watchRoutes({ video: () => preview({ status: "done", title: "T", channel: "刘翔的投资频道", channel_id: LX, description: "第一行\njavascript:alert(1)\n<b>x</b>\n第四行" }) }),
  });
  const v = await waitForVideo();
  expect(v.getAttribute("src")).toBe("/api/v1/previews/7/stream");
  expect(v.closest("article.watch-page[data-no-music-prime]")).not.toBeNull();
  expect(calls(f, "POST", "/api/v1/previews")[0].body).toEqual({ video_id: ID, media: "video", title: "T", channel: "刘翔的投资频道", duration_s: 754 });
  await waitFor(() => expect(calls(f, "POST", "/me/video-history/watches")).toHaveLength(1));
  expect(calls(f, "POST", "/me/video-history/watches")[0].body).toEqual({ video_id: ID, title: "T", channel: "刘翔的投资频道", channel_id: LX, duration_s: 754 });
  expect(await screen.findByText(/javascript:alert\(1\)/)).toBeInTheDocument();
  expect(screen.getByText(/<b>x<\/b>/)).toBeInTheDocument();
  expect(document.querySelector('.watch-page a[href^="javascript"]')).toBeNull();
  expect(document.querySelector(".watch-page b")).toBeNull();
  expect(screen.queryByText("第四行", { exact: false })).toBeNull(); // only 3 lines until 展开
  fireEvent.click(screen.getByRole("button", { name: "展开" }));
  expect(screen.getByText("第四行", { exact: false })).toBeInTheDocument();
  expect(screen.getByRole("heading", { name: "T" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "关注频道" })).toBeInTheDocument();
  expect((await screen.findByText("相关的")).closest("a")?.getAttribute("href")).toBe("/watch/PncPZ-E1GjE");
});

test("opened without a card (a shared link), the title comes from the preview and the watch is recorded then", async () => {
  const { f } = renderWithApp(pages, { path: `/watch/${ID}`, routes: watchRoutes() });
  await waitForVideo();
  expect(calls(f, "POST", "/api/v1/previews")[0].body).toEqual({ video_id: ID, media: "video" });
  expect(await screen.findByRole("heading", { name: "T" })).toBeInTheDocument();
  await waitFor(() => expect(calls(f, "POST", "/me/video-history/watches")).toHaveLength(1));
  expect(calls(f, "POST", "/me/video-history/watches")[0].body).toEqual({ video_id: ID, title: "T", channel: "刘翔的投资频道", channel_id: LX, duration_s: 0 });
});

test("WatchPage switches to HD at the same position and keeps playing", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  let hdDone = false;
  const { f } = renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: watchRoutes({ hd: () => preview({ id: 8, media: "hd", status: hdDone ? "done" : "downloading", progress: hdDone ? 100 : 40 }) }),
  });
  const v = await waitForVideo();
  playingAt(v, 61.5);
  fireEvent.click(screen.getByRole("button", { name: "高清" }));
  expect(await screen.findByRole("button", { name: "高清 40%" })).toBeDisabled();
  expect(calls(f, "POST", "/api/v1/previews")[1].body).toMatchObject({ video_id: ID, media: "hd" });
  expect(v.getAttribute("src")).toBe("/api/v1/previews/7/stream");
  hdDone = true;
  await act(() => vi.advanceTimersByTimeAsync(2000));
  await waitFor(() => expect(v.getAttribute("src")).toBe("/api/v1/previews/8/stream"));
  expect(document.querySelector("video.watch-video")).toBe(v); // the same element: no remount
  fireEvent.loadedMetadata(v);
  expect(v.currentTime).toBe(61.5);
  expect(v.play).toHaveBeenCalled();
  const hd = screen.getByRole("button", { name: "高清 ✓" });
  expect(hd).toHaveAttribute("aria-pressed", "true");
});

test("a paused video switched to HD stays paused at its second", async () => {
  const { f } = renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: watchRoutes({ hdStart: () => ({ status: 201, body: preview({ id: 8, media: "hd", status: "done", progress: 100 }) }) }),
  });
  const v = await waitForVideo();
  playingAt(v, 30);
  v.pause();
  fireEvent.click(screen.getByRole("button", { name: "高清" }));
  await waitFor(() => expect(v.getAttribute("src")).toBe("/api/v1/previews/8/stream"));
  expect(v.autoplay).toBe(false);
  fireEvent.loadedMetadata(v);
  expect(v.currentTime).toBe(30);
  expect(v.play).not.toHaveBeenCalled();
  expect(calls(f, "GET", "/previews/8")).toHaveLength(0); // done at once: nothing to poll
});

test("HD unavailable leaves the 360p playing and says so", async () => {
  renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: watchRoutes({ hd: () => preview({ id: 8, media: "hd", status: "failed", error: "hd_unavailable" }) }),
  });
  const v = await waitForVideo();
  playingAt(v, 10);
  fireEvent.click(screen.getByRole("button", { name: "高清" }));
  expect(await screen.findByText("这个视频没有高清")).toBeInTheDocument();
  expect(v.getAttribute("src")).toBe("/api/v1/previews/7/stream");
  expect(v.pause).not.toHaveBeenCalled();
});

test("HD failing for another reason says 高清下载失败; the 360p stays", async () => {
  renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: watchRoutes({ hdStart: () => ({ status: 500, body: { error: "boom" } }) }),
  });
  const v = await waitForVideo();
  fireEvent.click(screen.getByRole("button", { name: "高清" }));
  expect(await screen.findByText("高清下载失败")).toBeInTheDocument();
  expect(v.getAttribute("src")).toBe("/api/v1/previews/7/stream");
  expect(screen.getByRole("button", { name: "高清" })).toBeEnabled(); // tap again to retry
});

test("a video that can't be watched while it downloads starts with 高清, and says so", async () => {
  renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: watchRoutes({
      videoStart: () => ({ status: 201, body: preview({ status: "failed", error: "video_preview_unavailable" }) }),
      hdStart: () => ({ status: 201, body: preview({ id: 8, media: "hd", status: "done", progress: 100 }) }),
    }),
  });
  expect(await screen.findByText("这个视频不能边下边看：点「高清」下载好后观看")).toBeInTheDocument();
  expect(screen.queryByText("这个视频不能边下边看，请改用试听音频")).toBeNull(); // not "use audio" next to a highlighted 高清
  expect(document.querySelector("video.watch-video")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "高清" }));
  const v = await waitForVideo();
  expect(v.getAttribute("src")).toBe("/api/v1/previews/8/stream");
  expect(v.autoplay).toBe(true);
});

// YouTube refused format 18: the 360p is a merged download, playable only
// once complete. The page says 准备中 N% (not an error, not a player waiting
// on a stream that can't come yet), then plays it.
test("a merged 360p shows 准备中 N% instead of the player, then plays once complete", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  let state: Partial<PreviewInfo> = { status: "downloading", merged: true, progress: 45 };
  renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: watchRoutes({ video: () => preview({ title: "T", ...state }) }),
  });
  expect(await screen.findByText("准备中 45%")).toBeInTheDocument();
  expect(document.querySelector("video.watch-video")).toBeNull();
  expect(screen.queryByRole("alert")).toBeNull();
  expect(screen.queryByText("这个视频不能边下边看：点「高清」下载好后观看")).toBeNull();
  state = { status: "downloading", merged: true, progress: 92 };
  await act(() => vi.advanceTimersByTimeAsync(2000));
  expect(await screen.findByText("准备中 92%")).toBeInTheDocument();
  state = { status: "done" };
  await act(() => vi.advanceTimersByTimeAsync(2000));
  const v = await waitForVideo();
  expect(v.getAttribute("src")).toBe("/api/v1/previews/7/stream");
  expect(v.autoplay).toBe(true);
  expect(screen.queryByText(/准备中/)).toBeNull();
});

test("▶ 听 hands over to an audio preview at the current second", async () => {
  const { f, previewAudio } = renderWithApp(pages, { path: `/watch/${ID}`, state: { video: card(ID, "T") }, routes: watchRoutes() });
  const v = await waitForVideo();
  playingAt(v, 61.5);
  fireEvent.click(screen.getByRole("button", { name: "▶ 听" }));
  expect(v.pause).toHaveBeenCalled();
  fireEvent.pause(v); // the element's own event: our pause, so no background offer
  expect(ownsSession("preview")).toBe(true);
  await waitFor(() => expect(previewAudio.src).toContain("/api/v1/previews/9/stream"));
  expect(calls(f, "POST", "/api/v1/previews").at(-1)!.body).toEqual({ video_id: ID, media: "audio", title: "T", channel: "刘翔的投资频道", duration_s: 754 });
  act(() => previewAudio.fire("loadedmetadata"));
  expect(previewAudio.currentTime).toBe(61.5);
  expect(screen.getByRole("dialog", { name: "T" })).toHaveTextContent("保留到频道"); // keepTo channel
  expect(screen.queryByText("Melarka 切到后台，视频停了")).toBeNull();
});

test("iOS stopping the video in the background offers 继续收听 from that second", async () => {
  const { previewAudio } = renderWithApp(pages, { path: `/watch/${ID}`, state: { video: card(ID, "T") }, routes: watchRoutes() });
  const v = await waitForVideo();
  playingAt(v, 42);
  Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
  v.pause();
  fireEvent.pause(v);
  Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
  const strip = await screen.findByRole("status");
  expect(strip).toHaveTextContent("Melarka 切到后台，视频停了");
  fireEvent.click(within(strip).getByRole("button", { name: "继续收听" }));
  await waitFor(() => expect(previewAudio.src).toContain("/api/v1/previews/9/stream"));
  act(() => previewAudio.fire("loadedmetadata"));
  expect(previewAudio.currentTime).toBe(42);
});

test("保留 keeps the HD preview when it is done, else the 360p one", async () => {
  const user = userEvent.setup();
  const { f } = renderWithApp(pages, { path: `/watch/${ID}`, state: { video: card(ID, "T") }, routes: watchRoutes() });
  await waitForVideo();
  const keep = await screen.findByRole("button", { name: "保留" });
  await waitFor(() => expect(keep).toBeEnabled()); // the 360p is done
  await user.click(keep);
  expect(await screen.findByRole("link", { name: "已保留到频道" })).toHaveAttribute("href", `/episodes/${ID}`);
  expect(calls(f, "POST", "/previews/7/keep")[0].body).toEqual({ to: "channel" });
  expect(document.querySelector("video.watch-video")).not.toBeNull(); // still playing
});

test("保留 after 高清 keeps the HD file; it waits while the chosen preview downloads", async () => {
  const user = userEvent.setup();
  const { f } = renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: watchRoutes({
      video: () => preview({ status: "downloading", title: "T", progress: 10 }),
      hdStart: () => ({ status: 201, body: preview({ id: 8, media: "hd", status: "done", progress: 100 }) }),
    }),
  });
  const v = await waitForVideo();
  expect(await screen.findByRole("button", { name: "保留" })).toBeDisabled(); // the 360p still downloads
  fireEvent.click(screen.getByRole("button", { name: "高清" }));
  await waitFor(() => expect(v.getAttribute("src")).toBe("/api/v1/previews/8/stream"));
  const keep = screen.getByRole("button", { name: "保留" });
  expect(keep).toBeEnabled();
  await user.click(keep);
  expect(await screen.findByRole("link", { name: "已保留到频道" })).toBeInTheDocument();
  expect(calls(f, "POST", "/previews/8/keep")).toHaveLength(1);
  expect(calls(f, "POST", "/previews/7/keep")).toHaveLength(0);
});

test("taps on the watch page never start the music queue; music taking over pauses the video", async () => {
  const { audio } = renderWithApp(pages, { path: `/watch/${ID}`, state: { video: card(ID, "T") }, routes: watchRoutes() });
  const v = await waitForVideo();
  playingAt(v, 5);
  expect(ownsSession("video")).toBe(true);
  const hd = screen.getByRole("button", { name: "高清" });
  expect(hd.closest("[data-no-music-prime]")).not.toBeNull();
  fireEvent.click(hd);
  fireEvent.click(screen.getByRole("button", { name: "▶ 听" }).closest("article")!);
  expect(audio.play).not.toHaveBeenCalled();
  expect(audio.src).toBe("");
  act(() => claimSession("music"));
  expect(v.pause).toHaveBeenCalled();
});

test("another video on the same page starts afresh", async () => {
  const user = userEvent.setup();
  const base = watchRoutes();
  const { f } = renderWithApp(pages, { path: `/watch/${ID}`, state: { video: card(ID, "T") }, routes: {
    ...base,
    "POST /api/v1/previews": (init: RequestInit) => {
      const b = JSON.parse(init.body as string);
      if (b.video_id === ID) return base["POST /api/v1/previews"](init);
      return { status: 201, body: preview({ id: 10, video_id: b.video_id, status: "done", title: "相关的" }) };
    },
    "GET /api/v1/previews/10": () => ({ body: preview({ id: 10, video_id: "PncPZ-E1GjE", status: "done", title: "相关的" }) }),
    "GET /api/v1/videos/PncPZ-E1GjE/related": () => ({ body: { videos: [] } }),
  } });
  const old = await waitForVideo();
  await user.click(await screen.findByText("相关的"));
  await waitFor(() => expect(old.hasAttribute("src")).toBe(false)); // the old stream is let go, not left loading
  await waitFor(() => expect(calls(f, "POST", "/api/v1/previews").at(-1)!.body).toEqual({ video_id: "PncPZ-E1GjE", media: "video", title: "相关的", channel: "刘翔的投资频道", duration_s: 754 }));
  expect(await screen.findByRole("heading", { name: "相关的" })).toBeInTheDocument();
  await waitFor(() => expect(document.querySelector("video.watch-video")?.getAttribute("src")).toBe("/api/v1/previews/10/stream"));
  expect(screen.queryByText("简介")).toBeNull(); // nothing of the last video carries over
});

test("高清 refused at the start says why in words (the limit), and can be tapped again", async () => {
  renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: watchRoutes({ hdStart: () => ({ status: 429, body: { error: "limit", code: "preview_limit" } }) }),
  });
  await waitForVideo();
  fireEvent.click(screen.getByRole("button", { name: "高清" }));
  expect(await screen.findByText("试听太多了；保留一些，或等它们过期")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "高清" })).toBeEnabled();
});

test("a 高清 download the server dropped (503 preview_retry while polling) stops polling, says when to retry, and 高清 works again", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  let gone = false;
  const { f } = renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: {
      ...watchRoutes(),
      "GET /api/v1/previews/8": () => (gone
        ? { status: 503, body: { error: "retry", code: "preview_retry" }, headers: { "Retry-After": "30" } }
        : { body: preview({ id: 8, media: "hd", status: "downloading", progress: 37 }) }),
    },
  });
  await waitForVideo();
  fireEvent.click(screen.getByRole("button", { name: "高清" }));
  expect(await screen.findByRole("button", { name: "高清 37%" })).toBeDisabled();
  gone = true;
  await act(() => vi.advanceTimersByTimeAsync(2000));
  expect(await screen.findByText("YouTube 暂时拒绝了请求，请 30 秒后再试")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "高清" })).toBeEnabled();
  const polls = calls(f, "GET", "/previews/8").length;
  await act(() => vi.advanceTimersByTimeAsync(10_000));
  expect(calls(f, "GET", "/previews/8")).toHaveLength(polls);
});

test("a 高清 status read that fails without a reason (a network blip) keeps polling", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  let blip = false;
  renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: {
      ...watchRoutes(),
      "GET /api/v1/previews/8": () => (blip ? { status: 502 } : { body: preview({ id: 8, media: "hd", status: "downloading", progress: 37 }) }),
    },
  });
  await waitForVideo();
  fireEvent.click(screen.getByRole("button", { name: "高清" }));
  expect(await screen.findByRole("button", { name: "高清 37%" })).toBeInTheDocument();
  blip = true;
  await act(() => vi.advanceTimersByTimeAsync(2000));
  expect(screen.getByRole("button", { name: "高清 37%" })).toBeDisabled();
  expect(screen.queryByText("高清下载失败")).toBeNull();
});

test("the 720p stream breaking after the switch goes back to the 360p at the same second", async () => {
  renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: watchRoutes({ hdStart: () => ({ status: 201, body: preview({ id: 8, media: "hd", status: "done", progress: 100 }) }) }),
  });
  const v = await waitForVideo();
  playingAt(v, 20);
  fireEvent.click(screen.getByRole("button", { name: "高清" }));
  await waitFor(() => expect(v.getAttribute("src")).toBe("/api/v1/previews/8/stream"));
  fireEvent.loadedMetadata(v);
  v.currentTime = 95;
  fireEvent.error(v);
  await waitFor(() => expect(v.getAttribute("src")).toBe("/api/v1/previews/7/stream"));
  fireEvent.loadedMetadata(v);
  expect(v.currentTime).toBe(95);
  expect(screen.getByRole("button", { name: "高清" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "高清" })).toHaveAttribute("aria-pressed", "false");
  expect(screen.getByText("高清下载失败")).toBeInTheDocument();
});

test("a 360p stream that breaks says so; 重试 reloads it from where it broke", async () => {
  renderWithApp(pages, { path: `/watch/${ID}`, state: { video: card(ID, "T") }, routes: watchRoutes() });
  const v = await waitForVideo();
  playingAt(v, 33);
  const load = vi.spyOn(v, "load");
  fireEvent.error(v);
  const alert = await screen.findByText("视频中断了");
  expect(alert).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "重试" }));
  expect(load).toHaveBeenCalled();
  expect(screen.queryByText("视频中断了")).toBeNull();
  v.currentTime = 0; // a reload starts from 0
  fireEvent.loadedMetadata(v);
  expect(v.currentTime).toBe(33);
  expect(v.play).toHaveBeenCalled();
});

test("a 360p that couldn't start (YouTube pushing back) offers 重试", async () => {
  let refuse = true;
  renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: watchRoutes({
      videoStart: () => (refuse ? { status: 503, body: { error: "retry", code: "preview_retry" }, headers: { "Retry-After": "30" } } : { status: 201, body: preview({ status: "downloading" }) }),
    }),
  });
  expect(await screen.findByText("YouTube 暂时拒绝了请求，请 30 秒后再试")).toBeInTheDocument();
  refuse = false;
  fireEvent.click(screen.getByRole("button", { name: "重试" }));
  const v = await waitForVideo();
  expect(v.getAttribute("src")).toBe("/api/v1/previews/7/stream");
  expect(screen.queryByText("YouTube 暂时拒绝了请求，请 30 秒后再试")).toBeNull();
});

test("关注频道 follows the channel of the video shown, also after moving to a related video", async () => {
  const user = userEvent.setup();
  const B = "UCRABK12_6Ie2X549K9cXS0g";
  renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: {
      ...watchRoutes(),
      "POST /api/v1/channels/follow": () => ({ body: {} }),
      [`GET /api/v1/videos/${ID}/related`]: () => ({ body: { videos: [card("PncPZ-E1GjE", "相关的", { channel: "别的频道", channel_id: B })] } }),
      "GET /api/v1/videos/PncPZ-E1GjE/related": () => ({ body: { videos: [] } }),
    },
  });
  await waitForVideo();
  await user.click(await screen.findByRole("button", { name: "关注频道" }));
  expect(await screen.findByRole("button", { name: "已关注" })).toBeInTheDocument();
  await user.click(await screen.findByText("相关的"));
  expect(await screen.findByRole("button", { name: "关注频道" })).toHaveAttribute("aria-pressed", "false");
});

test("a description starting with a blank line still shows; one long paragraph folds too", async () => {
  const long = "很长的一段".repeat(60);
  renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: watchRoutes({ video: () => preview({ status: "done", title: "T", description: `\n\n${long}` }) }),
  });
  const p = await screen.findByText(long);
  expect(p).toHaveClass("clamp-3");
  fireEvent.click(screen.getByRole("button", { name: "展开" }));
  expect(screen.getByText(long)).not.toHaveClass("clamp-3");
});

test("a shared link whose 360p can't stream takes its title from the 高清 preview and records the watch", async () => {
  const { f } = renderWithApp(pages, {
    path: `/watch/${ID}`,
    routes: watchRoutes({
      videoStart: () => ({ status: 201, body: preview({ status: "failed", error: "video_preview_unavailable" }) }),
      hdStart: () => ({ status: 201, body: preview({ id: 8, media: "hd", status: "done", title: "HD标题", channel: "频道", channel_id: LX, duration_s: 100 }) }),
    }),
  });
  fireEvent.click(await screen.findByRole("button", { name: "高清" }));
  expect(await screen.findByRole("heading", { name: "HD标题" })).toBeInTheDocument();
  await waitFor(() => expect(calls(f, "POST", "/me/video-history/watches")).toHaveLength(1));
  expect(calls(f, "POST", "/me/video-history/watches")[0].body).toEqual({ video_id: ID, title: "HD标题", channel: "频道", channel_id: LX, duration_s: 100 });
});

test("a failed search is asked again when 搜索 is tapped with the same words", async () => {
  const user = userEvent.setup();
  let fail = true;
  const { f } = renderWithApp(pages, {
    path: "/video",
    routes: {
      "GET /api/v1/me/video-history": () => ({ body: emptyHistory }),
      "GET /api/v1/me/video-recommendations": () => ({ body: emptyRecs }),
      "GET /api/v1/videos/search": () => (fail ? { status: 504, body: { error: "timeout" } } : { body: { videos: [card(ID, "终于")] } }),
    },
  });
  await user.type(screen.getByLabelText("搜索视频"), "刘翔{Enter}");
  await waitFor(() => expect(calls(f, "GET", "/videos/search")).toHaveLength(1));
  await waitFor(() => expect(document.querySelector(".video-page .error")).not.toBeNull());
  fail = false;
  await user.click(screen.getByRole("button", { name: "搜索" }));
  expect(await screen.findByText("终于")).toBeInTheDocument();
  expect(calls(f, "GET", "/videos/search")).toHaveLength(2);
});

test("为你推荐 failing once (a network blip) is asked again; 清除记录 clears the error", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  let fail = true;
  const { f } = renderWithApp(pages, {
    path: "/video",
    routes: {
      "GET /api/v1/me/video-history": () => ({ body: emptyHistory }),
      "GET /api/v1/me/video-recommendations": () => (fail ? { status: 502 } : { body: emptyRecs }),
    },
  });
  await waitFor(() => expect(calls(f, "GET", "/me/video-recommendations")).toHaveLength(1));
  fail = false;
  await act(() => vi.advanceTimersByTimeAsync(10_000));
  expect(await screen.findByText("看几个视频或搜索一下，这里就会有推荐")).toBeInTheDocument();
  expect(calls(f, "GET", "/me/video-recommendations")).toHaveLength(2);
});

test("保留 waits while 高清 downloads, so the episode never locks in the 360p", async () => {
  renderWithApp(pages, { path: `/watch/${ID}`, state: { video: card(ID, "T") }, routes: watchRoutes() });
  await waitForVideo();
  const keep = await screen.findByRole("button", { name: "保留" });
  await waitFor(() => expect(keep).toBeEnabled()); // the 360p is done
  fireEvent.click(screen.getByRole("button", { name: "高清" }));
  await screen.findByRole("button", { name: "高清 40%" });
  expect(screen.getByRole("button", { name: "保留" })).toBeDisabled();
});

test("kept at 360p, then 高清 done: 保留 is offered again and keeps the 720p", async () => {
  const user = userEvent.setup();
  const { f } = renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: watchRoutes({ hdStart: () => ({ status: 201, body: preview({ id: 8, media: "hd", status: "done", progress: 100 }) }) }),
  });
  await waitForVideo();
  const keep = await screen.findByRole("button", { name: "保留" });
  await waitFor(() => expect(keep).toBeEnabled());
  await user.click(keep);
  expect(await screen.findByRole("link", { name: "已保留到频道" })).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "高清" }));
  await user.click(await screen.findByRole("button", { name: "保留" }));
  expect(await screen.findByRole("link", { name: "已保留到频道" })).toBeInTheDocument();
  expect(calls(f, "POST", "/previews/7/keep")).toHaveLength(1);
  expect(calls(f, "POST", "/previews/8/keep")).toHaveLength(1);
});

test("only 高清 plays (the 360p can't stream): its description is shown", async () => {
  renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: watchRoutes({
      videoStart: () => ({ status: 201, body: preview({ status: "failed", error: "video_preview_unavailable" }) }),
      hdStart: () => ({ status: 201, body: preview({ id: 8, media: "hd", status: "done", title: "T", description: "高清的简介" }) }),
    }),
  });
  fireEvent.click(await screen.findByRole("button", { name: "高清" }));
  expect(await screen.findByText("高清的简介")).toBeInTheDocument();
});

test("a second 高清 waiting for the slot says 排队中; one that waited too long says so and can be tapped again", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  let timedOut = false;
  renderWithApp(pages, {
    path: `/watch/${ID}`,
    state: { video: card(ID, "T") },
    routes: watchRoutes({
      hd: () => (timedOut
        ? preview({ id: 8, media: "hd", status: "failed", error: "preview_queue_timeout" })
        : preview({ id: 8, media: "hd", status: "downloading", queued: true })),
    }),
  });
  await waitForVideo();
  fireEvent.click(screen.getByRole("button", { name: "高清" }));
  expect(await screen.findByRole("button", { name: "高清 排队中" })).toBeDisabled();
  timedOut = true;
  await act(() => vi.advanceTimersByTimeAsync(2000));
  expect(await screen.findByText("高清排队太久了，请稍后再试")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "高清" })).toBeEnabled();
});

test("no dangerouslySetInnerHTML in 视频 sources", () => {
  for (const f of ["pages/video/VideoPage.tsx", "pages/video/WatchPage.tsx", "components/VideoCard.tsx"]) {
    expect(readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), "..", f), "utf8")).not.toMatch(/dangerouslySetInnerHTML|innerHTML/);
  }
});
