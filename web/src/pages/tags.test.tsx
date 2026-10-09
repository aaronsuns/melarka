import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router";
import LibraryPage from "./LibraryPage";
import HomePage from "./HomePage";
import TagPage from "./TagPage";
import { EditTags } from "../components/EditTags";
import { TrackList } from "../components/TrackList";
import { MiniPlayer } from "../player/MiniPlayer";
import { usePlayer, type Player } from "../player/PlayerProvider";
import { setLocale } from "../i18n/i18n";
import { renderWithApp } from "../test/render";
import type { Track } from "../api/types";

const tr = (id: number, extra: Partial<Track> = {}) =>
  ({ id, title: `歌${id}`, artist: "邓丽君", album: "精选", duration_ms: 1000, codec: "mp3", bitrate: 320, lossless: false, ...extra }) as Track;
const TAGS = [
  { name: "mandopop", kind: "genre", count: 12 },
  { name: "开车啦", kind: "other", count: 1 },
  { name: "chill", kind: "mood", count: 3 },
];
const VOCAB = [
  { slug: "mandopop", kind: "genre" },
  { slug: "chill", kind: "mood" },
  { slug: "driving", kind: "scene" },
];

function tagRoutes() {
  return {
    "GET /api/v1/tags": () => ({ body: TAGS }),
    "GET /api/v1/tracks": () => ({ body: { items: [tr(1), tr(2)], next_cursor: "" } }),
  };
}

function LibraryWithTag() {
  return (
    <Routes>
      <Route path="/library" element={<LibraryPage />} />
      <Route path="/tags/:name" element={<TagPage />} />
    </Routes>
  );
}

test("Tags tab groups tags by kind with localized labels and counts; free tags as typed", async () => {
  renderWithApp(<LibraryWithTag />, { path: "/library?tab=tags", routes: tagRoutes() });
  const headings = await screen.findAllByRole("heading", { level: 2 });
  expect(headings.map((h) => h.textContent)).toEqual(["流派", "心情", "其他"]);
  expect(screen.getByRole("link", { name: "华语流行 · 12" })).toHaveAttribute("href", "/tags/mandopop");
  expect(screen.getByRole("link", { name: "放松 · 3" })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "开车啦 · 1" })).toHaveAttribute("href", `/tags/${encodeURIComponent("开车啦")}`);
});

test("Tags tab empty state", async () => {
  renderWithApp(<LibraryPage />, { path: "/library?tab=tags", routes: { "GET /api/v1/tags": () => ({ body: [] }) } });
  expect(await screen.findByText("还没有标签")).toBeInTheDocument();
});

test("tapping a tag opens its page listing tracks; 🔀 plays /tracks/random?tag=", async () => {
  const tracks = vi.fn(() => ({ body: { items: [tr(1), tr(2)], next_cursor: "" } }));
  const random = vi.fn(() => ({ body: [tr(9), tr(10)] }));
  const { audio, f } = renderWithApp(<LibraryWithTag />, {
    path: "/library?tab=tags",
    routes: { "GET /api/v1/tags": () => ({ body: TAGS }), "GET /api/v1/tracks": tracks, "GET /api/v1/tracks/random": random },
  });
  await userEvent.click(await screen.findByRole("link", { name: "华语流行 · 12" }));
  expect(await screen.findByRole("heading", { name: "华语流行" })).toBeInTheDocument();
  expect(await screen.findByText("歌1")).toBeInTheDocument();
  expect((tracks.mock.calls[0] as unknown[])[1]).toContain("tag=mandopop");
  await userEvent.click(screen.getByRole("button", { name: /随机播放这个标签/ }));
  await vi.waitFor(() => expect(audio.src).toContain("/tracks/9/stream"));
  expect(f.mock.calls.some((c) => String(c[0]).endsWith("/tracks/random?n=200&tag=mandopop"))).toBe(true);
});

test("Home shows the 常用标签 row (most used first, max 12) only when tags exist", async () => {
  const many = Array.from({ length: 15 }, (_, i) => ({ name: `t${i}`, kind: "other", count: 100 - i }));
  renderWithApp(<HomePage />, { routes: { ...tagRoutes(), "GET /api/v1/tags": () => ({ body: [{ name: "x-last", kind: "other", count: 1 }, ...many] }) } });
  const heading = await screen.findByRole("heading", { name: "常用标签" });
  const row = heading.parentElement!;
  const links = within(row).getAllByRole("link");
  expect(links).toHaveLength(12);
  expect(links[0]).toHaveTextContent("t0 · 100");
  expect(within(row).queryByText(/x-last/)).toBeNull();
});

test("Home hides the 常用标签 row when there are no tags", async () => {
  renderWithApp(<HomePage />, { routes: { ...tagRoutes(), "GET /api/v1/tags": () => ({ body: [] }) } });
  await screen.findByRole("heading", { name: "最近添加" });
  await vi.waitFor(() => expect(screen.queryByRole("heading", { name: "常用标签" })).toBeNull());
});

test("in Swedish the chip reads Mandopop and the heading Genre", async () => {
  setLocale("sv");
  renderWithApp(<LibraryPage />, { path: "/library?tab=tags", routes: tagRoutes() });
  expect(await screen.findByRole("heading", { name: "Genre" })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Mandopop · 12" })).toBeInTheDocument();
});

function editRoutes(put: ReturnType<typeof vi.fn>) {
  return {
    "GET /api/v1/tracks/7/tags": () => ({ body: [{ name: "chill", kind: "mood" }, { name: "driving", kind: "scene" }] }),
    "GET /api/v1/tags/vocabulary": () => ({ body: VOCAB }),
    "PUT /api/v1/tracks/7/tags": put as never,
  };
}

test("EditTags: remove 放松, add a free tag and a vocabulary label; PUT carries manual tags", async () => {
  const put = vi.fn(() => ({ status: 204 }));
  const onClose = vi.fn();
  const { f } = renderWithApp(<EditTags track={tr(7)} onClose={onClose} />, { role: "admin", routes: editRoutes(put) });
  const dialog = await screen.findByRole("dialog");
  await userEvent.click(await within(dialog).findByRole("button", { name: "删除放松" }));
  const input = within(dialog).getByLabelText("添加标签");
  await userEvent.type(input, "通勤{Enter}");
  await userEvent.type(input, "华语流行{Enter}");
  await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));

  await vi.waitFor(() => expect(onClose).toHaveBeenCalled());
  const call = f.mock.calls.find((c) => c[0] === "/api/v1/tracks/7/tags" && c[1]?.method === "PUT")!;
  const body = JSON.parse(call[1]!.body as string);
  expect(body.source).toBe("manual");
  const sorted = <T extends { name: string }>(a: T[]) => [...a].sort((x, y) => x.name.localeCompare(y.name));
  expect(sorted(body.tags)).toEqual(
    sorted([{ name: "driving", kind: "scene" }, { name: "mandopop", kind: "genre" }, { name: "通勤", kind: "other" }]),
  );
});

test("EditTags: a save error shows inline and keeps the sheet open", async () => {
  const put = vi.fn(() => ({ status: 500, body: { error: "保存失败了" } }));
  const onClose = vi.fn();
  renderWithApp(<EditTags track={tr(7)} onClose={onClose} />, { role: "admin", routes: editRoutes(put) });
  const dialog = await screen.findByRole("dialog");
  await within(dialog).findByRole("button", { name: "删除放松" });
  await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
  expect(await within(dialog).findByRole("alert")).toHaveTextContent("保存失败了");
  expect(onClose).not.toHaveBeenCalled();
});

test("track menu: admin sees 编辑标签 and it opens the editor; members don't", async () => {
  const put = vi.fn(() => ({ status: 204 }));
  renderWithApp(<TrackList tracks={[tr(7)]} />, { role: "admin", routes: editRoutes(put) });
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌7" }));
  await userEvent.click(await screen.findByRole("menuitem", { name: "编辑标签" }));
  expect(await screen.findByRole("dialog", { name: "标签" })).toBeInTheDocument();
});

test("track menu: members don't see 编辑标签", async () => {
  renderWithApp(<TrackList tracks={[tr(7)]} />, { role: "member" });
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌7" }));
  expect(screen.queryByRole("menuitem", { name: "编辑标签" })).toBeNull();
});

let p!: Player;
function Grab() {
  p = usePlayer();
  return null;
}

test("NowPlaying: admin gets a separate 编辑标签 button next to ✎; members don't", async () => {
  const put = vi.fn(() => ({ status: 204 }));
  renderWithApp(<><Grab /><MiniPlayer /></>, { role: "admin", routes: editRoutes(put) });
  p.playList([tr(7), tr(8)], 0);
  await userEvent.click(await screen.findByText("歌7"));
  const now = await screen.findByRole("dialog", { name: "正在播放" });
  expect(within(now).getByRole("button", { name: "编辑信息" })).toBeInTheDocument();
  await userEvent.click(within(now).getByRole("button", { name: "编辑标签" }));
  expect(await screen.findByRole("dialog", { name: "标签" })).toBeInTheDocument();
});

test("NowPlaying: members don't see 编辑标签", async () => {
  renderWithApp(<><Grab /><MiniPlayer /></>, { role: "member" });
  p.playList([tr(7), tr(8)], 0);
  await userEvent.click(await screen.findByText("歌7"));
  await screen.findByRole("dialog", { name: "正在播放" });
  expect(screen.queryByRole("button", { name: "编辑标签" })).toBeNull();
});
