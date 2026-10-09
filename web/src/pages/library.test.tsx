import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, Route, Routes } from "react-router";
import LibraryPage from "./LibraryPage";
import AlbumPage from "./AlbumPage";
import ArtistPage from "./ArtistPage";
import { renderWithApp } from "../test/render";
import type { Track } from "../api/types";

const tr = (id: number, extra: Partial<Track> = {}) =>
  ({ id, title: `歌${id}`, artist: "邓丽君", album: "精选", duration_ms: 1000, codec: "mp3", bitrate: 320, lossless: false, ...extra }) as Track;
const album = { id: 3, name: "精选", artist: "邓丽君", artist_id: 4, year: 1983, track_count: 2, library_id: 1 };

test("songs tab pages through the cursor", async () => {
  const tracks = vi.fn((_i: RequestInit, url: string) =>
    url.includes("cursor=100") ? { body: { items: [tr(3)], next_cursor: "" } } : { body: { items: [tr(1), tr(2)], next_cursor: "100" } });
  renderWithApp(<LibraryPage />, { path: "/library?tab=songs", routes: { "GET /api/v1/tracks": tracks } });
  expect(await screen.findByText("歌1")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "加载更多" }));
  expect(await screen.findByText("歌3")).toBeInTheDocument();
  expect(tracks.mock.calls[0][1]).toContain("sort=title");
});

// Task 5: the songs tab's 随机播放 button shuffles the whole library.
test("songs tab shuffle button plays random tracks", async () => {
  const { audio } = renderWithApp(<LibraryPage />, {
    path: "/library?tab=songs",
    routes: { "GET /api/v1/tracks": () => ({ body: { items: [], next_cursor: "" } }), "GET /api/v1/tracks/random": () => ({ body: [tr(4)] }) },
  });
  await userEvent.click(await screen.findByRole("button", { name: /^🔀 随机播放$/ }));
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/4/stream"));
});

test("favorites tab has no global shuffle button", async () => {
  renderWithApp(<LibraryPage />, { path: "/library?tab=favorites", routes: { "GET /api/v1/tracks": () => ({ body: { items: [], next_cursor: "" } }) } });
  await screen.findByRole("tablist");
  expect(screen.queryByRole("button", { name: /^🔀 随机播放$/ })).toBeNull();
});

test("favorites tab asks for favorite=1", async () => {
  const tracks = vi.fn(() => ({ body: { items: [tr(8, { favorite: true })], next_cursor: "" } }));
  renderWithApp(<LibraryPage />, { path: "/library?tab=favorites", routes: { "GET /api/v1/tracks": tracks } });
  expect(await screen.findByText("歌8")).toBeInTheDocument();
  expect((tracks.mock.calls[0] as unknown[])[1]).toContain("favorite=1");
});

test("Library opens on 收藏", async () => {
  const tracks = vi.fn(() => ({ body: { items: [tr(8, { favorite: true })], next_cursor: "" } }));
  renderWithApp(<LibraryPage />, { path: "/library", routes: { "GET /api/v1/tracks": tracks } });
  expect(await screen.findByText("歌8")).toBeInTheDocument();
  expect(screen.getByRole("tab", { name: "收藏" })).toHaveAttribute("aria-selected", "true");
  expect(screen.getAllByRole("tab")[0]).toHaveTextContent("收藏");
  expect((tracks.mock.calls[0] as unknown[])[1]).toContain("favorite=1");
});

test("albums tab shows album links", async () => {
  renderWithApp(<LibraryPage />, { path: "/library?tab=albums", routes: { "GET /api/v1/albums": () => ({ body: { items: [album], next_cursor: "" } }) } });
  const card = await screen.findByRole("link", { name: /^精选/ });
  expect(card).toHaveAttribute("href", "/albums/3");
  expect(card.querySelector(".cover img")).toHaveAttribute("src", "/api/v1/albums/3/cover?size=300");
});

test("album page plays all in order", async () => {
  const { audio } = renderWithApp(<Routes><Route path="/albums/:id" element={<AlbumPage />} /></Routes>, {
    path: "/albums/3",
    routes: { "GET /api/v1/albums/3": () => ({ body: { album, tracks: [tr(1), tr(2)] } }) },
  });
  await userEvent.click(await screen.findByRole("button", { name: "播放全部" }));
  expect(audio.src).toContain("/tracks/1/stream");
  expect(document.querySelector(".hero .cover img")).toHaveAttribute("src", "/api/v1/albums/3/cover?size=300");
  expect(screen.getByRole("link", { name: "邓丽君" })).toHaveAttribute("href", "/artists/4");
});

test("artist page play-all loads that artist's tracks", async () => {
  const { audio } = renderWithApp(<Routes><Route path="/artists/:id" element={<ArtistPage />} /></Routes>, {
    path: "/artists/4",
    routes: {
      "GET /api/v1/artists/4": () => ({ body: { artist: { id: 4, name: "邓丽君", track_count: 2 }, albums: [album] } }),
      "GET /api/v1/tracks": (_i, url) => { expect(url).toContain("artist=4"); return { body: { items: [tr(5), tr(6)], next_cursor: "" } }; },
    },
  });
  await userEvent.click(await screen.findByRole("button", { name: "播放全部" }));
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/5/stream"));
});

// Final review minor 10: a failed play-all fetch is shown, not swallowed.
test("artist page play-all shows an error when the tracks fail to load", async () => {
  renderWithApp(<Routes><Route path="/artists/:id" element={<ArtistPage />} /></Routes>, {
    path: "/artists/4",
    routes: {
      "GET /api/v1/artists/4": () => ({ body: { artist: { id: 4, name: "邓丽君", track_count: 2 }, albums: [album] } }),
      "GET /api/v1/tracks": () => ({ status: 500, body: { error: "服务器出错" } }),
    },
  });
  await userEvent.click(await screen.findByRole("button", { name: "播放全部" }));
  expect(await screen.findByText("服务器出错")).toBeInTheDocument();
});

test("unknown album shows not found", async () => {
  renderWithApp(<Routes><Route path="/albums/:id" element={<AlbumPage />} /></Routes>, {
    path: "/albums/99",
    routes: { "GET /api/v1/albums/99": () => ({ status: 404, body: { error: "not found" } }) },
  });
  expect(await screen.findByText("专辑不存在")).toBeInTheDocument();
});

test("navigating from a 404 album to a valid one recovers", async () => {
  renderWithApp(
    <Routes>
      <Route
        path="/albums/:id"
        element={
          <>
            <Link to="/albums/3">go</Link>
            <AlbumPage />
          </>
        }
      />
    </Routes>,
    {
      path: "/albums/99",
      routes: {
        "GET /api/v1/albums/99": () => ({ status: 404, body: { error: "not found" } }),
        "GET /api/v1/albums/3": () => ({ body: { album, tracks: [tr(1), tr(2)] } }),
      },
    },
  );
  expect(await screen.findByText("专辑不存在")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("link", { name: "go" }));
  expect(await screen.findByRole("heading", { name: "精选" })).toBeInTheDocument();
});

test("navigating from a 404 artist to a valid one recovers", async () => {
  renderWithApp(
    <Routes>
      <Route
        path="/artists/:id"
        element={
          <>
            <Link to="/artists/4">go</Link>
            <ArtistPage />
          </>
        }
      />
    </Routes>,
    {
      path: "/artists/99",
      routes: {
        "GET /api/v1/artists/99": () => ({ status: 404, body: { error: "not found" } }),
        "GET /api/v1/artists/4": () => ({ body: { artist: { id: 4, name: "邓丽君", track_count: 2 }, albums: [album] } }),
      },
    },
  );
  expect(await screen.findByText("歌手不存在")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("link", { name: "go" }));
  expect(await screen.findByRole("heading", { name: "邓丽君" })).toBeInTheDocument();
});

test("albums tab shows empty state message", async () => {
  renderWithApp(<LibraryPage />, { path: "/library?tab=albums", routes: { "GET /api/v1/albums": () => ({ body: { items: [], next_cursor: "" } }) } });
  expect(await screen.findByText("还没有专辑")).toBeInTheDocument();
});

test("artists tab shows empty state message", async () => {
  renderWithApp(<LibraryPage />, { path: "/library?tab=artists", routes: { "GET /api/v1/artists": () => ({ body: { items: [], next_cursor: "" } }) } });
  expect(await screen.findByText("还没有歌手")).toBeInTheDocument();
});

test("artist page shows empty albums state and disables play-all with no tracks", async () => {
  renderWithApp(<Routes><Route path="/artists/:id" element={<ArtistPage />} /></Routes>, {
    path: "/artists/5",
    routes: { "GET /api/v1/artists/5": () => ({ body: { artist: { id: 5, name: "无专辑歌手", track_count: 0 }, albums: [] } }) },
  });
  expect(await screen.findByText("没有专辑")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "播放全部" })).toBeDisabled();
});
