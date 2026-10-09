import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { Route, Routes } from "react-router";
import { afterEach, expect, test } from "vitest";
import type { ChannelPageData, Episode } from "../api/types";
import { EpisodeMini } from "../channels/EpisodeMini";
import { ownsSession, resetSessionOwner } from "../player/sessionOwner";
import { renderWithApp } from "../test/render";
import ChannelPage from "./channels/ChannelPage";
import ChannelsPage from "./channels/ChannelsPage";

// The episode Now Playing, its queue sheet, and the 频道 views that start a queue.

const LX = "UC0e5c4U67Vm6sAVK0vxN3Uw";
const OTHER = "UCRABK12_6Ie2X549K9cXS0g";
// Higher n = older, like a real 最新 list.
const ep = (n: number, extra: Partial<Episode> = {}): Episode => ({
  video_id: `episode000${n}`, channel_id: LX, channel_title: "刘翔的投资频道", title: `第${n}集`, published_at: 1_790_000_000 - n * 3600,
  duration_s: 1800, kind: "video", thumbnail: `/api/v1/episodes/episode000${n}/thumbnail`,
  audio: { status: "done", progress: 100, bytes: 1, error: "" }, video: null, position_s: 0, played: false, kept: false, ...extra,
});
const progress = Object.fromEntries([1, 2, 3, 4, 5].map((n) => [`PUT /api/v1/episodes/episode000${n}/progress`, () => ({ status: 204 })]));
const queueTitles = () => within(screen.getByRole("list", { name: "节目队列" })).getAllByRole("button").map((b) => b.querySelector(".ellipsis")?.textContent);

afterEach(() => resetSessionOwner());

function latest(items: Episode[]) {
  return { "GET /api/v1/episodes/latest": () => ({ body: { items, unplayed: items.length } }) };
}

async function openNowPlaying() {
  fireEvent.click(screen.getByRole("button", { name: /正在播放的节目/ }));
  return screen.findByRole("dialog", { name: "正在播放的节目" });
}

test("the episode mini player opens Now Playing: artwork, title, channel link, ⏮ ⏭, ±, speed; ⌄ closes it", async () => {
  const { episodeAudio } = renderWithApp(<><ChannelsPage /><EpisodeMini /></>, { path: "/channels", routes: { ...latest([ep(1), ep(2), ep(3)]), ...progress } });
  fireEvent.click(within((await screen.findByText("第2集")).closest("li")!).getByRole("button", { name: "听" }));
  const now = await openNowPlaying();
  expect(within(now).getByRole("heading", { name: "第2集" })).toBeInTheDocument();
  expect(within(now).getByRole("link", { name: "刘翔的投资频道" })).toHaveAttribute("href", `/channels/${LX}`);
  expect(now.querySelector("img")?.getAttribute("src")).toBe("/api/v1/episodes/episode0002/thumbnail");
  expect(now.closest("[data-no-music-prime]")).not.toBeNull();
  // ⏭ / ⏮ follow the queue (newest first: 1, 2, 3).
  fireEvent.click(within(now).getByRole("button", { name: "下一集" }));
  expect(episodeAudio.src).toContain("episode0003");
  episodeAudio.currentTime = 10;
  fireEvent.click(within(now).getByRole("button", { name: "上一集" }));
  expect(episodeAudio.src).toContain("episode0003"); // more than 3 s in: back to the start
  expect(episodeAudio.currentTime).toBe(0);
  fireEvent.click(within(now).getByRole("button", { name: "上一集" }));
  expect(episodeAudio.src).toContain("episode0002");
  episodeAudio.currentTime = 100;
  fireEvent.click(within(now).getByRole("button", { name: "前进 30 秒" }));
  expect(episodeAudio.currentTime).toBe(130);
  fireEvent.click(within(now).getByRole("button", { name: "后退 15 秒" }));
  expect(episodeAudio.currentTime).toBe(115);
  fireEvent.click(within(now).getByRole("button", { name: "1.5×" }));
  expect(episodeAudio.playbackRate).toBe(1.5);
  fireEvent.click(within(now).getByRole("button", { name: "暂停" }));
  expect(episodeAudio.paused).toBe(true);
  fireEvent.click(within(now).getByRole("button", { name: "收起" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.getByRole("tab", { name: "最新" })).toBeInTheDocument();
});

test("the progress slider previews while dragging and seeks once, on release; it shows elapsed and remaining", async () => {
  const { episodeAudio } = renderWithApp(<><ChannelsPage /><EpisodeMini /></>, { path: "/channels", routes: { ...latest([ep(1)]), ...progress } });
  fireEvent.click(within((await screen.findByText("第1集")).closest("li")!).getByRole("button", { name: "听" }));
  episodeAudio.duration = 1800;
  act(() => episodeAudio.fire("loadedmetadata"));
  episodeAudio.currentTime = 60;
  act(() => episodeAudio.fire("timeupdate"));
  const now = await openNowPlaying();
  expect(within(now).getByText("1:00")).toBeInTheDocument();
  expect(within(now).getByText("-29:00")).toBeInTheDocument();
  const slider = within(now).getByRole("slider", { name: "进度" }) as HTMLInputElement;
  fireEvent.input(slider, { target: { value: "900" } });
  expect(episodeAudio.currentTime).toBe(60); // only a preview
  expect(within(now).getByText("15:00")).toBeInTheDocument();
  fireEvent.change(slider, { target: { value: "900" } });
  expect(episodeAudio.currentTime).toBe(900);
});

test("the queue sheet: current highlighted, tap jumps, order 新→旧/旧→新/随机, 包含已播放", async () => {
  const { episodeAudio } = renderWithApp(<><ChannelsPage /><EpisodeMini /></>, {
    path: "/channels", routes: { ...latest([ep(1), ep(2, { played: true }), ep(3), ep(4)]), ...progress },
  });
  fireEvent.click(within((await screen.findByText("第3集")).closest("li")!).getByRole("button", { name: "听" }));
  const now = await openNowPlaying();
  fireEvent.click(within(now).getByRole("button", { name: "节目队列" }));
  expect(queueTitles()).toEqual(["第1集", "第3集", "第4集"]); // 第2集 played: left out
  const current = within(screen.getByRole("list", { name: "节目队列" })).getByRole("button", { current: true });
  expect(current).toHaveTextContent("第3集");
  fireEvent.click(within(now).getByRole("button", { name: "旧→新" }));
  expect(queueTitles()).toEqual(["第4集", "第3集", "第1集"]);
  fireEvent.click(within(now).getByRole("checkbox", { name: "包含已播放" }));
  expect(queueTitles()).toEqual(["第4集", "第3集", "第2集", "第1集"]);
  fireEvent.click(within(now).getByRole("button", { name: "随机" }));
  expect(queueTitles()[0]).toBe("第3集");
  expect(episodeAudio.src).toContain("episode0003");
  fireEvent.click(within(now).getByRole("button", { name: "新→旧" }));
  fireEvent.click(screen.getByText("第1集", { selector: "#episode-queue .ellipsis" }));
  expect(episodeAudio.src).toContain("episode0001");
});

test("最新: the sort and 只看未播放 are asked of the server, remembered on this device, and ▶ queues in that order", async () => {
  const urls: string[] = [];
  const routes = {
    "GET /api/v1/episodes/latest": (_: RequestInit, url: string) => {
      urls.push(url);
      const asc = url.includes("order=asc");
      const items = [ep(1), ep(2), ep(3)];
      return { body: { items: asc ? items.reverse() : items, unplayed: 3 } };
    },
    ...progress,
  };
  renderWithApp(<><ChannelsPage /><EpisodeMini /></>, { path: "/channels", routes });
  await screen.findByText("第1集");
  expect(urls.at(-1)).not.toContain("order=asc");
  fireEvent.click(screen.getByRole("button", { name: "最早在前" }));
  await waitFor(() => expect(urls.at(-1)).toContain("order=asc"));
  fireEvent.click(screen.getByRole("checkbox", { name: "只看未播放" }));
  await waitFor(() => expect(urls.at(-1)).toContain("unplayed=1"));
  expect(localStorage.getItem("lark.latestSort")).toBe("oldest");
  expect(localStorage.getItem("lark.latestUnplayed")).toBe("1");
  await waitFor(() => expect(screen.getAllByRole("button", { name: "听" })).toHaveLength(3));
  fireEvent.click(within(screen.getByText("第2集").closest("li")!).getByRole("button", { name: "听" }));
  const now = await openNowPlaying();
  fireEvent.click(within(now).getByRole("button", { name: "节目队列" }));
  expect(queueTitles()).toEqual(["第3集", "第2集", "第1集"]);
});

test("最新 starts with the remembered sort and filter", async () => {
  localStorage.setItem("lark.latestSort", "oldest");
  localStorage.setItem("lark.latestUnplayed", "1");
  const urls: string[] = [];
  renderWithApp(<ChannelsPage />, {
    path: "/channels",
    routes: { "GET /api/v1/episodes/latest": (_: RequestInit, url: string) => (urls.push(url), { body: { items: [ep(1)], unplayed: 1 } }) },
  });
  await screen.findByText("第1集");
  expect(urls[0]).toContain("order=asc");
  expect(urls[0]).toContain("unplayed=1");
  expect(screen.getByRole("button", { name: "最早在前" })).toHaveAttribute("aria-pressed", "true");
  expect(screen.getByRole("checkbox", { name: "只看未播放" })).toBeChecked();
});

const group = (id: string, title: string, unplayed: number, episodes: Episode[]) => ({
  channel: { id, title, handle: "", avatar: "", description: "", polled_at: 1, last_error: "" }, unplayed, latest_published_at: 1, episodes,
});

test("按频道: a section per channel with its unplayed count; ▶ 全部播放 skips played, 🔀 随机 shuffles, more episodes expand", async () => {
  const many = [ep(1), ep(2, { played: true }), ep(3), ep(4), ep(5)];
  const { episodeAudio } = renderWithApp(<><ChannelsPage /><EpisodeMini /></>, {
    path: "/channels?tab=channels",
    routes: {
      "GET /api/v1/episodes/by-channel": (_: RequestInit, url: string) => ({
        status: url.includes("per=50") ? 200 : 400, // play-all queues up to 50 a channel
        body: { groups: [group(LX, "刘翔的投资频道", 4, many), group(OTHER, "刘翔", 0, [])] },
      }),
      ...progress,
    },
  });
  const section = (await screen.findByRole("heading", { name: "刘翔的投资频道" })).closest("section")!;
  expect(within(section).getByText("4 个新节目")).toBeInTheDocument();
  expect(screen.getByRole("heading", { name: "刘翔" })).toBeInTheDocument();
  // The first three show; the rest behind 更多.
  expect(within(section).queryByText("第4集")).toBeNull();
  fireEvent.click(within(section).getByRole("button", { name: "更多" }));
  expect(within(section).getByText("第5集")).toBeInTheDocument();
  fireEvent.click(within(section).getByRole("button", { name: "全部播放" }));
  expect(episodeAudio.src).toContain("episode0001");
  expect(ownsSession("episode")).toBe(true);
  let now = await openNowPlaying();
  fireEvent.click(within(now).getByRole("button", { name: "节目队列" }));
  expect(queueTitles()).toEqual(["第1集", "第3集", "第4集", "第5集"]);
  fireEvent.click(within(now).getByRole("button", { name: "收起" }));
  fireEvent.click(within(section).getByRole("button", { name: "随机播放" }));
  now = await openNowPlaying();
  fireEvent.click(within(now).getByRole("button", { name: "节目队列" }));
  expect([...queueTitles()].sort()).toEqual(["第1集", "第3集", "第4集", "第5集"]);
  expect(within(now).getByRole("button", { name: "随机" })).toHaveAttribute("aria-pressed", "true");
});

test("a channel page plays all (newest first, unplayed only) or shuffled", async () => {
  const data: ChannelPageData = {
    channel: { id: LX, title: "刘翔的投资频道", handle: "", avatar: "", description: "", polled_at: 1, last_error: "" },
    following: { media: "audio", keep_days: null, paused: false, include_shorts: false, include_live: false }, followers: 1,
    episodes: [ep(1, { played: true }), ep(2), ep(3, { audio: { status: "queued", progress: 0, bytes: 0, error: "" } }), ep(4)],
  };
  const { episodeAudio } = renderWithApp(
    <><Routes><Route path="/channels/:id" element={<ChannelPage />} /></Routes><EpisodeMini /></>,
    { path: `/channels/${LX}`, routes: { [`GET /api/v1/channels/${LX}`]: () => ({ body: data }), ...progress } },
  );
  fireEvent.click(await screen.findByRole("button", { name: "全部播放" }));
  expect(episodeAudio.src).toContain("episode0002");
  const now = await openNowPlaying();
  fireEvent.click(within(now).getByRole("button", { name: "节目队列" }));
  expect(queueTitles()).toEqual(["第2集", "第4集"]); // played and undownloaded left out
  fireEvent.click(within(now).getByRole("button", { name: "收起" }));
  fireEvent.click(screen.getByRole("button", { name: "随机播放" }));
  expect(screen.getByRole("button", { name: "随机播放" }).closest("[data-no-music-prime]")).not.toBeNull();
});
