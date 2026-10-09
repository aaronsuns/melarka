import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import HomePage from "./HomePage";
import SearchPage from "./SearchPage";
import { renderWithApp } from "../test/render";
import type { Track } from "../api/types";

const tr = (id: number, extra: Partial<Track> = {}) =>
  ({ id, title: `歌${id}`, artist: "邓丽君", album: "精选", album_id: 3, duration_ms: 1000, codec: "mp3", bitrate: 320, lossless: false, status: "kept", ...extra }) as Track;

const page = (items: Track[]) => ({ body: { items, next_cursor: "" } });

test("home shows pending and recent sections", async () => {
  renderWithApp(<HomePage />, {
    routes: {
      "GET /api/v1/tracks": (_i, url) => (url.includes("status=pending") ? page([tr(9, { status: "pending" })]) : page([tr(1), tr(2)])),
    },
  });
  expect(await screen.findByText("待定新歌")).toBeInTheDocument();
  expect(await screen.findByText("歌9")).toBeInTheDocument();
  expect(screen.getByText("最近添加")).toBeInTheDocument();
  expect(await screen.findByText("歌2")).toBeInTheDocument();
});

test("home hides the pending section when there are none", async () => {
  renderWithApp(<HomePage />, { routes: { "GET /api/v1/tracks": () => page([tr(1)]) } });
  await screen.findByText("歌1");
  expect(screen.queryByText("待定新歌")).toBeNull();
});

// A failed fetch shows an error instead of an empty page.
test("home shows an error line when its lists fail to load", async () => {
  renderWithApp(<HomePage />, { routes: { "GET /api/v1/tracks": () => ({ status: 500, body: { error: "数据库忙" } }) } });
  expect(await screen.findByText("加载失败：数据库忙")).toBeInTheDocument();
});

// The radio tap primes the element before awaiting the fetch.
test("personal radio primes the audio element synchronously in the tap", async () => {
  const { audio } = renderWithApp(<HomePage />, {
    routes: { "GET /api/v1/tracks": () => page([]), "GET /api/v1/radio/next": () => ({ body: [] }) },
  });
  const btn = await screen.findByRole("button", { name: /私人电台/ });
  act(() => btn.click());
  expect(audio.play).toHaveBeenCalledTimes(1); // before the radio fetch has resolved
  expect(await screen.findByText("先收藏几首歌，电台会更懂你")).toBeInTheDocument();
});

test("personal radio plays what the server suggests", async () => {
  const { audio } = renderWithApp(<HomePage />, {
    routes: { "GET /api/v1/tracks": () => page([]), "GET /api/v1/radio/next": () => ({ body: [tr(5), tr(6)] }) },
  });
  await userEvent.click(await screen.findByRole("button", { name: /私人电台/ }));
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/5/stream"));
});

// The 随机播放 button shuffles the whole library.
test("home shuffle button primes synchronously and plays random tracks", async () => {
  const { audio } = renderWithApp(<HomePage />, {
    routes: { "GET /api/v1/tracks": () => page([]), "GET /api/v1/tracks/random": () => ({ body: [tr(5), tr(6)] }) },
  });
  const btn = await screen.findByRole("button", { name: /^🔀 随机播放(?!收藏)/ });
  act(() => btn.click());
  expect(audio.play).toHaveBeenCalledTimes(1); // primed before the fetch resolved
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/5/stream"));
});

test("home shuffle button shows an inline error on failure", async () => {
  renderWithApp(<HomePage />, {
    routes: { "GET /api/v1/tracks": () => page([]), "GET /api/v1/tracks/random": () => ({ status: 500, body: { error: "随机播放失败" } }) },
  });
  await userEvent.click(await screen.findByRole("button", { name: /^🔀 随机播放(?!收藏)/ }));
  expect(await screen.findByText("随机播放失败")).toBeInTheDocument();
});

test("search debounces and renders artists, albums and tracks", async () => {
  const search = vi.fn(() => ({
    body: { tracks: [tr(1, { title: "甜蜜蜜" })], albums: [{ id: 3, name: "精选", artist: "邓丽君", artist_id: 4, year: null, track_count: 10, library_id: 1 }], artists: [{ id: 4, name: "邓丽君", track_count: 99 }] },
  }));
  renderWithApp(<SearchPage />, { routes: { "GET /api/v1/search": search } });
  await userEvent.type(screen.getByRole("searchbox"), "dlj");
  expect(await screen.findByText("甜蜜蜜")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "邓丽君" })).toHaveAttribute("href", "/artists/4");
  expect(screen.getByRole("link", { name: /^精选/ })).toHaveAttribute("href", "/albums/3");
  expect(search.mock.calls.length).toBeLessThanOrEqual(2); // debounced, not one call per keystroke
});

test("search shows an empty state", async () => {
  renderWithApp(<SearchPage />, {
    routes: {
      "GET /api/v1/search": () => ({ body: { tracks: [], albums: [], artists: [] } }),
      "GET /api/v1/youtube/search": () => ({ body: { videos: [], playlists: [] } }),
    },
  });
  await userEvent.type(screen.getByRole("searchbox"), "zzz");
  expect(await screen.findByText("没有找到「zzz」")).toBeInTheDocument();
});
