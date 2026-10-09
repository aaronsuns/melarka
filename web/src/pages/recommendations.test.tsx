import { act, fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithApp } from "../test/render";
import HomePage from "./HomePage";
import RecommendationsPage from "./RecommendationsPage";
import type { DownloadJob, Recommendation, Recommendations, Track } from "../api/types";

const rec = (n: number, extra: Partial<Recommendation> = {}): Recommendation => ({
  video_id: `recvideo0${n}`.slice(0, 11),
  title: `推荐歌曲${n}`,
  channel: `频道${n}`,
  duration_s: 200,
  thumbnail: `https://i.ytimg.com/vi/recvideo0${n}/hqdefault.jpg`,
  url: `https://www.youtube.com/watch?v=recvideo0${n}`,
  reason: { track_id: 1, title: "甜蜜蜜", artist: "邓丽君", kind: "played" },
  score: 1,
  ...extra,
});

const recs = (items: Recommendation[], extra: Partial<Recommendations> = {}) => ({
  body: { items, refreshed_at: 1, refreshing: false, enabled: true, ...extra },
});

const job = (extra: Partial<DownloadJob> = {}): DownloadJob => ({
  id: 5, user_id: 1, username: "u", url: "https://www.youtube.com/watch?v=recvideo01", video_id: "recvideo01",
  title: "推荐歌曲1", channel: "频道1", duration_s: 200, thumbnail: "", status: "queued", progress: 0, error: "",
  track_id: null, track_available: true, created_at: 1, updated_at: 1, ...extra,
});

const track = (id: number) =>
  ({ id, title: "推荐歌曲1", artist: "a", album: "", album_id: null, duration_ms: 1000, codec: "aac", bitrate: 128, lossless: false, status: "pending" }) as Track;

const homeRoutes = { "GET /api/v1/tracks": () => ({ body: { items: [], next_cursor: "" } }), "GET /api/v1/downloads": () => ({ body: [] }) };

test("home shows the first 6 recommendations with their reasons and 查看全部", async () => {
  const items = [1, 2, 3, 4, 5, 6, 7, 8].map((n) => rec(n));
  items[1] = rec(2, { reason: { track_id: 2, title: "Faded", artist: "Alan Walker", kind: "favorite" } });
  renderWithApp(<HomePage />, { routes: { ...homeRoutes, "GET /api/v1/me/recommendations": () => recs(items) } });
  const section = (await screen.findByRole("heading", { name: "为你推荐" })).closest("section")!;
  expect(await within(section).findByText("推荐歌曲1")).toBeInTheDocument();
  expect(within(section).getByText("推荐歌曲6")).toBeInTheDocument();
  expect(within(section).queryByText("推荐歌曲7")).toBeNull();
  expect(within(section).getAllByText("因为你常听 甜蜜蜜").length).toBe(5);
  expect(within(section).getByText("因为你收藏了 Faded")).toBeInTheDocument();
  expect(within(section).getByText(/频道1/)).toBeInTheDocument();
  expect(within(section).getByRole("link", { name: "查看全部" })).toHaveAttribute("href", "/recommendations");
});

test("home explains an empty list and still links to the page (where 刷新 is)", async () => {
  renderWithApp(<HomePage />, { routes: { ...homeRoutes, "GET /api/v1/me/recommendations": () => recs([]) } });
  expect(await screen.findByText("播放或收藏几首歌后，这里会出现为你挑选的新歌。每晚自动更新。")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "查看全部" })).toHaveAttribute("href", "/recommendations");
});

test("a row keeps 下载 and a small ✕ 不感兴趣 side by side; a thumbnail that fails shows the initials tile", async () => {
  renderWithApp(<RecommendationsPage />, { routes: { "GET /api/v1/me/recommendations": () => recs([rec(1)]), "GET /api/v1/downloads": () => ({ body: [] }) } });
  const row = (await screen.findByText("推荐歌曲1")).closest("li")!;
  const dismiss = within(row).getByRole("button", { name: "不感兴趣" });
  expect(dismiss.querySelector("svg")).not.toBeNull(); // a round ✕ icon
  expect(dismiss).toHaveAttribute("title", "不感兴趣");
  expect(dismiss.parentElement).toBe(within(row).getByRole("button", { name: "下载" }).parentElement);
  const img = row.querySelector("img")!;
  expect(img).toHaveAttribute("src", "https://i.ytimg.com/vi/recvideo01/hqdefault.jpg");
  expect(img).toHaveAttribute("referrerpolicy", "no-referrer");
  fireEvent.error(img);
  expect(row.querySelector("img")).toBeNull();
  expect(row.querySelector(".cover")).toHaveTextContent(/\S/);
});

test("home has no recommendations section when the server switched them off", async () => {
  renderWithApp(<HomePage />, { routes: { ...homeRoutes, "GET /api/v1/me/recommendations": () => recs([], { enabled: false }) } });
  await screen.findByText("最近添加");
  await act(async () => {});
  expect(screen.queryByRole("heading", { name: "为你推荐" })).toBeNull();
});

describe("downloads", () => {
  beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
  afterEach(() => vi.useRealTimers());

  test("下载 shows the live status, then ▶ 播放 plays the downloaded song", async () => {
    let n = 0;
    let posted = false;
    const lists = [[job()], [job({ status: "downloading", progress: 40 })], [job({ status: "done", progress: 100, track_id: 7 })]];
    const post = vi.fn((init: RequestInit) => {
      expect(JSON.parse(String(init.body)).video.id).toBe("recvideo01");
      posted = true;
      return { status: 201, body: { jobs: [job()] } };
    });
    const { audio } = renderWithApp(<HomePage />, {
      routes: {
        ...homeRoutes,
        "GET /api/v1/me/recommendations": () => recs([rec(1)]),
        "POST /api/v1/downloads": post,
        // Home's own "downloading" chip reads the list before anything was posted.
        "GET /api/v1/downloads": () => ({ body: posted ? lists[Math.min(n++, lists.length - 1)] : [] }),
        "GET /api/v1/tracks/7": () => ({ body: track(7) }),
      },
    });
    const row = (await screen.findByText("推荐歌曲1")).closest("li")!;
    await userEvent.click(within(row).getByRole("button", { name: "下载" }));
    expect(post).toHaveBeenCalled();
    expect(await within(row).findByText("排队中")).toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(await within(row).findByText("下载中 40%")).toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    await userEvent.click(await within(row).findByRole("button", { name: /播放/ }));
    await vi.waitFor(() => expect(audio.src).toContain("/tracks/7/stream"));
  });
});

test("不感兴趣 removes the row at once and tells the server", async () => {
  const dismiss = vi.fn(() => ({ status: 204 }));
  renderWithApp(<RecommendationsPage />, {
    routes: { "GET /api/v1/me/recommendations": () => recs([rec(1), rec(2)]), "PUT /api/v1/me/recommendations/recvideo01/dismiss": dismiss, "GET /api/v1/downloads": () => ({ body: [] }) },
  });
  const row = (await screen.findByText("推荐歌曲1")).closest("li")!;
  await userEvent.click(within(row).getByRole("button", { name: "不感兴趣" }));
  expect(screen.queryByText("推荐歌曲1")).toBeNull();
  expect(screen.getByText("推荐歌曲2")).toBeInTheDocument();
  await vi.waitFor(() => expect(dismiss).toHaveBeenCalled());
});

test("a failed 不感兴趣 puts the row back with an error", async () => {
  renderWithApp(<RecommendationsPage />, {
    routes: {
      "GET /api/v1/me/recommendations": () => recs([rec(1), rec(2)]),
      "PUT /api/v1/me/recommendations/recvideo01/dismiss": () => ({ status: 500, body: { error: "boom", code: "server_error" } }),
      "GET /api/v1/downloads": () => ({ body: [] }),
    },
  });
  const row = (await screen.findByText("推荐歌曲1")).closest("li")!;
  await userEvent.click(within(row).getByRole("button", { name: "不感兴趣" }));
  expect(await screen.findByText("推荐歌曲1")).toBeInTheDocument();
  expect(await screen.findByText("服务器出错了")).toBeInTheDocument();
});

describe("refresh", () => {
  beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
  afterEach(() => {
    vi.useRealTimers();
    delete (document as { hidden?: boolean }).hidden; // back to jsdom's own getter
  });

  test("刷新 shows the refreshing state and polls until the new list arrives", async () => {
    let state: { items: Recommendation[]; refreshing: boolean } = { items: [rec(1)], refreshing: false };
    const get = vi.fn(() => recs(state.items, { refreshing: state.refreshing }));
    const post = vi.fn(() => {
      state = { items: [rec(1)], refreshing: true };
      return { status: 202 };
    });
    renderWithApp(<RecommendationsPage />, {
      routes: { "GET /api/v1/me/recommendations": get, "POST /api/v1/me/recommendations/refresh": post, "GET /api/v1/downloads": () => ({ body: [] }) },
    });
    await screen.findByText("推荐歌曲1");
    await userEvent.click(screen.getByRole("button", { name: "刷新" }));
    expect(await screen.findByRole("button", { name: "正在刷新…" })).toBeDisabled();
    const calls = get.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(get.mock.calls.length).toBeGreaterThan(calls);
    state = { items: [rec(3)], refreshing: false };
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(await screen.findByText("推荐歌曲3")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "刷新" })).toBeEnabled();
    const done = get.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
    expect(get.mock.calls.length).toBe(done); // polling stopped
  });

  test("no polling while the page is hidden; it resumes when visible", async () => {
    const get = vi.fn(() => recs([rec(1)], { refreshing: true }));
    let hidden = true;
    Object.defineProperty(document, "hidden", { configurable: true, get: () => hidden });
    renderWithApp(<RecommendationsPage />, { routes: { "GET /api/v1/me/recommendations": get, "GET /api/v1/downloads": () => ({ body: [] }) } });
    await screen.findByText("推荐歌曲1");
    const calls = get.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
    expect(get.mock.calls.length).toBe(calls);
    hidden = false;
    act(() => { document.dispatchEvent(new Event("visibilitychange")); });
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(get.mock.calls.length).toBeGreaterThan(calls);
  });

  test("刷新 has no time limit: tapping again joins the running refresh, with no message", async () => {
    let refreshing = false;
    const post = vi.fn(() => {
      refreshing = true;
      return { status: 202 };
    });
    renderWithApp(<RecommendationsPage />, {
      routes: {
        "GET /api/v1/me/recommendations": () => recs([rec(1)], { refreshing }),
        "POST /api/v1/me/recommendations/refresh": post,
        "GET /api/v1/downloads": () => ({ body: [] }),
      },
    });
    await screen.findByText("推荐歌曲1");
    await userEvent.click(screen.getByRole("button", { name: "刷新" }));
    expect(await screen.findByRole("button", { name: "正在刷新…" })).toBeDisabled();
    expect(post).toHaveBeenCalledTimes(1);
    expect(document.querySelector(".error")).toBeNull();
  });
});

test("the page explains the empty state", async () => {
  renderWithApp(<RecommendationsPage />, { routes: { "GET /api/v1/me/recommendations": () => recs([]), "GET /api/v1/downloads": () => ({ body: [] }) } });
  expect(await screen.findByText("播放或收藏几首歌后，这里会出现为你挑选的新歌。每晚自动更新。")).toBeInTheDocument();
});
