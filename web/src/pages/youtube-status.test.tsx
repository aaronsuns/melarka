import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useDownloads } from "../downloads/DownloadsProvider";
import { renderWithApp } from "../test/render";
import SearchPage from "./SearchPage";
import DownloadsPage from "./DownloadsPage";
import { LinkCard } from "../components/LinkCard";
import type { DownloadJob, Track, YTVideo } from "../api/types";

const video: YTVideo = {
  id: "v1",
  title: "视频v1",
  channel: "频道A",
  url: "https://www.youtube.com/watch?v=v1",
  thumbnail: "https://i.ytimg.com/vi/v1/default.jpg",
  duration_s: 125,
};

const job = (extra: Partial<DownloadJob> = {}): DownloadJob => ({
  id: 5,
  user_id: 1,
  username: "u",
  url: video.url,
  video_id: "v1",
  title: "歌曲标题",
  channel: "频道A",
  duration_s: 125,
  thumbnail: "",
  status: "queued",
  progress: 0,
  error: "",
  track_id: null,
  track_available: true,
  created_at: 1,
  updated_at: 1,
  ...extra,
});

const track = (id: number): Track => ({
  id, title: "歌曲标题", artist: "a", artist_id: null, album: "b", album_id: null, year: null,
  duration_ms: 1000, codec: "mp3", bitrate: 320, lossless: false, status: "kept", broken: false,
  broken_reason: "", favorite: false, disliked: false, library_id: 1, path: "", added_at: 1,
  track_no: null, disc_no: null,
});

// Sequence of GET /downloads responses; the last one repeats.
function sequence(...lists: DownloadJob[][]) {
  let i = 0;
  return vi.fn(() => ({ body: lists[Math.min(i++, lists.length - 1)] }));
}

type H = () => { status?: number; body?: unknown };
function ytRoutes(list: H, extra: Record<string, H> = {}) {
  return {
    "GET /api/v1/search": () => ({ body: { tracks: [], albums: [], artists: [] } }),
    "GET /api/v1/youtube/search": () => ({ body: { videos: [video], playlists: [] } }),
    "POST /api/v1/downloads": () => ({ status: 201, body: { jobs: [job()] } }),
    "GET /api/v1/downloads": list,
    "GET /api/v1/tracks/7": () => ({ body: track(7) }),
    ...extra,
  };
}

async function startDownload() {
  await userEvent.type(screen.getByRole("searchbox"), "abcd");
  await userEvent.click(await screen.findByRole("button", { name: "下载" }, { timeout: 3000 }));
}

// Tracks a job without any result row on screen (e.g. the user has since
// navigated elsewhere), so only the toast can announce it.
function Starter() {
  const dl = useDownloads();
  return <button onClick={() => dl.track([job({ status: "downloading", progress: 10 })])}>开始</button>;
}

beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
afterEach(() => vi.useRealTimers());

const tick = (ms = 2000) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });

test("row goes 排队中 → 下载中 → ▶ 播放, which plays the downloaded track", async () => {
  const list = sequence([job()], [job({ status: "downloading", progress: 40 })], [job({ status: "done", progress: 100, track_id: 7 })]);
  const { audio } = renderWithApp(<SearchPage />, { routes: ytRoutes(list) });
  await startDownload();
  const row = (await screen.findByText("视频v1")).closest("li")!;
  expect(await within(row).findByText("排队中")).toBeInTheDocument();
  await tick();
  expect(await within(row).findByText("下载中 40%")).toBeInTheDocument();
  await tick();
  const play = await within(row).findByRole("button", { name: /播放/ });
  await userEvent.click(play);
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/7/stream"));
});

test("polling stops once nothing is queued or downloading", async () => {
  const list = sequence([job({ status: "done", track_id: 7 })]);
  renderWithApp(<SearchPage />, { routes: ytRoutes(list) });
  await startDownload();
  await tick();
  const row = (await screen.findByText("视频v1")).closest("li")!;
  await within(row).findByRole("button", { name: /播放/ });
  const calls = list.mock.calls.length;
  await tick(10_000);
  expect(list.mock.calls.length).toBe(calls);
});

test("a failed job shows the translated error and 重试 re-queues it", async () => {
  const retry = vi.fn(() => ({ status: 204 }));
  const list = sequence([job({ status: "failed", error: "lark:timeout" })], [job()]);
  renderWithApp(<SearchPage />, { routes: ytRoutes(list, { "POST /api/v1/downloads/5/retry": retry }) });
  await startDownload();
  await tick();
  const row = (await screen.findByText("视频v1")).closest("li")!;
  expect(await within(row).findByText("下载超时（30 分钟）")).toBeInTheDocument();
  await userEvent.click(within(row).getByRole("button", { name: "重试" }));
  await vi.waitFor(() => expect(retry).toHaveBeenCalled());
  expect(await within(row).findByText("排队中")).toBeInTheDocument();
});

test("yt-dlp's raw error never shows in the row: a short line under the title, the raw text as its tooltip", async () => {
  const raw = "ERROR: [youtube] v1: unable to download video data: HTTP Error 403: Forbidden";
  renderWithApp(<SearchPage />, { routes: ytRoutes(sequence([job({ status: "failed", error: raw })])) });
  await startDownload();
  await tick();
  const row = (await screen.findByText("视频v1")).closest("li")!;
  const line = await within(row).findByText("下载失败（YouTube 拒绝，稍后重试）");
  expect(line).toHaveAttribute("title", raw);
  expect(line.closest(".yt-text")).not.toBeNull();
  expect(within(row).queryByText(/HTTP Error/)).toBeNull();
  expect(within(row).getByRole("button", { name: "重试" }).closest(".row-actions")).not.toBeNull();
});

test("while active, the status links to /downloads?job=<id>", async () => {
  renderWithApp(<SearchPage />, { routes: ytRoutes(sequence([job()])) });
  await startDownload();
  const row = (await screen.findByText("视频v1")).closest("li")!;
  const link = await within(row).findByRole("link", { name: "排队中" });
  expect(link).toHaveAttribute("href", "/downloads?job=5");
});

test("DownloadsPage highlights and scrolls to ?job=", async () => {
  const scroll = vi.fn();
  Element.prototype.scrollIntoView = scroll;
  renderWithApp(<DownloadsPage />, {
    path: "/downloads?job=5",
    routes: { "GET /api/v1/downloads": () => ({ body: [job({ id: 4, title: "别的歌" }), job({ id: 5, title: "目标歌" })] }) },
  });
  const target = (await screen.findByText("目标歌")).closest("li")!;
  await vi.waitFor(() => expect(target).toHaveClass("job-highlight"));
  expect(screen.getByText("别的歌").closest("li")).not.toHaveClass("job-highlight");
  expect(scroll).toHaveBeenCalled();
  await tick(3500);
  expect(target).not.toHaveClass("job-highlight");
});

test("finishing a job from this session toasts once; ▶ plays it; it auto-dismisses", async () => {
  const list = sequence([job({ status: "downloading", progress: 10 })], [job({ status: "done", track_id: 7 })]);
  const { audio } = renderWithApp(<Starter />, { routes: ytRoutes(list) });
  await userEvent.click(screen.getByRole("button", { name: "开始" }));
  expect(screen.queryByText("已下载：歌曲标题")).not.toBeInTheDocument();
  await tick();
  const toast = await screen.findByRole("status");
  expect(within(toast).getByText("已下载：歌曲标题")).toBeInTheDocument();
  await tick(2000);
  expect(screen.getAllByText("已下载：歌曲标题")).toHaveLength(1);
  await userEvent.click(within(toast).getByRole("button", { name: /播放/ }));
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/7/stream"));
  expect(screen.queryByText("已下载：歌曲标题")).not.toBeInTheDocument();
});

test("a toast auto-dismisses after about 8 s", async () => {
  const list = sequence([job({ status: "downloading" })], [job({ status: "done", track_id: 7, progress: 100 })]);
  renderWithApp(<Starter />, { routes: ytRoutes(list) });
  await userEvent.click(screen.getByRole("button", { name: "开始" }));
  await tick();
  expect(await screen.findByText("已下载：歌曲标题")).toBeInTheDocument();
  await tick(8100);
  expect(screen.queryByText("已下载：歌曲标题")).not.toBeInTheDocument();
});

test("no toast when the row on screen already offers ▶ 播放", async () => {
  const list = sequence([job({ status: "downloading", progress: 10 })], [job({ status: "done", track_id: 7 })]);
  renderWithApp(<SearchPage />, { routes: ytRoutes(list) });
  await startDownload();
  await tick();
  const row = (await screen.findByText("视频v1")).closest("li")!;
  await within(row).findByRole("button", { name: /播放/ });
  await tick(500);
  expect(screen.queryByText("已下载：歌曲标题")).not.toBeInTheDocument();
});

test("polling skips while the page is hidden and catches up when it is shown", async () => {
  const list = sequence([job({ status: "downloading", progress: 10 })]);
  renderWithApp(<Starter />, { routes: ytRoutes(list) });
  let hidden = false;
  Object.defineProperty(document, "hidden", { configurable: true, get: () => hidden });
  try {
    await userEvent.click(screen.getByRole("button", { name: "开始" }));
    await vi.waitFor(() => expect(list).toHaveBeenCalledTimes(1));
    hidden = true;
    document.dispatchEvent(new Event("visibilitychange"));
    await tick(10_000);
    expect(list).toHaveBeenCalledTimes(1);
    hidden = false;
    document.dispatchEvent(new Event("visibilitychange"));
    await vi.waitFor(() => expect(list).toHaveBeenCalledTimes(2));
  } finally {
    delete (document as unknown as Record<string, unknown>).hidden;
  }
});

test("failed polls back off and stop after five in a row", async () => {
  const list = vi.fn(() => ({ status: 500, body: { error: "boom" } }));
  renderWithApp(<Starter />, { routes: ytRoutes(list) });
  await userEvent.click(screen.getByRole("button", { name: "开始" }));
  await vi.waitFor(() => expect(list).toHaveBeenCalledTimes(1));
  await tick(4000); // 2nd, after 4 s
  expect(list).toHaveBeenCalledTimes(2);
  await tick(4000); // the next wait is 8 s
  expect(list).toHaveBeenCalledTimes(2);
  await tick(4000);
  expect(list).toHaveBeenCalledTimes(3);
  await tick(16_000 + 32_000);
  expect(list).toHaveBeenCalledTimes(5);
  await tick(120_000);
  expect(list).toHaveBeenCalledTimes(5);
});

test("jobs not started in this session never toast", async () => {
  const list = sequence([job({ id: 99, status: "downloading" })], [job({ id: 99, status: "done", track_id: 7 })]);
  renderWithApp(<DownloadsPage />, { path: "/downloads", routes: { "GET /api/v1/downloads": list } });
  await screen.findByText("歌曲标题");
  await tick(16_000);
  expect(screen.queryByText("已下载：歌曲标题")).not.toBeInTheDocument();
});

test("the link card's single song shows live status and plays when done", async () => {
  const list = sequence([job()], [job({ status: "done", track_id: 7 })]);
  const { audio } = renderWithApp(
    <LinkCard link={{ videoId: "v1", listId: null, isMix: false }} />,
    { routes: ytRoutes(list) },
  );
  await userEvent.click(screen.getByRole("button", { name: /下载/ }));
  expect(await screen.findByText("排队中")).toBeInTheDocument();
  await tick();
  const actions = within(document.querySelector(".link-actions") as HTMLElement);
  await userEvent.click(await actions.findByRole("button", { name: /播放/ }));
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/7/stream"));
});
