import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router";
import { afterEach, expect, test, vi } from "vitest";
import type { ChannelSuggestions, Episode, PreviewInfo, Recommendation } from "../api/types";
import { usePreviews, type PreviewTarget } from "../channels/PreviewProvider";
import { RecommendationList } from "../components/RecommendationList";
import { claimSession, ownsSession, resetSessionOwner } from "../player/sessionOwner";
import { renderWithApp } from "../test/render";
import type { FakeAudio } from "../test/setup";
import ChannelPage from "./channels/ChannelPage";
import ChannelsPage from "./channels/ChannelsPage";
import EpisodePage from "./channels/EpisodePage";

afterEach(() => {
  resetSessionOwner();
  vi.useRealTimers();
  delete (document as unknown as { visibilityState?: string }).visibilityState;
});

const sugg = (extra: Partial<ChannelSuggestions> = {}): ChannelSuggestions => ({
  channels: [{ id: "UCyyyyyyyyyyyyyyyyyyyyyy", title: "推荐频道", avatar: "", score: 2, sample_video_id: "sugvideo001" }],
  videos: [{ video_id: "sugvideo001", title: "推荐视频", channel: "推荐频道", channel_id: "UCyyyyyyyyyyyyyyyyyyyyyy", duration_s: 900,
    thumbnail: "https://i.ytimg.com/vi/sugvideo001/hqdefault.jpg", url: "https://www.youtube.com/watch?v=sugvideo001", score: 1 }],
  refreshed_at: 1, refreshing: false, ...extra,
});
const pv = (extra: Partial<PreviewInfo> = {}): PreviewInfo => ({
  id: 1, video_id: "sugvideo001", media: "audio", status: "downloading", title: "推荐视频", channel: "推荐频道", channel_id: "UCyyyyyyyyyyyyyyyyyyyyyy",
  duration_s: 900, error: "", progress: 0, queued: false, merged: false, description: "", stream_url: "/api/v1/previews/1/stream", ...extra,
});
const body = (f: { mock: { calls: unknown[][] } }, suffix: string) => {
  const call = f.mock.calls.find(([u]) => String(u).endsWith(suffix))!;
  return JSON.parse((call[1] as RequestInit).body as string);
};
const posts = (f: { mock: { calls: unknown[][] } }, suffix: string) =>
  f.mock.calls.filter(([u, i]) => String(u).endsWith(suffix) && (i as RequestInit | undefined)?.method === "POST").map(([, i]) => JSON.parse((i as RequestInit).body as string));
const ep = (n: number, extra: Partial<Episode> = {}): Episode => ({
  video_id: `episode000${n}`, channel_id: "UC0e5c4U67Vm6sAVK0vxN3Uw", channel_title: "刘翔的投资频道", title: `第${n}集`, published_at: 1, duration_s: 600,
  kind: "video", thumbnail: "", audio: null, video: null, position_s: 0, played: false, kept: false, ...extra,
});
const done = { status: "done" as const, progress: 100, bytes: 1, error: "" };

function stubMediaSession() {
  const ms = { setActionHandler: vi.fn(), setPositionState: vi.fn(), metadata: null as unknown, playbackState: "none" };
  Object.defineProperty(navigator, "mediaSession", { value: ms, configurable: true, writable: true });
  vi.stubGlobal("MediaMetadata", class { constructor(public init: MediaMetadataInit) {} });
  const handler = (a: string) => [...ms.setActionHandler.mock.calls].reverse().find(([n]) => n === a)?.[1] as (() => void) | null | undefined;
  return { ms, handler };
}

test("推荐 lists suggested channels with why, follow and 不感兴趣; refresh at the daily limit says so", async () => {
  const user = userEvent.setup();
  const { f } = renderWithApp(<ChannelsPage />, {
    path: "/channels?tab=suggested",
    routes: {
      "GET /api/v1/me/channel-suggestions": () => ({ body: sugg() }),
      "PUT /api/v1/me/channel-suggestions/channels/UCyyyyyyyyyyyyyyyyyyyyyy/dismiss": () => ({ status: 204 }),
      "POST /api/v1/me/channel-suggestions/refresh": () => ({ status: 429, body: { error: "x", code: "too_soon" } }),
    },
  });
  expect(screen.getByRole("tab", { name: "推荐" })).toHaveAttribute("aria-selected", "true");
  const row = (await screen.findByText("推荐频道", { selector: ".suggested-channel .ellipsis" })).closest("li")!;
  expect(within(row).getByText("来自你的 2 个频道")).toBeInTheDocument();
  expect(within(row).getByRole("button", { name: "关注" })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "刷新" }));
  // too_soon means today's budget is used up.
  expect(await screen.findByText("今天刷新推荐的次数已达上限，请明天再试")).toBeInTheDocument();
  await user.click(within(row).getByRole("button", { name: "不感兴趣" }));
  await waitFor(() => expect(screen.queryByText("推荐频道", { selector: ".suggested-channel .ellipsis" })).toBeNull());
  expect(f.mock.calls.some(([u, i]) => String(u).endsWith("/channels/UCyyyyyyyyyyyyyyyyyyyyyy/dismiss") && (i as RequestInit).method === "PUT")).toBe(true);
});

test("刷新 joins or starts a refresh: 正在刷新 until it finishes, asked again only while the page is visible", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  let refreshing = false;
  const { f } = renderWithApp(<ChannelsPage />, {
    path: "/channels?tab=suggested",
    routes: {
      "GET /api/v1/me/channel-suggestions": () => ({ body: sugg({ refreshing }) }),
      "POST /api/v1/me/channel-suggestions/refresh": () => {
        refreshing = true;
        return { status: 202 };
      },
    },
  });
  const gets = () => f.mock.calls.filter(([u]) => String(u).endsWith("/me/channel-suggestions")).length;
  fireEvent.click(await screen.findByRole("button", { name: "刷新" }));
  const busy = await screen.findByRole("button", { name: "正在刷新推荐…" });
  expect(busy).toBeDisabled();
  const before = gets();
  Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
  fireEvent(document, new Event("visibilitychange"));
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(gets()).toBe(before);
  refreshing = false;
  Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
  fireEvent(document, new Event("visibilitychange"));
  expect(await screen.findByRole("button", { name: "刷新" })).toBeEnabled();
  const after = gets();
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(gets()).toBe(after); // nothing running: no more polling
});

test("▶ 试听 on a suggested video previews it and 保留到频道 keeps it in Channels", async () => {
  const user = userEvent.setup();
  const { f, audio, previewAudio } = renderWithApp(<ChannelsPage />, {
    path: "/channels?tab=suggested",
    routes: {
      "GET /api/v1/me/channel-suggestions": () => ({ body: sugg() }),
      "POST /api/v1/previews": () => ({ status: 201, body: pv() }),
      "GET /api/v1/previews/1": () => ({ body: pv({ status: "done" }) }),
      "POST /api/v1/previews/1/keep": () => ({ body: { episode_id: "sugvideo001" } }),
    },
  });
  const row = (await screen.findByText("推荐视频")).closest("li")!;
  const button = within(row).getByRole("button", { name: "试听" });
  expect(button.closest("[data-no-music-prime]")).not.toBeNull();
  fireEvent.click(button);
  expect(ownsSession("preview")).toBe(true);
  expect(audio.play).not.toHaveBeenCalled();
  const sheet = await screen.findByRole("dialog", { name: "推荐视频" });
  expect(sheet).toHaveAttribute("data-no-music-prime");
  await waitFor(() => expect(previewAudio.src).toContain("/api/v1/previews/1/stream"));
  expect(body(f, "/api/v1/previews")).toEqual({ video_id: "sugvideo001", media: "audio", title: "推荐视频", channel: "推荐频道", duration_s: 900 });
  const keep = await within(sheet).findByRole("button", { name: "保留到频道" });
  await waitFor(() => expect(keep).toBeEnabled());
  await user.click(keep);
  expect(await within(sheet).findByText("已保留到频道")).toBeInTheDocument();
  expect(body(f, "/previews/1/keep")).toEqual({ to: "channel" });
  expect(previewAudio.pause).toHaveBeenCalled();
});

test("下载 on a suggested video keeps it in Channels without playing it", async () => {
  const user = userEvent.setup();
  const { f, previewAudio } = renderWithApp(<ChannelsPage />, {
    path: "/channels?tab=suggested",
    routes: {
      "GET /api/v1/me/channel-suggestions": () => ({ body: sugg() }),
      "POST /api/v1/previews": () => ({ status: 201, body: pv() }),
      "GET /api/v1/previews/1": () => ({ body: pv({ status: "done" }) }),
      "POST /api/v1/previews/1/keep": () => ({ body: { episode_id: "sugvideo001" } }),
    },
  });
  const row = (await screen.findByText("推荐视频")).closest("li")!;
  await user.click(within(row).getByRole("button", { name: "下载" }));
  expect(await within(row).findByText("已保留到频道")).toBeInTheDocument();
  expect(body(f, "/previews/1/keep")).toEqual({ to: "channel" });
  expect(previewAudio.play).not.toHaveBeenCalled();
  expect(ownsSession("music")).toBe(true);
});

test("▶ 试听 on a music recommendation keeps into music, then ▶ 播放 plays the new song", async () => {
  const user = userEvent.setup();
  const rec: Recommendation = { video_id: "recvideo001", title: "推荐歌曲", channel: "歌手", duration_s: 200, thumbnail: "",
    url: "https://www.youtube.com/watch?v=recvideo001", reason: null, score: 1 };
  const { f } = renderWithApp(<RecommendationList items={[rec]} onChange={() => {}} />, {
    routes: {
      "GET /api/v1/downloads": () => ({ body: [] }),
      "POST /api/v1/previews": () => ({ status: 201, body: pv({ id: 2, video_id: "recvideo001", title: "推荐歌曲", stream_url: "/api/v1/previews/2/stream", status: "done" }) }),
      "POST /api/v1/previews/2/keep": () => ({ body: { job: { id: 7, track_id: 42, status: "done" } } }),
      "GET /api/v1/tracks/42": () => ({ body: { id: 42, title: "推荐歌曲", artist: "歌手", album: "", duration_ms: 200000 } }),
    },
  });
  fireEvent.click(screen.getByRole("button", { name: "试听" }));
  const sheet = await screen.findByRole("dialog", { name: "推荐歌曲" });
  await user.click(await within(sheet).findByRole("button", { name: "保留到音乐" }));
  expect(await within(sheet).findByText("已保留——在“我的下载”里")).toBeInTheDocument();
  expect(body(f, "/previews/2/keep")).toEqual({ to: "music" });
  expect(within(sheet).getByRole("button", { name: /播放/ })).toBeInTheDocument();
});

test("a channel page offers ▶ 试听 on episodes that aren't on disk; the episode page previews video", async () => {
  const { f } = renderWithApp(
    <Routes>
      <Route path="/channels/:id" element={<ChannelPage />} />
      <Route path="/episodes/:id" element={<EpisodePage />} />
    </Routes>,
    {
      path: "/channels/UC0e5c4U67Vm6sAVK0vxN3Uw",
      routes: {
        "GET /api/v1/channels/UC0e5c4U67Vm6sAVK0vxN3Uw": () => ({ body: { channel: { id: "UC0e5c4U67Vm6sAVK0vxN3Uw", title: "刘翔的投资频道", handle: "", avatar: "", description: "", polled_at: 1, last_error: "" },
          following: null, followers: 0, episodes: [ep(1), ep(2, { audio: done })] } }),
        "GET /api/v1/episodes/episode0002": () => ({ body: ep(2, { audio: done }) }),
        "POST /api/v1/previews": () => ({ status: 201, body: pv({ media: "video", video_id: "episode0002", title: "第2集" }) }),
        "GET /api/v1/previews/1": () => ({ body: pv({ media: "video", status: "done" }) }),
      },
    },
  );
  const row1 = (await screen.findByText("第1集")).closest("li")!;
  expect(within(row1).getByRole("button", { name: "▶ 试听" })).toBeInTheDocument();
  const row2 = screen.getByText("第2集").closest("li")!;
  expect(within(row2).queryByRole("button", { name: "▶ 试听" })).toBeNull();
  await act(async () => within(row2).getAllByRole("link")[0].click());
  fireEvent.click(await screen.findByRole("button", { name: "▶ 看（预览）" }));
  await screen.findByRole("dialog", { name: "第2集" });
  expect(body(f, "/api/v1/previews")).toMatchObject({ video_id: "episode0002", media: "video" });
  await waitFor(() => expect(document.querySelector(".preview-sheet video")?.getAttribute("src")).toBe("/api/v1/previews/1/stream"));
});

// A merged 360p (YouTube refused format 18) can't stream while it downloads:
// the sheet says 准备中 N% until it is complete, then shows the player.
test("▶ 看（预览） of a merged video says 准备中 N% until it is complete, then plays", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  let state: Partial<PreviewInfo> = { status: "downloading", merged: true, progress: 30 };
  renderWithApp(<Routes><Route path="/episodes/:id" element={<EpisodePage />} /></Routes>, {
    path: "/episodes/episode0001",
    routes: {
      "GET /api/v1/episodes/episode0001": () => ({ body: ep(1) }),
      "POST /api/v1/previews": () => ({ status: 201, body: pv({ media: "video", video_id: "episode0001", title: "第1集" }) }),
      "GET /api/v1/previews/1": () => ({ body: pv({ media: "video", video_id: "episode0001", title: "第1集", ...state }) }),
    },
  });
  fireEvent.click(await screen.findByRole("button", { name: "▶ 看（预览）" }));
  const sheet = await screen.findByRole("dialog", { name: "第1集" });
  expect(await within(sheet).findByText("准备中 30%")).toBeInTheDocument();
  expect(sheet.querySelector("video")).toBeNull();
  expect(within(sheet).queryByRole("alert")).toBeNull();
  state = { status: "done" };
  await act(() => vi.advanceTimersByTimeAsync(2000));
  await waitFor(() => expect(sheet.querySelector("video")?.getAttribute("src")).toBe("/api/v1/previews/1/stream"));
  expect(within(sheet).queryByText(/准备中/)).toBeNull();
});

test("an episode not on disk offers ▶ 试听 (audio) next to ▶ 看（预览）", async () => {
  renderWithApp(<Routes><Route path="/episodes/:id" element={<EpisodePage />} /></Routes>, {
    path: "/episodes/episode0001",
    routes: { "GET /api/v1/episodes/episode0001": () => ({ body: ep(1) }) },
  });
  expect(await screen.findByRole("button", { name: "▶ 试听" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "▶ 看（预览）" })).toBeInTheDocument();
});

test("a video that can't be watched while it downloads offers the audio preview instead", async () => {
  const { f } = renderWithApp(<Routes><Route path="/episodes/:id" element={<EpisodePage />} /></Routes>, {
    path: "/episodes/episode0001",
    routes: {
      "GET /api/v1/episodes/episode0001": () => ({ body: ep(1) }),
      "POST /api/v1/previews": (init) => {
        const b = JSON.parse(init.body as string);
        return b.media === "video"
          ? { status: 409, body: { error: "x", code: "video_preview_unavailable" } }
          : { status: 201, body: pv({ id: 3, video_id: "episode0001", title: "第1集", stream_url: "/api/v1/previews/3/stream" }) };
      },
      "GET /api/v1/previews/3": () => ({ body: pv({ id: 3, status: "done" }) }),
    },
  });
  fireEvent.click(await screen.findByRole("button", { name: "▶ 看（预览）" }));
  const sheet = await screen.findByRole("dialog", { name: "第1集" });
  expect(await within(sheet).findByText("这个视频不能边下边看，请改用试听音频")).toBeInTheDocument();
  fireEvent.click(within(sheet).getByRole("button", { name: "▶ 试听" }));
  await waitFor(() => expect(posts(f, "/api/v1/previews").map((b) => b.media)).toEqual(["video", "audio"]));
  expect(await within(screen.getByRole("dialog", { name: "第1集" })).findByRole("button", { name: "保留到频道" })).toBeInTheDocument();
});

test("a failed video preview found while polling also offers the audio preview", async () => {
  renderWithApp(<Routes><Route path="/episodes/:id" element={<EpisodePage />} /></Routes>, {
    path: "/episodes/episode0001",
    routes: {
      "GET /api/v1/episodes/episode0001": () => ({ body: ep(1) }),
      "POST /api/v1/previews": () => ({ status: 201, body: pv({ media: "video", video_id: "episode0001", title: "第1集" }) }),
      "GET /api/v1/previews/1": () => ({ body: pv({ media: "video", status: "failed", error: "video_preview_unavailable" }) }),
    },
  });
  fireEvent.click(await screen.findByRole("button", { name: "▶ 看（预览）" }));
  const sheet = await screen.findByRole("dialog", { name: "第1集" });
  expect(await within(sheet).findByText("这个视频不能边下边看，请改用试听音频")).toBeInTheDocument();
  expect(within(sheet).getByRole("button", { name: "▶ 试听" })).toBeInTheDocument();
  expect(sheet.querySelector("video")).toBeNull();
});

test("YouTube pushing back (503 preview_retry) says when to try again, and 重试 asks again", async () => {
  let n = 0;
  const { f } = renderWithApp(<ChannelsPage />, {
    path: "/channels?tab=suggested",
    routes: {
      "GET /api/v1/me/channel-suggestions": () => ({ body: sugg() }),
      "POST /api/v1/previews": () =>
        ++n === 1 ? { status: 503, body: { error: "x", code: "preview_retry" }, headers: { "Retry-After": "30" } } : { status: 201, body: pv() },
      "GET /api/v1/previews/1": () => ({ body: pv() }),
    },
  });
  fireEvent.click(within((await screen.findByText("推荐视频")).closest("li")!).getByRole("button", { name: "试听" }));
  const sheet = await screen.findByRole("dialog", { name: "推荐视频" });
  expect(await within(sheet).findByText("YouTube 暂时拒绝了请求，请 30 秒后再试")).toBeInTheDocument();
  fireEvent.click(within(sheet).getByRole("button", { name: "重试" }));
  await waitFor(() => expect(posts(f, "/api/v1/previews")).toHaveLength(2));
  await waitFor(() => expect(within(sheet).queryByText(/秒后再试/)).toBeNull());
});

test("no space (507) and too long (409) are shown in words", async () => {
  for (const [status, code, text] of [
    [507, "preview_no_space", "服务器磁盘空间不足，无法试听"],
    [409, "preview_too_long", "这个视频下载太慢，无法试听"],
  ] as const) {
    renderWithApp(<ChannelsPage />, {
      path: "/channels?tab=suggested",
      routes: {
        "GET /api/v1/me/channel-suggestions": () => ({ body: sugg() }),
        "POST /api/v1/previews": () => ({ status, body: { error: "x", code } }),
      },
    });
    fireEvent.click(within((await screen.findByText("推荐视频")).closest("li")!).getByRole("button", { name: "试听" }));
    expect(await within(await screen.findByRole("dialog", { name: "推荐视频" })).findByText(text)).toBeInTheDocument();
    cleanup();
    resetSessionOwner();
  }
});

test("the preview owns the lock screen while it plays, pauses for music, and gives the session back on close", async () => {
  const { ms, handler } = stubMediaSession();
  const user = userEvent.setup();
  const { previewAudio } = renderWithApp(<ChannelsPage />, {
    path: "/channels?tab=suggested",
    routes: {
      "GET /api/v1/me/channel-suggestions": () => ({ body: sugg() }),
      "POST /api/v1/previews": () => ({ status: 201, body: pv({ status: "done" }) }),
    },
  });
  fireEvent.click(within((await screen.findByText("推荐视频")).closest("li")!).getByRole("button", { name: "试听" }));
  await waitFor(() => expect(previewAudio.src).toContain("/api/v1/previews/1/stream"));
  await waitFor(() => expect((ms.metadata as { init: MediaMetadataInit } | null)?.init.title).toBe("推荐视频"));
  expect((ms.metadata as { init: MediaMetadataInit }).init.artist).toBe("推荐频道");
  expect(handler("nexttrack")).toBeNull();
  const previewPause = handler("pause")!;
  previewAudio.pause.mockClear();
  act(() => previewPause());
  expect(previewAudio.pause).toHaveBeenCalled();
  act(() => handler("play")!());
  expect(previewAudio.paused).toBe(false);
  // Music (or an episode) taking over pauses the preview.
  act(() => claimSession("music"));
  expect(previewAudio.paused).toBe(true);
  act(() => claimSession("preview"));
  await user.click(screen.getByRole("button", { name: "关闭试听" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(ownsSession("music")).toBe(true);
  expect(ms.metadata).toBeNull();
  // The preview's handlers are gone (music registers its own when it owns the session).
  expect(handler("pause")).not.toBe(previewPause);
});

test("closing a preview that no longer owns the session leaves the owner alone", async () => {
  const user = userEvent.setup();
  renderWithApp(<ChannelsPage />, {
    path: "/channels?tab=suggested",
    routes: {
      "GET /api/v1/me/channel-suggestions": () => ({ body: sugg() }),
      "POST /api/v1/previews": () => ({ status: 201, body: pv({ status: "done" }) }),
    },
  });
  fireEvent.click(within((await screen.findByText("推荐视频")).closest("li")!).getByRole("button", { name: "试听" }));
  await screen.findByRole("dialog", { name: "推荐视频" });
  act(() => claimSession("episode"));
  await user.click(screen.getByRole("button", { name: "关闭试听" }));
  expect(ownsSession("episode")).toBe(true);
});

test("unmounting while a preview owns the session stops it and gives the session back", async () => {
  const { previewAudio } = renderWithApp(<ChannelsPage />, {
    path: "/channels?tab=suggested",
    routes: {
      "GET /api/v1/me/channel-suggestions": () => ({ body: sugg() }),
      "POST /api/v1/previews": () => ({ status: 201, body: pv({ status: "done" }) }),
    },
  });
  fireEvent.click(within((await screen.findByText("推荐视频")).closest("li")!).getByRole("button", { name: "试听" }));
  await waitFor(() => expect(previewAudio.src).toContain("/stream"));
  cleanup();
  expect(previewAudio.paused).toBe(true);
  expect(previewAudio.src).toBe("");
  expect(ownsSession("music")).toBe(true);
});

test("the preview's video claims the session when it plays and pauses when music takes over", async () => {
  renderWithApp(<Routes><Route path="/episodes/:id" element={<EpisodePage />} /></Routes>, {
    path: "/episodes/episode0002",
    routes: {
      "GET /api/v1/episodes/episode0002": () => ({ body: ep(2, { audio: done }) }),
      "POST /api/v1/previews": () => ({ status: 201, body: pv({ media: "video", video_id: "episode0002", title: "第2集", status: "done" }) }),
    },
  });
  fireEvent.click(await screen.findByRole("button", { name: "▶ 看（预览）" }));
  expect(ownsSession("preview")).toBe(true);
  await waitFor(() => expect(document.querySelector(".preview-sheet video")).not.toBeNull());
  const video = document.querySelector(".preview-sheet video") as HTMLVideoElement;
  act(() => claimSession("music"));
  const pause = vi.spyOn(video, "pause");
  act(() => claimSession("episode"));
  expect(pause).toHaveBeenCalled();
  fireEvent.play(video);
  expect(ownsSession("preview")).toBe(true);
});

test("an episode already on disk whose video can't be previewed offers ▶ 听 instead of an audio preview", async () => {
  const { f, episodeAudio } = renderWithApp(<Routes><Route path="/episodes/:id" element={<EpisodePage />} /></Routes>, {
    path: "/episodes/episode0002",
    routes: {
      "GET /api/v1/episodes/episode0002": () => ({ body: ep(2, { audio: done }) }),
      "POST /api/v1/previews": () => ({ status: 409, body: { error: "x", code: "video_preview_unavailable" } }),
    },
  });
  fireEvent.click(await screen.findByRole("button", { name: "▶ 看（预览）" }));
  const sheet = await screen.findByRole("dialog", { name: "第2集" });
  expect(await within(sheet).findByText("这个视频不能边下边看，请改用试听音频")).toBeInTheDocument();
  expect(within(sheet).queryByRole("button", { name: "▶ 试听" })).toBeNull();
  fireEvent.click(within(sheet).getByRole("button", { name: "▶ 听" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(episodeAudio.src).toContain("/api/v1/episodes/episode0002/stream");
  expect(episodeAudio.play).toHaveBeenCalled();
  expect(ownsSession("episode")).toBe(true);
  expect(posts(f, "/api/v1/previews")).toHaveLength(1);
});

// The suggested video's ▶ 试听, with the preview's status read from `status()`.
async function openSuggestedPreview(status: () => Partial<PreviewInfo>, start: Partial<PreviewInfo> = {}) {
  const r = renderWithApp(<ChannelsPage />, {
    path: "/channels?tab=suggested",
    routes: {
      "GET /api/v1/me/channel-suggestions": () => ({ body: sugg() }),
      "POST /api/v1/previews": () => ({ status: 201, body: pv(start) }),
      "GET /api/v1/previews/1": () => ({ body: pv(status()) }),
    },
  });
  fireEvent.click(within((await screen.findByText("推荐视频")).closest("li")!).getByRole("button", { name: "试听" }));
  await waitFor(() => expect(r.previewAudio.src).toContain("/api/v1/previews/1/stream"));
  const gets = () => r.f.mock.calls.filter(([u]) => String(u).endsWith("/api/v1/previews/1")).length;
  return { ...r, gets };
}

function breakStream(a: FakeAudio, at: number) {
  a.currentTime = at;
  act(() => a.fire("error"));
}

test("a stream that breaks while downloading is reloaded from where it was once the preview is done", async () => {
  let status: PreviewInfo["status"] = "downloading";
  const { previewAudio } = await openSuggestedPreview(() => ({ status }));
  previewAudio.play.mockClear();
  status = "done";
  breakStream(previewAudio, 42);
  await waitFor(() => expect(previewAudio.load).toHaveBeenCalled());
  expect(previewAudio.src).toContain("/api/v1/previews/1/stream");
  expect(previewAudio.play).toHaveBeenCalled();
  expect(previewAudio.paused).toBe(false);
  act(() => previewAudio.fire("loadedmetadata"));
  expect(previewAudio.currentTime).toBe(42);
  expect(screen.queryByRole("alert")).toBeNull();
});

test("a broken stream still downloading is reloaded after one more status read", async () => {
  const { previewAudio, gets } = await openSuggestedPreview(() => ({ status: "downloading" }));
  const before = gets();
  breakStream(previewAudio, 10);
  await waitFor(() => expect(gets()).toBe(before + 1));
  expect(previewAudio.load).not.toHaveBeenCalled();
  await waitFor(() => expect(previewAudio.load).toHaveBeenCalled(), { timeout: 4000 });
  expect(gets()).toBe(before + 2);
});

test("▶ on a broken stream reloads it at once", async () => {
  const user = userEvent.setup();
  const { previewAudio } = await openSuggestedPreview(() => ({ status: "downloading" }));
  breakStream(previewAudio, 5);
  const sheet = screen.getByRole("dialog", { name: "推荐视频" });
  await user.click(await within(sheet).findByRole("button", { name: "播放" }));
  expect(previewAudio.load).toHaveBeenCalled();
  expect(previewAudio.paused).toBe(false);
});

test("a stream that breaks again after the finished file was reloaded fails, with 重试", async () => {
  const { previewAudio, f } = await openSuggestedPreview(() => ({ status: "done" }), { status: "done" });
  breakStream(previewAudio, 3);
  await waitFor(() => expect(previewAudio.load).toHaveBeenCalled());
  breakStream(previewAudio, 3);
  const sheet = screen.getByRole("dialog", { name: "推荐视频" });
  expect(await within(sheet).findByText("无法加载试听")).toBeInTheDocument();
  expect(previewAudio.paused).toBe(true);
  fireEvent.click(within(sheet).getByRole("button", { name: "重试" }));
  await waitFor(() => expect(posts(f, "/api/v1/previews")).toHaveLength(2));
  await waitFor(() => expect(within(sheet).queryByText("无法加载试听")).toBeNull());
});

test("the preview video is reloaded when its stream breaks", async () => {
  renderWithApp(<Routes><Route path="/episodes/:id" element={<EpisodePage />} /></Routes>, {
    path: "/episodes/episode0001",
    routes: {
      "GET /api/v1/episodes/episode0001": () => ({ body: ep(1) }),
      "POST /api/v1/previews": () => ({ status: 201, body: pv({ media: "video", video_id: "episode0001", title: "第1集", status: "done" }) }),
      "GET /api/v1/previews/1": () => ({ body: pv({ media: "video", status: "done" }) }),
    },
  });
  fireEvent.click(await screen.findByRole("button", { name: "▶ 看（预览）" }));
  await waitFor(() => expect(document.querySelector(".preview-sheet video")).not.toBeNull());
  const video = document.querySelector(".preview-sheet video") as HTMLVideoElement;
  const load = vi.spyOn(video, "load");
  fireEvent.error(video);
  await waitFor(() => expect(load).toHaveBeenCalled());
  expect(video.getAttribute("src")).toBe("/api/v1/previews/1/stream");
});

test("the sheet's status reads stop while the page is hidden and catch up when it shows", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const { gets } = await openSuggestedPreview(() => ({ status: "downloading" }));
  await waitFor(() => expect(gets()).toBe(1));
  Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
  fireEvent(document, new Event("visibilitychange"));
  await act(() => vi.advanceTimersByTimeAsync(20_000));
  expect(gets()).toBe(1);
  Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
  fireEvent(document, new Event("visibilitychange"));
  await waitFor(() => expect(gets()).toBe(2));
  await act(() => vi.advanceTimersByTimeAsync(2_000));
  await waitFor(() => expect(gets()).toBe(3));
});

test("下载 asks again after a failure without a reason, and waits while the page is hidden", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  let n = 0;
  let reads = 0;
  const { f } = renderWithApp(<ChannelsPage />, {
    path: "/channels?tab=suggested",
    routes: {
      "GET /api/v1/me/channel-suggestions": () => ({ body: sugg() }),
      "POST /api/v1/previews": () => (++n === 1 ? { status: 502, body: { error: "Bad Gateway" } } : { status: 201, body: pv() }),
      "GET /api/v1/previews/1": () => ({ body: pv({ status: ++reads >= 3 ? "done" : "downloading" }) }),
      "POST /api/v1/previews/1/keep": () => ({ body: { episode_id: "sugvideo001" } }),
    },
  });
  const row = (await screen.findByText("推荐视频")).closest("li")!;
  fireEvent.click(within(row).getByRole("button", { name: "下载" }));
  await act(() => vi.advanceTimersByTimeAsync(2_000));
  await waitFor(() => expect(posts(f, "/api/v1/previews")).toHaveLength(2));
  await waitFor(() => expect(reads).toBe(1));
  Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
  fireEvent(document, new Event("visibilitychange"));
  await act(() => vi.advanceTimersByTimeAsync(20_000));
  expect(reads).toBe(1);
  Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
  fireEvent(document, new Event("visibilitychange"));
  await waitFor(() => expect(reads).toBe(2));
  await act(() => vi.advanceTimersByTimeAsync(2_000));
  expect(await within(row).findByText("已保留到频道")).toBeInTheDocument();
  expect(within(row).queryByRole("alert")).toBeNull();
});

test("下载 that fails for good says so in the row's text; the actions don't nest", async () => {
  const user = userEvent.setup();
  renderWithApp(<ChannelsPage />, {
    path: "/channels?tab=suggested",
    routes: {
      "GET /api/v1/me/channel-suggestions": () => ({ body: sugg() }),
      "POST /api/v1/previews": () => ({ status: 400, body: { error: "bad request" } }),
    },
  });
  const row = (await screen.findByText("推荐视频")).closest("li")!;
  expect(document.querySelector(".job-actions .job-actions")).toBeNull();
  await user.click(within(row).getByRole("button", { name: "下载" }));
  const alert = await within(row).findByRole("alert");
  expect(alert).toHaveTextContent("无法加载试听");
  expect(alert.closest(".yt-text")).not.toBeNull();
  expect(within(row).getByRole("button", { name: "下载" })).toBeEnabled();
});

test("不感兴趣 puts the row back where it was when the server refuses", async () => {
  const user = userEvent.setup();
  const two = sugg();
  two.videos.push({ ...two.videos[0], video_id: "sugvideo002", title: "第二个推荐视频" });
  renderWithApp(<ChannelsPage />, {
    path: "/channels?tab=suggested",
    routes: {
      "GET /api/v1/me/channel-suggestions": () => ({ body: two }),
      "PUT /api/v1/me/channel-suggestions/videos/sugvideo001/dismiss": () => ({ status: 500, body: { error: "boom" } }),
    },
  });
  const row = (await screen.findByText("推荐视频")).closest("li")!;
  await user.click(within(row).getByRole("button", { name: "不感兴趣" }));
  expect(await screen.findByText("boom")).toBeInTheDocument();
  const titles = [...document.querySelectorAll(".yt-results .yt-text > .row-title")].map((e) => e.textContent);
  expect(titles).toEqual(["推荐视频", "第二个推荐视频"]);
});

// 视频: a preview opened at a second starts there once its metadata is in.
function Opener({ target }: { target: PreviewTarget }) {
  const p = usePreviews();
  return <button data-no-music-prime="" onClick={() => p.open(target)}>open</button>;
}
const at30 = (media: "audio" | "video"): PreviewTarget => ({
  videoId: "sugvideo001", media, title: "推荐视频", channel: "推荐频道", durationS: 900, keepTo: "music", startAt: 30,
});

test("a preview opened with startAt seeks there once the audio's metadata is in", async () => {
  const { previewAudio } = renderWithApp(<Opener target={at30("audio")} />, {
    routes: { "POST /api/v1/previews": () => ({ status: 201, body: pv({ status: "done" }) }) },
  });
  fireEvent.click(screen.getByRole("button", { name: "open" }));
  // The silent unlock's own metadata comes first in a real browser: not the load to seek.
  expect(previewAudio.src.startsWith("data:")).toBe(true);
  act(() => previewAudio.fire("loadedmetadata"));
  await waitFor(() => expect(previewAudio.src).toContain("/api/v1/previews/1/stream"));
  expect(previewAudio.currentTime).toBe(0);
  act(() => previewAudio.fire("loadedmetadata"));
  expect(previewAudio.currentTime).toBe(30);
  // Only the first load: a later metadata event leaves the position alone.
  previewAudio.currentTime = 45;
  act(() => previewAudio.fire("loadedmetadata"));
  expect(previewAudio.currentTime).toBe(45);
});

test("a video preview opened with startAt seeks there too", async () => {
  renderWithApp(<Opener target={at30("video")} />, {
    routes: { "POST /api/v1/previews": () => ({ status: 201, body: pv({ status: "done", media: "video" }) }) },
  });
  fireEvent.click(screen.getByRole("button", { name: "open" }));
  await waitFor(() => expect(document.querySelector("video.preview-video")).not.toBeNull());
  const video = document.querySelector("video.preview-video") as HTMLVideoElement;
  fireEvent.loadedMetadata(video);
  expect(video.currentTime).toBe(30);
});
