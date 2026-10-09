import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, Route, Routes } from "react-router";
import PlaylistsPage from "./PlaylistsPage";
import PlaylistPage from "./PlaylistPage";
import { renderWithApp } from "../test/render";
import type { Track } from "../api/types";

const tr = (id: number) => ({ id, title: `歌${id}`, artist: "a", album: "b", duration_ms: 1000, codec: "mp3", bitrate: 320, lossless: false }) as Track;
const pl = { id: 7, name: "车上", track_count: 2, updated_at: 1 };
const routes = <Routes><Route path="/playlists" element={<PlaylistsPage />} /><Route path="/playlists/:id" element={<PlaylistPage />} /></Routes>;

test("create a playlist and land on it", async () => {
  renderWithApp(routes, {
    path: "/playlists",
    routes: {
      "GET /api/v1/playlists": () => ({ body: [pl] }),
      "POST /api/v1/playlists": (init) => ({ status: 201, body: { id: 8, name: JSON.parse(init.body as string).name, track_count: 0, updated_at: 2 } }),
      "GET /api/v1/playlists/8": () => ({ body: { playlist: { id: 8, name: "散步", track_count: 0, updated_at: 2 }, tracks: [] } }),
    },
  });
  expect(await screen.findByRole("link", { name: /车上/ })).toHaveAttribute("href", "/playlists/7");
  await userEvent.type(screen.getByPlaceholderText("新歌单名称"), "散步");
  await userEvent.click(screen.getByRole("button", { name: "新建歌单" }));
  expect(await screen.findByRole("heading", { name: "散步" })).toBeInTheDocument();
});

test("remove a track from the playlist", async () => {
  const put = vi.fn(() => ({ status: 204 }));
  renderWithApp(routes, {
    path: "/playlists/7",
    routes: { "GET /api/v1/playlists/7": () => ({ body: { playlist: pl, tracks: [tr(1), tr(2)] } }), "PUT /api/v1/playlists/7": put },
  });
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌1" }));
  await userEvent.click(screen.getByRole("menuitem", { name: "从歌单移除" }));
  await vi.waitFor(() => expect(screen.queryByText("歌1")).toBeNull());
  expect(JSON.parse(((put.mock.calls[0] as unknown[])[0] as RequestInit).body as string)).toEqual({ track_ids: [2] });
});

test("remove two tracks sequentially uses the latest list", async () => {
  const put = vi.fn(() => ({ status: 204 }));
  renderWithApp(routes, {
    path: "/playlists/7",
    routes: {
      "GET /api/v1/playlists/7": () => ({ body: { playlist: pl, tracks: [tr(1), tr(2), tr(3)] } }),
      "PUT /api/v1/playlists/7": put,
    },
  });
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌1" }));
  await userEvent.click(screen.getByRole("menuitem", { name: "从歌单移除" }));
  await vi.waitFor(() => expect(screen.queryByText("歌1")).toBeNull());

  await userEvent.click(screen.getByRole("button", { name: "更多：歌2" }));
  await userEvent.click(screen.getByRole("menuitem", { name: "从歌单移除" }));
  await vi.waitFor(() => expect(screen.queryByText("歌2")).toBeNull());

  expect(screen.getByText("歌3")).toBeInTheDocument();
  expect(put).toHaveBeenCalledTimes(2);
  expect(JSON.parse(((put.mock.calls[0] as unknown[])[0] as RequestInit).body as string)).toEqual({ track_ids: [2, 3] });
  expect(JSON.parse(((put.mock.calls[1] as unknown[])[0] as RequestInit).body as string)).toEqual({ track_ids: [3] });
});

test("a failed remove rolls back and shows the error", async () => {
  const put = vi.fn(() => ({ status: 500, body: { error: "服务器错误" } }));
  renderWithApp(routes, {
    path: "/playlists/7",
    routes: { "GET /api/v1/playlists/7": () => ({ body: { playlist: pl, tracks: [tr(1), tr(2)] } }), "PUT /api/v1/playlists/7": put },
  });
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌1" }));
  await userEvent.click(screen.getByRole("menuitem", { name: "从歌单移除" }));
  expect(await screen.findByText("服务器错误")).toBeInTheDocument();
  expect(screen.getByText("歌1")).toBeInTheDocument();
  expect(screen.getByText("歌2")).toBeInTheDocument();
});

test("navigating to a different playlist resets an armed delete confirm", async () => {
  renderWithApp(
    <Routes>
      <Route
        path="/playlists/:id"
        element={
          <>
            <Link to="/playlists/8">go8</Link>
            <PlaylistPage />
          </>
        }
      />
    </Routes>,
    {
      path: "/playlists/7",
      routes: {
        "GET /api/v1/playlists/7": () => ({ body: { playlist: pl, tracks: [] } }),
        "GET /api/v1/playlists/8": () => ({ body: { playlist: { id: 8, name: "散步", track_count: 0, updated_at: 2 }, tracks: [] } }),
      },
    },
  );
  await userEvent.click(await screen.findByRole("button", { name: "删除歌单" }));
  expect(screen.getByRole("button", { name: "确认删除歌单" })).toBeInTheDocument();

  await userEvent.click(screen.getByRole("link", { name: "go8" }));
  await screen.findByRole("heading", { name: "散步" });
  expect(screen.getByRole("button", { name: "删除歌单" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "确认删除歌单" })).toBeNull();
});

test("delete needs confirmation", async () => {
  const del = vi.fn(() => ({ status: 204 }));
  renderWithApp(routes, {
    path: "/playlists/7",
    routes: {
      "GET /api/v1/playlists/7": () => ({ body: { playlist: pl, tracks: [] } }),
      "DELETE /api/v1/playlists/7": del,
      "GET /api/v1/playlists": () => ({ body: [] }),
    },
  });
  await userEvent.click(await screen.findByRole("button", { name: "删除歌单" }));
  expect(del).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "确认删除歌单" }));
  await vi.waitFor(() => expect(del).toHaveBeenCalled());
  expect(await screen.findByText("还没有歌单")).toBeInTheDocument();
});
