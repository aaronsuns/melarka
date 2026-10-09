import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import PlaylistsPage from "./PlaylistsPage";
import MyDownloadsPage from "./MyDownloadsPage";
import AllDownloadsPage from "./admin/AllDownloadsPage";
import { renderWithApp } from "../test/render";
import type { DownloadJob, Track } from "../api/types";

const tr = (id: number) => ({ id, title: `歌${id}`, artist: "假歌手", album: "", duration_ms: 1000, codec: "aac", bitrate: 128, lossless: false }) as Track;

test("My downloads is the first row of Playlists; ▶ plays it newest first without opening it", async () => {
  const tracks = vi.fn(() => ({ body: [tr(5), tr(4)] }));
  const { audio } = renderWithApp(<PlaylistsPage />, {
    path: "/playlists",
    routes: { "GET /api/v1/playlists": () => ({ body: [{ id: 7, name: "车上", track_count: 2 }] }), "GET /api/v1/downloads/tracks": tracks },
  });
  await screen.findByText("车上");
  const first = screen.getAllByRole("listitem")[0];
  expect(within(first).getByRole("link", { name: /我的下载/ })).toHaveAttribute("href", "/my-downloads");
  expect(tracks).not.toHaveBeenCalled(); // nothing fetched until asked
  await userEvent.click(within(first).getByRole("button", { name: "播放我的下载" }));
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/5/stream"));
});

test("🔀 shuffles My downloads", async () => {
  const { audio } = renderWithApp(<PlaylistsPage />, {
    path: "/playlists",
    routes: { "GET /api/v1/playlists": () => ({ body: [] }), "GET /api/v1/downloads/tracks": () => ({ body: [tr(5), tr(4), tr(3)] }) },
  });
  const button = await screen.findByRole("button", { name: "随机播放我的下载" });
  const spy = vi.spyOn(Math, "random").mockReturnValue(0); // after render: nothing else may draw from it
  try {
    await userEvent.click(button);
    await vi.waitFor(() => expect(audio.src).toContain("/tracks/4/stream")); // shuffled([5,4,3], () => 0) = [4,3,5]
  } finally {
    spy.mockRestore();
  }
});

test("an empty My downloads says so instead of playing", async () => {
  renderWithApp(<PlaylistsPage />, {
    path: "/playlists",
    routes: { "GET /api/v1/playlists": () => ({ body: [] }), "GET /api/v1/downloads/tracks": () => ({ body: [] }) },
  });
  await userEvent.click(await screen.findByRole("button", { name: "播放我的下载" }));
  expect(await screen.findByText("从 YouTube 下载的歌会出现在这里。")).toBeInTheDocument();
});

test("the My downloads page lists the songs and plays all", async () => {
  const { audio } = renderWithApp(<MyDownloadsPage />, { path: "/my-downloads", routes: { "GET /api/v1/downloads/tracks": () => ({ body: [tr(5), tr(4)] }) } });
  expect(await screen.findByRole("heading", { name: "我的下载" })).toBeInTheDocument();
  expect(screen.getByText("歌4")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
  expect(audio.src).toContain("/tracks/5/stream");
});

const job = (id: number, user_id: number, username: string): DownloadJob => ({
  id, user_id, username, url: "", video_id: `v${id}`, title: `任务${id}`, channel: "C", duration_s: 1, thumbnail: "",
  status: "done", progress: 100, error: "", track_id: id, track_available: true, created_at: id, updated_at: id,
});

test("admin All downloads: the user picker filters the jobs and plays that user's downloads", async () => {
  const tracks = vi.fn((_i: RequestInit, url: string) => ({ body: url.includes("user=2") ? [tr(9)] : [tr(8), tr(9)] }));
  const { audio } = renderWithApp(<AllDownloadsPage />, {
    role: "admin",
    path: "/admin/downloads",
    routes: {
      "GET /api/v1/downloads": () => ({ body: [job(1, 1, "u"), job(2, 2, "bob")] }),
      "GET /api/v1/users": () => ({ body: [{ id: 1, username: "u", role: "admin" }, { id: 2, username: "bob", role: "member" }] }),
      "GET /api/v1/downloads/tracks": tracks,
    },
  });
  await screen.findByText("任务1");
  await userEvent.selectOptions(await screen.findByLabelText("谁的下载"), "2");
  expect(screen.queryByText("任务1")).toBeNull();
  expect(screen.getByText("任务2")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "播放全部" }));
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/9/stream"));
  expect(String(tracks.mock.calls[0][1])).toContain("user=2");
});
