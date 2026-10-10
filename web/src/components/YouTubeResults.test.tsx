import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { DownloadJob, YTPlaylist, YTVideo } from "../api/types";
import { renderWithApp } from "../test/render";
import { YouTubeResults } from "./YouTubeResults";

const vid = (id: string): YTVideo => ({
  id,
  title: `视频${id}`,
  channel: "频道A",
  url: `https://www.youtube.com/watch?v=${id}`,
  thumbnail: `https://i.ytimg.com/vi/${id}/default.jpg`,
  duration_s: 125,
});
const range = (from: number, to: number) => Array.from({ length: to - from }, (_, i) => vid(`v${from + i}`));
const pl: YTPlaylist = { id: "PLfakelist0001", title: "假歌单", channel: "假歌手频道", url: "https://www.youtube.com/playlist?list=PLfakelist0001", thumbnail: "", count: 0 };
const job: DownloadJob = {
  id: 7, user_id: 1, username: "u", url: "https://www.youtube.com/watch?v=t2", video_id: "t2", title: "视频t2", channel: "频道A",
  duration_s: 125, thumbnail: "", status: "queued", progress: 0, error: "", track_id: null, track_available: true, created_at: 1, updated_at: 1,
};

const rowTitles = () => within(document.querySelector("ul.yt-results:not(.yt-tracks)") as HTMLElement).getAllByText(/^视频v/).map((e) => e.textContent);

test("显示更多 asks for a larger page and appends only the new videos", async () => {
  const urls: string[] = [];
  renderWithApp(<YouTubeResults query="歌" auto />, {
    routes: {
      "GET /api/v1/youtube/search": (_i, url) => {
        urls.push(url);
        const n = Number(new URL(url, "http://x").searchParams.get("n") ?? 10);
        // Page 2 repeats page 1 (ytsearch has no offset) and shuffles one in.
        return { body: n === 10 ? { videos: range(0, 10), playlists: [pl], more: true } : { videos: [vid("v3"), ...range(10, 18)], playlists: [] } };
      },
    },
  });
  expect(await screen.findByText("视频v9")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "显示更多" }));
  expect(await screen.findByText("视频v17")).toBeInTheDocument();
  expect(urls[1]).toBe("/api/v1/youtube/search?q=%E6%AD%8C&n=20");
  expect(rowTitles()).toEqual(range(0, 18).map((v) => v.title));
  // more was false: no further page; the first page's playlists stay.
  expect(screen.queryByRole("button", { name: "显示更多" })).toBeNull();
  expect(screen.getByText("假歌单")).toBeInTheDocument();
});

test("显示更多 is not offered for a short result, and a failed page can be retried", async () => {
  let fail = true;
  renderWithApp(<YouTubeResults query="歌" auto />, {
    routes: {
      "GET /api/v1/youtube/search": (_i, url) => {
        if (!url.includes("n=")) return { body: { videos: range(0, 10), playlists: [], more: true } };
        if (fail) return { status: 502, body: { error: "YouTube search failed: boom", code: "youtube_search_failed" } };
        return { body: { videos: range(0, 12), playlists: [], more: true } };
      },
    },
  });
  await userEvent.click(await screen.findByRole("button", { name: "显示更多" }));
  expect(await screen.findByText(/boom/)).toBeInTheDocument();
  fail = false;
  await userEvent.click(screen.getByRole("button", { name: "显示更多" }));
  expect(await screen.findByText("视频v11")).toBeInTheDocument();
  expect(screen.queryByText(/boom/)).toBeNull();
  expect(screen.getByRole("button", { name: "显示更多" })).toBeInTheDocument();
});

test("a playlist expands in place: its tracks with 试听 and 下载, more on demand, no refetch on reopen", async () => {
  const entryUrls: string[] = [];
  const posted: unknown[] = [];
  renderWithApp(<YouTubeResults query="歌" auto />, {
    routes: {
      "GET /api/v1/youtube/search": () => ({ body: { videos: [vid("v1")], playlists: [pl] } }),
      "GET /api/v1/youtube/playlist/entries": (_i, url) => {
        entryUrls.push(url);
        return url.includes("n=100")
          ? { body: { videos: [vid("t1"), vid("t2"), vid("t3")], count: 3, more: false } }
          : { body: { videos: [vid("t1"), vid("t2")], count: 3, more: true } };
      },
      "POST /api/v1/downloads": (init) => {
        posted.push(JSON.parse(init.body as string));
        return { status: 201, body: { jobs: [job] } };
      },
    },
  });
  const toggle = await screen.findByRole("button", { name: "「假歌单」的歌曲" });
  expect(toggle).toHaveAttribute("aria-expanded", "false");
  // The list-level action is still there.
  expect(screen.getByRole("button", { name: "下载全部" })).toBeInTheDocument();
  await userEvent.click(toggle);
  expect(toggle).toHaveAttribute("aria-expanded", "true");
  const panel = await screen.findByRole("region", { name: "「假歌单」的歌曲" });
  expect(toggle).toHaveAttribute("aria-controls", panel.id);
  expect(await within(panel).findByText("视频t2")).toBeInTheDocument();
  expect(entryUrls[0]).toBe("/api/v1/youtube/playlist/entries?list=PLfakelist0001");
  const row = within(panel).getByText("视频t2").closest("li") as HTMLElement;
  expect(within(row).getByText("频道A · 2:05")).toBeInTheDocument();
  expect(within(row).getByRole("button", { name: "试听" })).toBeInTheDocument();
  await userEvent.click(within(row).getByRole("button", { name: "下载" }));
  expect(posted).toEqual([{ video: vid("t2") }]);

  await userEvent.click(within(panel).getByRole("button", { name: "显示更多" }));
  expect(await within(panel).findByText("视频t3")).toBeInTheDocument();
  expect(entryUrls[1]).toBe("/api/v1/youtube/playlist/entries?list=PLfakelist0001&n=100");
  expect(within(panel).getAllByText(/^视频t/)).toHaveLength(3);
  expect(within(panel).queryByRole("button", { name: "显示更多" })).toBeNull();

  await userEvent.click(toggle);
  expect(toggle).toHaveAttribute("aria-expanded", "false");
  expect(screen.queryByText("视频t1")).toBeNull();
  await userEvent.click(toggle);
  expect(await screen.findByText("视频t3")).toBeInTheDocument();
  expect(entryUrls).toHaveLength(2);
});

test("a playlist that fails to open offers 重试; an empty one says so", async () => {
  let calls = 0;
  renderWithApp(<YouTubeResults query="歌" auto />, {
    routes: {
      "GET /api/v1/youtube/search": () => ({ body: { videos: [], playlists: [pl] } }),
      "GET /api/v1/youtube/playlist/entries": () =>
        ++calls === 1 ? { status: 502, body: { error: "YouTube search failed: gone", code: "youtube_search_failed" } } : { body: { videos: [], count: 0, more: false } },
    },
  });
  await userEvent.click(await screen.findByRole("button", { name: "「假歌单」的歌曲" }));
  const panel = await screen.findByRole("region", { name: "「假歌单」的歌曲" });
  expect(await within(panel).findByText(/gone/)).toBeInTheDocument();
  await userEvent.click(within(panel).getByRole("button", { name: "重试" }));
  expect(await within(panel).findByText("这个歌单里没有可播放的视频")).toBeInTheDocument();
});
