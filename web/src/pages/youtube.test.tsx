import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { MemoryRouter } from "react-router";
import { AuthProvider } from "../auth/AuthProvider";
import { DownloadsProvider } from "../downloads/DownloadsProvider";
import { PlayerProvider } from "../player/PlayerProvider";
import { YouTubeResults } from "../components/YouTubeResults";
import { FakeAudio } from "../test/setup";
import { renderWithApp } from "../test/render";
import SearchPage from "./SearchPage";
import DownloadsPage from "./DownloadsPage";
import type { DownloadJob, Track, YTVideo } from "../api/types";

const vid = (id: string, extra: Partial<YTVideo> = {}): YTVideo => ({
  id,
  title: `视频${id}`,
  channel: "频道A",
  url: `https://www.youtube.com/watch?v=${id}`,
  thumbnail: `https://i.ytimg.com/vi/${id}/default.jpg`,
  duration_s: 125,
  ...extra,
});

const job = (id: number, extra: Partial<DownloadJob> = {}): DownloadJob => ({
  id,
  user_id: 1,
  username: "u",
  url: "https://www.youtube.com/watch?v=abc",
  video_id: "abc",
  title: "歌曲标题",
  channel: "频道A",
  duration_s: 200,
  thumbnail: "https://i.ytimg.com/vi/abc/default.jpg",
  status: "queued",
  progress: 0,
  error: "",
  track_id: null,
  track_available: true,
  created_at: 1,
  updated_at: 1,
  ...extra,
});

const localTrack = (id: number, title: string): Track => ({
  id,
  title,
  artist: "a",
  artist_id: null,
  album: "b",
  album_id: null,
  year: null,
  duration_ms: 1000,
  codec: "mp3",
  bitrate: 320,
  lossless: false,
  status: "kept",
  broken: false,
  broken_reason: "",
  favorite: false,
  disliked: false,
  library_id: 1,
  path: "",
  added_at: 1,
  track_no: null,
  disc_no: null,
});

test("empty local search shows YouTube results automatically; 下载 adds it", async () => {
  const createDownload = vi.fn(() => ({ status: 201, body: { jobs: [job(1, { status: "queued", user_id: 1 })] } }));
  renderWithApp(<SearchPage />, {
    routes: {
      "GET /api/v1/search": () => ({ body: { tracks: [], albums: [], artists: [] } }),
      "GET /api/v1/youtube/search": () => ({ body: { videos: [vid("v1")], playlists: [] } }),
      "POST /api/v1/downloads": createDownload,
    },
  });
  await userEvent.type(screen.getByRole("searchbox"), "abcd");
  expect(await screen.findByText("视频v1", {}, { timeout: 3000 })).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "下载" }));
  expect(await screen.findByText("排队中")).toBeInTheDocument();
  expect(JSON.parse(((createDownload.mock.calls[0] as unknown[])[0] as RequestInit).body as string)).toEqual({ video: vid("v1") });
});

test("non-empty local search shows a button that searches YouTube on tap", async () => {
  renderWithApp(<SearchPage />, {
    routes: {
      "GET /api/v1/search": () => ({ body: { tracks: [localTrack(1, "本地歌")], albums: [], artists: [] } }),
      "GET /api/v1/youtube/search": () => ({ body: { videos: [vid("v2")], playlists: [] } }),
    },
  });
  await userEvent.type(screen.getByRole("searchbox"), "本地歌");
  expect(await screen.findByText("本地歌")).toBeInTheDocument();
  const btn = await screen.findByRole("button", { name: /在 YouTube 上搜索/ }, { timeout: 3000 }); // after the 900 ms settle
  expect(screen.queryByText("视频v2")).toBeNull();
  await userEvent.click(btn);
  expect(await screen.findByText("视频v2")).toBeInTheDocument();
});

test("a deduped job that is already done offers ▶ 播放", async () => {
  renderWithApp(<SearchPage />, {
    routes: {
      "GET /api/v1/search": () => ({ body: { tracks: [], albums: [], artists: [] } }),
      "GET /api/v1/youtube/search": () => ({ body: { videos: [vid("v3")], playlists: [] } }),
      "POST /api/v1/downloads": () => ({ status: 201, body: { jobs: [job(2, { status: "done", track_id: 9 })] } }),
    },
  });
  await userEvent.type(screen.getByRole("searchbox"), "xyz");
  await userEvent.click(await screen.findByRole("button", { name: "下载" }, { timeout: 3000 }));
  expect(await screen.findByRole("button", { name: /播放/ })).toBeInTheDocument();
});

test("a done job whose song is gone shows 已在音乐库", async () => {
  renderWithApp(<SearchPage />, {
    routes: {
      "GET /api/v1/search": () => ({ body: { tracks: [], albums: [], artists: [] } }),
      "GET /api/v1/youtube/search": () => ({ body: { videos: [vid("v3")], playlists: [] } }),
      "POST /api/v1/downloads": () => ({ status: 201, body: { jobs: [job(2, { status: "done", track_id: 9, track_available: false })] } }),
    },
  });
  await userEvent.type(screen.getByRole("searchbox"), "xyz");
  await userEvent.click(await screen.findByRole("button", { name: "下载" }, { timeout: 3000 }));
  expect(await screen.findByRole("button", { name: "已在音乐库" })).toBeInTheDocument();
});

test("a deduped job already queued by someone else shows 已在下载队列", async () => {
  renderWithApp(<SearchPage />, {
    routes: {
      "GET /api/v1/search": () => ({ body: { tracks: [], albums: [], artists: [] } }),
      "GET /api/v1/youtube/search": () => ({ body: { videos: [vid("v4")], playlists: [] } }),
      "POST /api/v1/downloads": () => ({ status: 201, body: { jobs: [job(3, { status: "downloading", user_id: 0, username: "" })] } }),
    },
  });
  await userEvent.type(screen.getByRole("searchbox"), "xyz9");
  await userEvent.click(await screen.findByRole("button", { name: "下载" }, { timeout: 3000 }));
  expect(await screen.findByRole("button", { name: "已在下载队列" })).toBeInTheDocument();
});

test("an unsafe thumbnail host falls back to the Cover placeholder", async () => {
  renderWithApp(<SearchPage />, {
    routes: {
      "GET /api/v1/search": () => ({ body: { tracks: [], albums: [], artists: [] } }),
      "GET /api/v1/youtube/search": () => ({ body: { videos: [vid("v5", { thumbnail: "https://evil.example.com/i.ytimg.com/thumb.jpg" })], playlists: [] } }),
    },
  });
  await userEvent.type(screen.getByRole("searchbox"), "xyz99");
  expect(await screen.findByText("视频v5", {}, { timeout: 3000 })).toBeInTheDocument();
  expect(screen.queryByRole("img")).toBeNull();
});

test("a slow search for an old query does not overwrite the new query's results", async () => {
  let resolveOld!: (r: Response) => void;
  const fn = vi.fn((input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input.toString();
    if (url.includes("/me")) {
      return Promise.resolve(new Response(JSON.stringify({ id: 1, username: "u", role: "member" }), { status: 200 }));
    }
    if (url.includes("/queue")) {
      return Promise.resolve(
        new Response(JSON.stringify({ queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] }), { status: 200 }),
      );
    }
    if (url.includes("/youtube/search") && url.includes("q=old")) {
      return new Promise<Response>((res) => {
        resolveOld = res;
      });
    }
    if (url.includes("/youtube/search") && url.includes("q=new")) {
      return Promise.resolve(new Response(JSON.stringify({ videos: [vid("newv")], playlists: [] }), { status: 200 }));
    }
    return Promise.resolve(new Response(JSON.stringify({ error: "no mock" }), { status: 599 }));
  });
  vi.stubGlobal("fetch", fn);

  function Harness() {
    const [q, setQ] = useState("old");
    return (
      <>
        <button onClick={() => setQ("new")}>switch</button>
        <YouTubeResults query={q} auto />
      </>
    );
  }

  render(
    <MemoryRouter>
      <AuthProvider>
        <PlayerProvider audio={new FakeAudio() as unknown as HTMLAudioElement} userId={1}>
          <DownloadsProvider>
          <Harness />
          </DownloadsProvider>
        </PlayerProvider>
      </AuthProvider>
    </MemoryRouter>,
  );

  expect(await screen.findByText("正在搜索 YouTube…")).toBeInTheDocument();
  await userEvent.click(screen.getByText("switch"));
  expect(await screen.findByText("视频newv")).toBeInTheDocument();

  resolveOld(new Response(JSON.stringify([vid("oldv")]), { status: 200 }));
  await new Promise((r) => setTimeout(r, 0));
  expect(screen.queryByText("视频oldv")).toBeNull();
  expect(screen.getByText("视频newv")).toBeInTheDocument();
});

test("downloads page pastes a link, lists jobs with badges, cancels, retries and plays", async () => {
  const create = vi.fn(() => ({ status: 201, body: { jobs: [job(10, { status: "queued", title: "新加的歌" })] } }));
  const list = vi.fn(() => ({
    body: [
      job(1, { status: "downloading", progress: 42, title: "下载中的歌" }),
      job(2, { status: "failed", error: "网络错误", title: "失败的歌" }),
      job(3, { status: "done", track_id: 55, title: "完成的歌" }),
    ],
  }));
  const del = vi.fn(() => ({ status: 204 }));
  const retry = vi.fn(() => ({ status: 204 }));
  const { audio } = renderWithApp(<DownloadsPage />, {
    path: "/downloads",
    routes: {
      "GET /api/v1/downloads": list,
      "POST /api/v1/downloads": create,
      "DELETE /api/v1/downloads/1": del,
      "POST /api/v1/downloads/2/retry": retry,
      "GET /api/v1/tracks/55": () => ({ body: localTrack(55, "完成的歌") }),
    },
  });

  expect(await screen.findByText("下载中的歌")).toBeInTheDocument();
  expect(screen.getByText("下载中 42%")).toBeInTheDocument();
  // A failed job's row: a short translated line, the raw error only as its tooltip.
  expect(screen.getByText("下载失败")).toHaveAttribute("title", "网络错误");

  const rowFor = (title: string) => screen.getByText(title).closest("li")!;

  await userEvent.click(within(rowFor("下载中的歌")).getByRole("button", { name: "取消" }));
  await vi.waitFor(() => expect(del).toHaveBeenCalled());
  expect(within(rowFor("下载中的歌")).getByText("已取消")).toBeInTheDocument();

  await userEvent.click(within(rowFor("失败的歌")).getByRole("button", { name: "重试" }));
  await vi.waitFor(() => expect(retry).toHaveBeenCalled());
  expect(within(rowFor("失败的歌")).getByText("排队中")).toBeInTheDocument();

  await userEvent.click(within(rowFor("完成的歌")).getByRole("button", { name: "播放" }));
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/55/stream"));

  await userEvent.type(screen.getByPlaceholderText("粘贴 YouTube 链接（单曲或播放列表）"), "https://youtu.be/abc");
  await userEvent.click(screen.getByRole("button", { name: "下载" }));
  expect(await screen.findByText("已加入 1 首")).toBeInTheDocument();
  expect(JSON.parse(((create.mock.calls[0] as unknown[])[0] as RequestInit).body as string)).toEqual({ url: "https://youtu.be/abc" });
  expect(await screen.findByText("新加的歌")).toBeInTheDocument();
});

test("polls every 2 s while a job is active, then backs off once idle", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    const list = vi
      .fn()
      .mockReturnValueOnce({ body: [job(1, { status: "downloading" })] })
      .mockReturnValue({ body: [job(1, { status: "done", track_id: 5 })] });
    renderWithApp(<DownloadsPage />, { path: "/downloads", routes: { "GET /api/v1/downloads": list } });
    await vi.waitFor(() => expect(list).toHaveBeenCalledTimes(1));
    await vi.advanceTimersByTimeAsync(2000);
    await vi.waitFor(() => expect(list).toHaveBeenCalledTimes(2));
  } finally {
    vi.useRealTimers();
  }
});

// Review round 1 (important): 播放 must prime the audio element
// synchronously inside the tap, before awaiting api.track — iOS only
// allows play() to start from within the gesture itself.
test("播放 primes the audio element synchronously, before the track fetch resolves", async () => {
  const { audio } = renderWithApp(<DownloadsPage />, {
    path: "/downloads",
    routes: {
      "GET /api/v1/downloads": () => ({ body: [job(3, { status: "done", track_id: 55, title: "完成的歌" })] }),
      "GET /api/v1/tracks/55": () => ({ body: localTrack(55, "完成的歌") }),
    },
  });
  const btn = await screen.findByRole("button", { name: "播放" });
  act(() => btn.click());
  expect(audio.play).toHaveBeenCalledTimes(1); // primed before the track fetch resolved
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/55/stream"));
});

// Review round 1 (minor): a play failure shows on that job's own row, not
// the page-level banner.
test("a play failure shows on that job's row, not the page-level banner", async () => {
  renderWithApp(<DownloadsPage />, {
    path: "/downloads",
    routes: {
      "GET /api/v1/downloads": () => ({ body: [job(7, { status: "done", track_id: 77, title: "坏掉的歌" })] }),
      "GET /api/v1/tracks/77": () => ({ status: 404, body: { error: "not found" } }),
    },
  });
  await userEvent.click(await screen.findByRole("button", { name: "播放" }));
  const row = screen.getByText("坏掉的歌").closest("li")!;
  expect(await within(row).findByText("歌曲已不在音乐库")).toBeInTheDocument();
  // Not hoisted to a page-level banner outside the row.
  expect(screen.getAllByText("歌曲已不在音乐库")).toHaveLength(1);
});

// Review round 1 (minor): a failed download row shows the error and a 重试
// button that resets it to idle so the user can tap 下载 again.
test("a failed download row shows an error and 重试 lets you download again", async () => {
  const createDownload = vi
    .fn()
    .mockReturnValueOnce({ status: 500, body: { error: "下载失败了" } })
    .mockReturnValueOnce({ status: 201, body: { jobs: [job(20, { status: "queued" })] } });
  renderWithApp(<SearchPage />, {
    routes: {
      "GET /api/v1/search": () => ({ body: { tracks: [], albums: [], artists: [] } }),
      "GET /api/v1/youtube/search": () => ({ body: { videos: [vid("v6")], playlists: [] } }),
      "POST /api/v1/downloads": createDownload,
    },
  });
  await userEvent.type(screen.getByRole("searchbox"), "retry1");
  await userEvent.click(await screen.findByRole("button", { name: "下载" }, { timeout: 3000 }));
  expect(await screen.findByText("下载失败了")).toBeInTheDocument();

  await userEvent.click(screen.getByRole("button", { name: "重试" }));
  expect(screen.queryByText("下载失败了")).toBeNull();

  await userEvent.click(await screen.findByRole("button", { name: "下载" }));
  expect(await screen.findByText("排队中")).toBeInTheDocument();
  expect(createDownload).toHaveBeenCalledTimes(2);
});

// Review round 1 (minor): an explicit re-entrancy guard in download() — a
// second tap while a request for the same row is in flight must not send a
// second POST (the disabled button already blocks a real second click;
// this exercises the end-to-end guarantee rather than just the UI attribute).
test("tapping 下载 again while the request is in flight sends only one POST", async () => {
  let resolveCreate!: (r: Response) => void;
  const fn = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.toString();
    if (url.includes("/me")) return Promise.resolve(new Response(JSON.stringify({ id: 1, username: "u", role: "member" }), { status: 200 }));
    if (url.includes("/queue")) {
      return Promise.resolve(
        new Response(JSON.stringify({ queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] }), { status: 200 }),
      );
    }
    if (url.includes("/youtube/search")) return Promise.resolve(new Response(JSON.stringify({ videos: [vid("vre")], playlists: [] }), { status: 200 }));
    if (url.includes("/downloads") && init?.method === "POST") {
      return new Promise<Response>((res) => {
        resolveCreate = res;
      });
    }
    return Promise.resolve(new Response(JSON.stringify({ error: "no mock" }), { status: 599 }));
  });
  vi.stubGlobal("fetch", fn);

  render(
    <MemoryRouter>
      <AuthProvider>
        <PlayerProvider audio={new FakeAudio() as unknown as HTMLAudioElement} userId={1}>
          <DownloadsProvider>
          <YouTubeResults query="re" auto />
          </DownloadsProvider>
        </PlayerProvider>
      </AuthProvider>
    </MemoryRouter>,
  );

  const btn = await screen.findByRole("button", { name: "下载" });
  await userEvent.click(btn);
  expect(screen.getByRole("button", { name: "下载" })).toBeDisabled();
  await userEvent.click(screen.getByRole("button", { name: "下载" })); // disabled: no-op

  resolveCreate(new Response(JSON.stringify({ jobs: [job(99, { status: "queued" })] }), { status: 201 }));
  expect(await screen.findByText("排队中")).toBeInTheDocument();

  const postCalls = fn.mock.calls.filter(([, i]) => (i as RequestInit | undefined)?.method === "POST");
  expect(postCalls.length).toBe(1);
});

// Final review: the automatic YouTube search waits for the input to be still
// for 900 ms (typing pauses longer than the 250 ms local debounce must not
// each fire one) and runs once per settled query.
test("auto YouTube search waits for a 900 ms pause and fires once", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    const yt = vi.fn(() => ({ body: { videos: [vid("v7")], playlists: [] } }));
    const local = vi.fn(() => ({ body: { tracks: [], albums: [], artists: [] } }));
    renderWithApp(<SearchPage />, { routes: { "GET /api/v1/search": local, "GET /api/v1/youtube/search": yt } });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const box = screen.getByRole("searchbox");
    await user.type(box, "a");
    await vi.advanceTimersByTimeAsync(500);
    await user.type(box, "b");
    await vi.advanceTimersByTimeAsync(500);
    await user.type(box, "c");
    await vi.advanceTimersByTimeAsync(500);
    expect(local).toHaveBeenCalled(); // local search kept up with the pauses...
    expect(yt).not.toHaveBeenCalled(); // ...YouTube did not
    await vi.advanceTimersByTimeAsync(500);
    expect(await screen.findByText("视频v7")).toBeInTheDocument();
    await vi.advanceTimersByTimeAsync(2000);
    expect(yt).toHaveBeenCalledTimes(1);
  } finally {
    vi.useRealTimers();
  }
});

test("Enter searches YouTube immediately, once", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    const yt = vi.fn(() => ({ body: { videos: [vid("v8")], playlists: [] } }));
    renderWithApp(<SearchPage />, {
      routes: {
        "GET /api/v1/search": () => ({ body: { tracks: [localTrack(1, "本地歌")], albums: [], artists: [] } }),
        "GET /api/v1/youtube/search": yt,
      },
    });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await user.type(screen.getByRole("searchbox"), "xy{Enter}");
    expect(await screen.findByText("视频v8")).toBeInTheDocument();
    expect(yt).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(2000); // the settle timer must not search again
    expect(yt).toHaveBeenCalledTimes(1);
    expect(screen.getByText("视频v8")).toBeInTheDocument();
  } finally {
    vi.useRealTimers();
  }
});

// Final review: a retry refused because another job already covers the
// video (409) shows the server's message on that row, not a page banner.
test("a retry blocked by dedupe shows 已在下载队列 on the row", async () => {
  renderWithApp(<DownloadsPage />, {
    path: "/downloads",
    routes: {
      "GET /api/v1/downloads": () => ({ body: [job(2, { status: "failed", error: "网络错误", title: "失败的歌" })] }),
      "POST /api/v1/downloads/2/retry": () => ({ status: 409, body: { error: "已在下载队列" } }),
    },
  });
  const row = (await screen.findByText("失败的歌")).closest("li")!;
  await userEvent.click(within(row).getByRole("button", { name: "重试" }));
  expect(await within(row).findByText("已在下载队列")).toBeInTheDocument();
  expect(screen.getAllByText("已在下载队列")).toHaveLength(1);
  expect(within(row).getByRole("button", { name: "重试" })).toBeInTheDocument(); // still failed
});

const pl = (extra = {}) => ({ id: "PLfakelist0001", title: "假歌单", channel: "歌单频道", url: "https://www.youtube.com/playlist?list=PLfakelist0001", thumbnail: "", count: 0, ...extra });
const bodyOf = (call: unknown[]) => JSON.parse((call[0] as RequestInit).body as string);

function renderPlaylistSearch(count: number, extra: Record<string, () => { status?: number; body?: unknown }> = {}) {
  const post = vi.fn(() => ({ status: 201, body: { jobs: [job(1), job(2)], playlist: { id: 5, name: "假歌单", list_id: "PLfakelist0001" } } }));
  const info = vi.fn(() => ({ body: pl({ count }) }));
  renderWithApp(<SearchPage />, {
    routes: {
      "GET /api/v1/search": () => ({ body: { tracks: [], albums: [], artists: [] } }),
      "GET /api/v1/youtube/search": () => ({ body: { videos: [], playlists: [pl()] } }),
      "GET /api/v1/youtube/playlist": info,
      "POST /api/v1/downloads": post,
      ...extra,
    },
  });
  return { post, info };
}

test("a playlist result downloads in full: 下载全部 → confirm with the count → added to the Lark playlist", async () => {
  const { post, info } = renderPlaylistSearch(2);
  await userEvent.type(screen.getByRole("searchbox"), "歌单");
  expect(await screen.findByText("假歌单", {}, { timeout: 3000 })).toBeInTheDocument();
  expect(screen.getByText("YouTube 歌单")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "下载全部" }));
  await userEvent.click(await screen.findByRole("button", { name: "下载全部（2）" }));
  expect(info.mock.calls.length).toBe(1);
  expect(bodyOf(post.mock.calls[0] as unknown[])).toEqual({ url: "https://www.youtube.com/playlist?list=PLfakelist0001" });
  expect(await screen.findByText("已加入 2 首到「假歌单」")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "打开歌单" })).toHaveAttribute("href", "/playlists/5");
});

test("a list longer than 200 offers the first 200", async () => {
  renderPlaylistSearch(247);
  await userEvent.type(screen.getByRole("searchbox"), "歌单");
  await userEvent.click(await screen.findByRole("button", { name: "下载全部" }, { timeout: 3000 }));
  expect(await screen.findByRole("button", { name: "下载前 200 首（共 247 首）" })).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "取消" }));
  expect(screen.getByRole("button", { name: "下载全部" })).toBeInTheDocument();
});

test("a failed playlist lookup shows the error and retries", async () => {
  let n = 0;
  renderPlaylistSearch(3, { "GET /api/v1/youtube/playlist": () => (n++ === 0 ? { status: 502, body: { error: "boom" } } : { body: pl({ count: 3 }) }) });
  await userEvent.type(screen.getByRole("searchbox"), "歌单");
  await userEvent.click(await screen.findByRole("button", { name: "下载全部" }, { timeout: 3000 }));
  await userEvent.click(await screen.findByRole("button", { name: "重试" }));
  expect(await screen.findByRole("button", { name: "下载全部（3）" })).toBeInTheDocument();
});

function renderPaste(link: string) {
  const post = vi.fn(() => ({ status: 201, body: { jobs: [job(1)] } }));
  const { f } = renderWithApp(<SearchPage />, { routes: { "POST /api/v1/downloads": post } });
  return { post, f, paste: async () => userEvent.click(screen.getByRole("searchbox")).then(() => userEvent.paste(link)) };
}

test("a pasted watch+list link offers the song or the whole list, and never searches", async () => {
  const { post, f, paste } = renderPaste("https://www.youtube.com/watch?v=fakevideo03&list=PLfakelist0001");
  await paste();
  expect(await screen.findByRole("button", { name: "这首歌" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "整个歌单" })).toBeInTheDocument();
  await new Promise((r) => setTimeout(r, 1200)); // past both search debounces
  const urls = f.mock.calls.map((c) => String(c[0]));
  expect(urls.some((u) => u.includes("/v1/search") || u.includes("/youtube/search"))).toBe(false);
  await userEvent.click(screen.getByRole("button", { name: "这首歌" }));
  expect(await screen.findByText("排队中")).toBeInTheDocument();
  expect(bodyOf(post.mock.calls[0] as unknown[])).toEqual({ url: "https://www.youtube.com/watch?v=fakevideo03" });
});

test("a pasted mix link only offers 下载这首歌", async () => {
  const { paste } = renderPaste("https://www.youtube.com/watch?v=fakevideo03&list=RDfakevideo03");
  await paste();
  expect(await screen.findByRole("button", { name: "下载这首歌" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "整个歌单" })).toBeNull();
});

test("移除 is only offered on failed and cancelled jobs", async () => {
  const del = vi.fn(() => ({ status: 204 }));
  renderWithApp(<DownloadsPage />, {
    path: "/downloads",
    routes: {
      "GET /api/v1/downloads": () => ({ body: [
        job(3, { status: "done", track_id: 55, track_available: true, title: "完成的歌" }),
        job(4, { status: "failed", error: "ERROR: boom", title: "失败的歌" }),
        job(5, { status: "cancelled", title: "取消的歌" }),
        job(6, { status: "queued", title: "排队的歌" }),
      ] }),
      "DELETE /api/v1/downloads/4": del,
    },
  });
  const row = (title: string) => screen.getByText(title).closest("li")!;
  await screen.findByText("完成的歌");
  expect(within(row("完成的歌")).queryByRole("button", { name: "移除" })).toBeNull();
  expect(within(row("完成的歌")).getByRole("button", { name: "播放" })).toBeInTheDocument();
  expect(within(row("排队的歌")).queryByRole("button", { name: "移除" })).toBeNull();
  expect(within(row("取消的歌")).getByRole("button", { name: "移除" })).toBeInTheDocument();
  await userEvent.click(within(row("失败的歌")).getByRole("button", { name: "移除" }));
  await vi.waitFor(() => expect(del).toHaveBeenCalled());
  await vi.waitFor(() => expect(screen.queryByText("失败的歌")).toBeNull());
});

test("a YouTube result with a channel id offers 关注频道, following by id", async () => {
  const follow = vi.fn(() => ({ status: 201, body: {} }));
  renderWithApp(<SearchPage />, {
    routes: {
      "GET /api/v1/search": () => ({ body: { tracks: [], albums: [], artists: [] } }),
      "GET /api/v1/youtube/search": () => ({ body: { videos: [vid("v1", { channel_id: "UC0e5c4U67Vm6sAVK0vxN3Uw" }), vid("v2")], playlists: [] } }),
      "POST /api/v1/channels/follow": follow,
    },
  });
  await userEvent.type(screen.getByRole("searchbox"), "abcd");
  const row = (await screen.findByText("视频v1", {}, { timeout: 3000 })).closest("li")!;
  expect(within(screen.getByText("视频v2").closest("li")!).queryByRole("button", { name: "关注频道" })).toBeNull();
  await userEvent.click(within(row).getByRole("button", { name: "关注频道" }));
  await within(row).findByRole("button", { name: "已关注" });
  expect(JSON.parse(((follow.mock.calls[0] as unknown[])[0] as RequestInit).body as string)).toEqual({ id: "UC0e5c4U67Vm6sAVK0vxN3Uw" });
});

test("关注频道 knows the channels already followed", async () => {
  renderWithApp(<SearchPage />, {
    routes: {
      "GET /api/v1/search": () => ({ body: { tracks: [], albums: [], artists: [] } }),
      "GET /api/v1/youtube/search": () => ({ body: { videos: [vid("v1", { channel_id: "UC0e5c4U67Vm6sAVK0vxN3Uw" }), vid("v3", { channel_id: "UCRABK12_6Ie2X549K9cXS0g" })], playlists: [] } }),
      "GET /api/v1/channels": () => ({
        body: { channels: [{ channel: { id: "UC0e5c4U67Vm6sAVK0vxN3Uw" }, settings: {}, followed_at: 1, unplayed: 0, latest_published_at: null }], usage: { bytes: 0, files: 0, max_bytes: 1 }, default_keep_days: 15 },
      }),
    },
  });
  await userEvent.type(screen.getByRole("searchbox"), "abcd");
  const row = (await screen.findByText("视频v1", {}, { timeout: 3000 })).closest("li")!;
  expect(await within(row).findByRole("button", { name: "已关注" })).toBeInTheDocument();
  expect(within(screen.getByText("视频v3").closest("li")!).getByRole("button", { name: "关注频道" })).toBeInTheDocument();
});
