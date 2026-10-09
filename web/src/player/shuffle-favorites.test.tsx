import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import HomePage from "../pages/HomePage";
import LibraryPage from "../pages/LibraryPage";
import { MiniPlayer } from "./MiniPlayer";
import { usePlayer, type Player } from "./PlayerProvider";
import { renderWithApp } from "../test/render";
import type { Track } from "../api/types";

const tr = (id: number) => ({ id, title: `歌${id}`, artist: "邓丽君", album: "精选", duration_ms: 200000, codec: "flac", bitrate: 900, lossless: true, favorite: true, status: "kept" }) as Track;
const empty = { body: { items: [], next_cursor: "" } };

let p!: Player;
function Grab() {
  p = usePlayer();
  return null;
}

const favRoute = { "GET /api/v1/tracks/random": () => ({ body: { source: "favorites", tracks: [tr(7), tr(8)] } }) };

test("home: the favorites shuffle is the first button, primes in the tap, and queues favorites", async () => {
  const { audio } = renderWithApp(<><Grab /><HomePage /></>, { routes: { "GET /api/v1/tracks": () => empty, ...favRoute } });
  const btn = await screen.findByRole("button", { name: /随机播放收藏/ });
  expect(screen.getAllByRole("button")[0]).toBe(btn);
  act(() => btn.click());
  expect(audio.play).toHaveBeenCalledTimes(1); // primed before the fetch resolved
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/7/stream"));
  expect(p.queue.source).toBe("favorites");
  // One row of same-style quick-play buttons: favorites, global shuffle, radio.
  const row = btn.closest(".quick-actions")!;
  const quick = [...row.querySelectorAll("button")];
  expect(quick.map((b) => b.textContent)).toEqual(["🔀 随机播放收藏", "🔀 随机播放", "📻 私人电台"]);
  for (const b of quick) expect(b.className).toBe("secondary quick");
});

test("library favorites tab: a compact shuffle-favorites button sits in the tab header, above the songs", async () => {
  const { audio } = renderWithApp(<><Grab /><LibraryPage /></>, {
    path: "/library?tab=favorites",
    routes: { "GET /api/v1/tracks": () => ({ body: { items: [{ ...tr(1) }], next_cursor: "" } }), ...favRoute },
  });
  const btn = await screen.findByRole("button", { name: /随机播放收藏/ });
  expect(btn).toHaveClass("secondary");
  expect(btn.closest(".tab-actions")).not.toBeNull();
  const song = await screen.findByText("歌1");
  expect(btn.compareDocumentPosition(song) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  await userEvent.click(btn);
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/7/stream"));
  expect(p.queue.source).toBe("favorites");
});

test("library songs tab has no favorites shuffle button", async () => {
  renderWithApp(<LibraryPage />, { path: "/library?tab=songs", routes: { "GET /api/v1/tracks": () => empty } });
  await screen.findByRole("tablist");
  expect(screen.queryByRole("button", { name: /随机播放收藏/ })).toBeNull();
});

test("idle mini player offers the favorites shuffle with a 44px target", async () => {
  const { audio } = renderWithApp(<><Grab /><MiniPlayer /></>, { routes: favRoute });
  const btn = await screen.findByRole("button", { name: /随机播放收藏/ });
  expect(btn).toHaveClass("mini-shuffle");
  act(() => btn.click());
  expect(audio.play).toHaveBeenCalledTimes(1);
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/7/stream"));
  expect(p.queue.source).toBe("favorites");
  // Something is loaded now: the shortcut is gone and the normal mini player shows.
  expect(screen.queryByRole("button", { name: /随机播放收藏/ })).toBeNull();
  expect(await screen.findByText("歌7")).toBeInTheDocument();
});

test("mini player with a track loaded has no shuffle shortcut", async () => {
  renderWithApp(<><Grab /><MiniPlayer /></>);
  act(() => p.playList([tr(1)], 0));
  expect(await screen.findByText("歌1")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /随机播放收藏/ })).toBeNull();
});

test("no favorites: falls back to a global shuffle and says so", async () => {
  renderWithApp(<><Grab /><MiniPlayer /></>, {
    routes: { "GET /api/v1/tracks/random": () => ({ body: { source: "all", tracks: [tr(5), tr(6)] } }) },
  });
  await userEvent.click(await screen.findByRole("button", { name: /随机播放收藏/ }));
  expect(await screen.findByText("还没有收藏，已随机播放整个音乐库")).toBeInTheDocument();
  expect(p.queue.source).toBe("shuffle");
  expect(p.queue.tracks.map((x) => x.id)).toEqual([5, 6]);
});

test("idle shortcut is hidden until the boot has settled (no race with resume)", async () => {
  renderWithApp(<MiniPlayer />);
  // Synchronously after render the queue fetch hasn't resolved yet.
  expect(screen.queryByRole("button", { name: /随机播放收藏/ })).toBeNull();
  expect(await screen.findByRole("button", { name: /随机播放收藏/ })).toBeInTheDocument();
});

test("idle shortcut failure shows a notice", async () => {
  renderWithApp(<MiniPlayer />, { routes: { "GET /api/v1/tracks/random": () => ({ status: 500, body: { error: "随机播放失败" } }) } });
  await userEvent.click(await screen.findByRole("button", { name: /随机播放收藏/ }));
  expect(await screen.findByText("随机播放失败")).toBeInTheDocument();
});

test("idle shortcut with an empty library shows a notice", async () => {
  renderWithApp(<MiniPlayer />, { routes: { "GET /api/v1/tracks/random": (_i, url) => ({ body: url.includes("source=favorites") ? { source: "all", tracks: [] } : [] }) } });
  await userEvent.click(await screen.findByRole("button", { name: /随机播放收藏/ }));
  expect(await screen.findByText("随机播放暂时不可用")).toBeInTheDocument();
});
