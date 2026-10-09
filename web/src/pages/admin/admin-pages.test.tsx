import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { AuthProvider } from "../../auth/AuthProvider";
import { PlayerProvider, usePlayer } from "../../player/PlayerProvider";
import { FakeAudio } from "../../test/setup";
import { renderWithApp } from "../../test/render";
import PendingPage from "./PendingPage";
import AllDownloadsPage from "./AllDownloadsPage";
import TrashPage from "./TrashPage";
import LibrariesPage from "./LibrariesPage";
import SystemPage from "./SystemPage";
import type { DownloadJob, Library, ScanStatus, Track, TrashItem } from "../../api/types";

const track = (id: number, title: string, extra: Partial<Track> = {}): Track => ({
  id,
  title,
  artist: "歌手",
  artist_id: null,
  album: "专辑",
  album_id: null,
  year: null,
  duration_ms: 180_000,
  codec: "mp3",
  bitrate: 320,
  lossless: false,
  status: "pending",
  broken: false,
  broken_reason: "",
  favorite: false,
  disliked: false,
  library_id: 1,
  path: `a/${id}.mp3`,
  added_at: id,
  track_no: null,
  disc_no: null,
  ...extra,
});

const pendingPage = (items: Track[]) => ({ body: { items, next_cursor: "" } });

// ---------- PendingPage ----------

test("全选 then 保留所选 sends a PUT per track and clears the list", async () => {
  const put = vi.fn(() => ({ status: 204 }));
  renderWithApp(<PendingPage />, {
    role: "admin",
    path: "/admin/pending",
    routes: {
      "GET /api/v1/tracks": () => pendingPage([track(1, "歌A"), track(2, "歌B")]),
      "PUT /api/v1/tracks/1/status": put,
      "PUT /api/v1/tracks/2/status": put,
    },
  });
  await screen.findByText("歌A");
  await userEvent.click(screen.getByRole("button", { name: "全选" }));
  await userEvent.click(screen.getByRole("button", { name: "保留所选" }));
  await vi.waitFor(() => expect(put).toHaveBeenCalledTimes(2));
  expect(JSON.parse(((put.mock.calls[0] as unknown[])[0] as RequestInit).body as string)).toEqual({ status: "kept" });
  await vi.waitFor(() => expect(screen.queryByText("歌A")).toBeNull());
  expect(screen.queryByText("歌B")).toBeNull();
});

test("bulk keep stops at the first error and shows it, leaving the rest unprocessed", async () => {
  const put = vi
    .fn()
    .mockReturnValueOnce({ status: 500, body: { error: "保存失败" } })
    .mockReturnValue({ status: 204 });
  renderWithApp(<PendingPage />, {
    role: "admin",
    path: "/admin/pending",
    routes: {
      "GET /api/v1/tracks": () => pendingPage([track(1, "歌A"), track(2, "歌B")]),
      "PUT /api/v1/tracks/1/status": put,
      "PUT /api/v1/tracks/2/status": put,
    },
  });
  await screen.findByText("歌A");
  await userEvent.click(screen.getByRole("button", { name: "全选" }));
  await userEvent.click(screen.getByRole("button", { name: "保留所选" }));
  expect(await screen.findByText("保存失败")).toBeInTheDocument();
  expect(put).toHaveBeenCalledTimes(1);
  expect(screen.getByText("歌A")).toBeInTheDocument();
  expect(screen.getByText("歌B")).toBeInTheDocument();
});

test("删除所选 requires a second tap before trashing the selected tracks", async () => {
  const del = vi.fn(() => ({ status: 204 }));
  renderWithApp(<PendingPage />, {
    role: "admin",
    path: "/admin/pending",
    routes: {
      "GET /api/v1/tracks": () => pendingPage([track(1, "歌A")]),
      "DELETE /api/v1/tracks/1": del,
    },
  });
  await screen.findByText("歌A");
  await userEvent.click(screen.getByLabelText("选择：歌A"));
  await userEvent.click(screen.getByRole("button", { name: "删除所选" }));
  expect(del).not.toHaveBeenCalled();
  expect(screen.getByRole("button", { name: "确认删除所选" })).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "确认删除所选" }));
  await vi.waitFor(() => expect(del).toHaveBeenCalled());
  await vi.waitFor(() => expect(screen.queryByText("歌A")).toBeNull());
});

test("tapping a pending row plays it", async () => {
  const { audio } = renderWithApp(<PendingPage />, {
    role: "admin",
    path: "/admin/pending",
    routes: { "GET /api/v1/tracks": () => pendingPage([track(5, "歌C")]) },
  });
  await screen.findByText("歌C");
  await userEvent.click(screen.getByRole("button", { name: "播放：歌C" }));
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/5/stream"));
});

function QueueProbe() {
  const { queue } = usePlayer();
  return <div data-testid="queue">{queue.tracks.map((t) => t.id).join(",")}</div>;
}

test("deleting a playing pending track in bulk removes it from the player queue too", async () => {
  const del = vi.fn(() => ({ status: 204 }));
  renderWithApp(
    <>
      <PendingPage />
      <QueueProbe />
    </>,
    {
      role: "admin",
      path: "/admin/pending",
      routes: {
        "GET /api/v1/tracks": () => pendingPage([track(1, "歌A"), track(2, "歌B")]),
        "DELETE /api/v1/tracks/1": del,
      },
    },
  );
  await screen.findByText("歌A");
  await userEvent.click(screen.getByRole("button", { name: "播放：歌A" })); // now playing, both tracks queued
  await vi.waitFor(() => expect(screen.getByTestId("queue").textContent).toBe("1,2"));

  await userEvent.click(screen.getByLabelText("选择：歌A"));
  await userEvent.click(screen.getByRole("button", { name: "删除所选" }));
  await userEvent.click(screen.getByRole("button", { name: "确认删除所选" }));
  await vi.waitFor(() => expect(del).toHaveBeenCalled());
  await vi.waitFor(() => expect(screen.getByTestId("queue").textContent).toBe("2"));
});

// ---------- AllDownloadsPage ----------

const job = (id: number, extra: Partial<DownloadJob> = {}): DownloadJob => ({
  id,
  user_id: 1,
  username: "bob",
  url: "https://www.youtube.com/watch?v=abc",
  video_id: "abc",
  title: "歌曲标题",
  channel: "频道A",
  duration_s: 200,
  thumbnail: "https://i.ytimg.com/vi/abc/default.jpg",
  status: "downloading",
  progress: 10,
  error: "",
  track_id: null,
  track_available: false,
  created_at: 1,
  updated_at: 1,
  ...extra,
});

test("admin all-downloads shows every job with its owner's username", async () => {
  renderWithApp(<AllDownloadsPage />, {
    role: "admin",
    path: "/admin/downloads",
    routes: { "GET /api/v1/downloads": () => ({ body: [job(1, { username: "carol" })] }), "GET /api/v1/users": () => ({ body: [] }) },
  });
  await screen.findByText("歌曲标题");
  expect(screen.getByText("频道A · carol")).toBeInTheDocument();
});

// ---------- TrashPage ----------

const trashItem = (trackId: number, path: string, extra: Partial<TrashItem> = {}): TrashItem => ({
  track_id: trackId,
  path,
  trashed_at: Math.floor(Date.now() / 1000),
  purge_at: Math.floor(Date.now() / 1000) + 30 * 86400,
  ...extra,
});

test("restore shows the conflict message on a 409", async () => {
  const restore = vi.fn(() => ({ status: 409, body: { error: "a file already exists at the original path" } }));
  renderWithApp(<TrashPage />, {
    role: "admin",
    path: "/admin/trash",
    routes: {
      "GET /api/v1/trash": () => ({ body: [trashItem(7, "music/old.mp3")] }),
      "POST /api/v1/trash/7/restore": restore,
    },
  });
  await screen.findByText("music/old.mp3");
  await userEvent.click(screen.getByRole("button", { name: "恢复：music/old.mp3" }));
  expect(await screen.findByText("原位置已有同名文件")).toBeInTheDocument();
  expect(screen.getByText("music/old.mp3")).toBeInTheDocument(); // row stays
});

test("empty trash needs a second tap and reports the purged count", async () => {
  const purge = vi.fn(() => ({ status: 200, body: { purged: 3 } }));
  renderWithApp(<TrashPage />, {
    role: "admin",
    path: "/admin/trash",
    routes: {
      "GET /api/v1/trash": () => ({ body: [trashItem(1, "a.mp3"), trashItem(2, "b.mp3")] }),
      "DELETE /api/v1/trash": purge,
    },
  });
  await screen.findByText("a.mp3");
  await userEvent.click(screen.getByRole("button", { name: "清空回收站" }));
  expect(purge).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "确认清空回收站" }));
  expect(await screen.findByText("已删除 3 首")).toBeInTheDocument();
  await vi.waitFor(() => expect(screen.queryByText("a.mp3")).toBeNull());
});

// ---------- LibrariesPage ----------

const library = (id: number, name: string, extra: Partial<Library> = {}): Library => ({
  id,
  name,
  root: `/music/${name}`,
  download_target: false,
  last_scan_at: 1000,
  ...extra,
});

const scanStatus = (libraryId: number, extra: Partial<ScanStatus> = {}): ScanStatus => ({
  library_id: libraryId,
  running: false,
  last: { added: 0, updated: 0, moved: 0, missing: 0, broken: 0, unchanged: 0 },
  last_error: "",
  finished_at: 1000,
  ...extra,
});

test("重新扫描 triggers the scan and polls status every 2s until it finishes", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    const trigger = vi.fn(() => ({ status: 202, body: { queued_at: 1 } }));
    const status = vi
      .fn()
      .mockReturnValueOnce({ body: [scanStatus(1)] }) // initial load: idle
      .mockReturnValueOnce({ body: [scanStatus(1, { running: true })] }) // first poll after trigger
      .mockReturnValue({
        body: [scanStatus(1, { running: false, finished_at: 2000, last: { added: 3, updated: 1, moved: 0, missing: 0, broken: 2, unchanged: 5 } })],
      });
    renderWithApp(<LibrariesPage />, {
      role: "admin",
      path: "/admin/libraries",
      routes: {
        "GET /api/v1/admin/libraries": () => ({ body: [library(1, "主库")] }),
        "GET /api/v1/admin/scan/status": status,
        "POST /api/v1/admin/scan": trigger,
        "GET /api/v1/tracks": () => ({ body: { items: [], next_cursor: "" } }),
      },
    });
    await vi.waitFor(() => expect(screen.getByText("主库")).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "重新扫描：主库" }));
    await vi.waitFor(() => expect(trigger).toHaveBeenCalledTimes(1));
    expect(((trigger.mock.calls[0] as unknown[])[1] as string)).toContain("library=1");
    await vi.waitFor(() => expect(status).toHaveBeenCalledTimes(2));
    await vi.advanceTimersByTimeAsync(2000);
    await vi.waitFor(() => expect(status).toHaveBeenCalledTimes(3));
    await vi.waitFor(() => expect(screen.getByText("新增 3 · 更新 1 · 移动 0 · 缺失 0 · 损坏 2 · 未变 5")).toBeInTheDocument());
  } finally {
    vi.useRealTimers();
  }
});

test("broken files section shows the path and a reason, or 无法读取 when there's none", async () => {
  renderWithApp(<LibrariesPage />, {
    role: "admin",
    path: "/admin/libraries",
    routes: {
      "GET /api/v1/admin/libraries": () => ({ body: [] }),
      "GET /api/v1/admin/scan/status": () => ({ body: [] }),
      "GET /api/v1/tracks": () => ({
        body: { items: [track(9, "坏歌1", { broken: true, broken_reason: "corrupt", path: "bad1.wma" }), track(10, "坏歌2", { broken: true, path: "bad2.wma" })], next_cursor: "" },
      }),
    },
  });
  expect(await screen.findByText("bad1.wma")).toBeInTheDocument();
  expect(screen.getByText("corrupt")).toBeInTheDocument();
  expect(screen.getByText("bad2.wma")).toBeInTheDocument();
  expect(screen.getByText("无法读取")).toBeInTheDocument();
});

// ---------- SystemPage ----------

test("shows the current yt-dlp version and updates it", async () => {
  const update = vi.fn(() => ({ status: 200, body: { version: "2026.09.01", path: "/data/yt-dlp" } }));
  renderWithApp(<SystemPage />, {
    role: "admin",
    path: "/admin/system",
    routes: {
      "GET /api/v1/admin/ytdlp": () => ({ body: { version: "2026.01.01", path: "/data/yt-dlp" } }),
      "POST /api/v1/admin/ytdlp/update": update,
    },
  });
  expect(await screen.findByText("当前版本：2026.01.01")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "更新 yt-dlp" }));
  expect(await screen.findByText("已更新到 2026.09.01")).toBeInTheDocument();
  expect(screen.getByText("当前版本：2026.09.01")).toBeInTheDocument();
});

test("yt-dlp update shows a timeout message after 60s with no response", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    // mockFetch's handlers run synchronously (it destructures {status,
    // body} straight off the return value), so a hung request has to be
    // stubbed by hand, as in youtube.test.tsx's slow-search test.
    const fn = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.includes("/me")) return Promise.resolve(new Response(JSON.stringify({ id: 1, username: "u", role: "admin" }), { status: 200 }));
      if (url.includes("/queue")) {
        return Promise.resolve(
          new Response(JSON.stringify({ queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] }), { status: 200 }),
        );
      }
      if (url.includes("/radio/next")) return Promise.resolve(new Response(JSON.stringify([]), { status: 200 }));
      if (url.includes("/admin/ytdlp/update") && init?.method === "POST") return new Promise<Response>(() => {});
      if (url.includes("/admin/ytdlp")) return Promise.resolve(new Response(JSON.stringify({ version: "2026.01.01", path: "/data/yt-dlp" }), { status: 200 }));
      return Promise.resolve(new Response(JSON.stringify({ error: "no mock" }), { status: 599 }));
    });
    vi.stubGlobal("fetch", fn);

    render(
      <MemoryRouter initialEntries={["/admin/system"]}>
        <AuthProvider>
          <PlayerProvider audio={new FakeAudio() as unknown as HTMLAudioElement} userId={1}>
            <SystemPage />
          </PlayerProvider>
        </AuthProvider>
      </MemoryRouter>,
    );
    await screen.findByText("当前版本：2026.01.01");
    await userEvent.click(screen.getByRole("button", { name: "更新 yt-dlp" }));
    await vi.advanceTimersByTimeAsync(60_000);
    expect(await screen.findByText("更新超时（60 秒）")).toBeInTheDocument();
  } finally {
    vi.useRealTimers();
  }
});

test("更新 stays disabled past the timeout message until the real request actually settles", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    let resolveUpdate!: (r: Response) => void;
    const fn = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.includes("/me")) return Promise.resolve(new Response(JSON.stringify({ id: 1, username: "u", role: "admin" }), { status: 200 }));
      if (url.includes("/queue")) {
        return Promise.resolve(
          new Response(JSON.stringify({ queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] }), { status: 200 }),
        );
      }
      if (url.includes("/radio/next")) return Promise.resolve(new Response(JSON.stringify([]), { status: 200 }));
      if (url.includes("/admin/ytdlp/update") && init?.method === "POST") {
        return new Promise<Response>((res) => {
          resolveUpdate = res;
        });
      }
      if (url.includes("/admin/ytdlp")) return Promise.resolve(new Response(JSON.stringify({ version: "2026.01.01", path: "/data/yt-dlp" }), { status: 200 }));
      return Promise.resolve(new Response(JSON.stringify({ error: "no mock" }), { status: 599 }));
    });
    vi.stubGlobal("fetch", fn);

    render(
      <MemoryRouter initialEntries={["/admin/system"]}>
        <AuthProvider>
          <PlayerProvider audio={new FakeAudio() as unknown as HTMLAudioElement} userId={1}>
            <SystemPage />
          </PlayerProvider>
        </AuthProvider>
      </MemoryRouter>,
    );
    await screen.findByText("当前版本：2026.01.01");
    await userEvent.click(screen.getByRole("button", { name: "更新 yt-dlp" }));
    await vi.advanceTimersByTimeAsync(60_000);
    await screen.findByText("更新超时（60 秒）");
    expect(screen.getByRole("button", { name: "更新中…" })).toBeDisabled();

    // The real request finally settles, well after the client gave up.
    resolveUpdate(new Response(JSON.stringify({ version: "2026.09.01", path: "/data/yt-dlp" }), { status: 200 }));
    await vi.waitFor(() => expect(screen.getByRole("button", { name: "更新 yt-dlp" })).not.toBeDisabled());
  } finally {
    vi.useRealTimers();
  }
});
