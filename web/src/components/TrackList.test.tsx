import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TrackList } from "./TrackList";
import { usePlayer } from "../player/PlayerProvider";
import { renderWithApp } from "../test/render";
import type { Track } from "../api/types";

const tr = (id: number, extra: Partial<Track> = {}) =>
  ({ id, title: `歌${id}`, artist: "邓丽君", album: "精选", duration_ms: 200000, codec: "flac", lossless: true, bitrate: 900, favorite: false, disliked: false, broken: false, broken_reason: "", ...extra }) as Track;

function renderList(tracks: Track[], role: "admin" | "member", props: Partial<Parameters<typeof TrackList>[0]> = {}) {
  return renderWithApp(<TrackList tracks={tracks} {...props} />, {
    role,
    routes: {
      "PUT /api/v1/favorites/2": () => ({ status: 204 }),
      "PUT /api/v1/dislikes/2": () => ({ status: 204 }),
      "DELETE /api/v1/tracks/2": () => ({ status: 204 }),
      "GET /api/v1/playlists": () => ({ body: [{ id: 7, name: "车上", track_count: 1, updated_at: 1 }] }),
      "GET /api/v1/playlists/7": () => ({ body: { playlist: { id: 7, name: "车上", track_count: 1, updated_at: 1 }, tracks: [tr(1)] } }),
      "PUT /api/v1/playlists/7": () => ({ status: 204 }),
    },
  });
}

test("tapping a row plays the list from that row", async () => {
  const { audio } = renderList([tr(1), tr(2), tr(3)], "member");
  await userEvent.click(await screen.findByText("歌2"));
  expect(audio.play).toHaveBeenCalled();
  expect(audio.src).toContain("/tracks/2/stream");
});

test("each row shows the track's 300 px cover over its initials", async () => {
  renderList([tr(1), tr(2)], "member");
  const row = (await screen.findByText("歌2")).closest("li")!;
  expect(row.querySelector(".cover img")).toHaveAttribute("src", "/api/v1/tracks/2/cover?size=300");
});

test("favorite toggles through the API and reports the change", async () => {
  const onChange = vi.fn();
  const { f } = renderList([tr(1), tr(2)], "member", { onChange });
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌2" }));
  await userEvent.click(screen.getByRole("menuitem", { name: "收藏" }));
  expect(f).toHaveBeenCalledWith("/api/v1/favorites/2", expect.objectContaining({ method: "PUT" }));
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ id: 2, favorite: true }));
});

test("dislike removes the row", async () => {
  const onRemoved = vi.fn();
  renderList([tr(1), tr(2)], "member", { onRemoved });
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌2" }));
  await userEvent.click(screen.getByRole("menuitem", { name: "不喜欢" }));
  expect(onRemoved).toHaveBeenCalledWith(2);
});

test("only admins see delete, and it needs a second tap", async () => {
  renderList([tr(1), tr(2)], "member");
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌2" }));
  expect(screen.queryByRole("menuitem", { name: "删除歌曲" })).toBeNull();
});

test("admin delete asks for confirmation then trashes", async () => {
  const onRemoved = vi.fn();
  const { f } = renderList([tr(1), tr(2)], "admin", { onRemoved });
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌2" }));
  await userEvent.click(await screen.findByRole("menuitem", { name: "删除歌曲" }));
  expect(f).not.toHaveBeenCalledWith("/api/v1/tracks/2", expect.anything());
  await userEvent.click(screen.getByRole("menuitem", { name: "确认删除（30天内可恢复）" }));
  expect(f).toHaveBeenCalledWith("/api/v1/tracks/2", expect.objectContaining({ method: "DELETE" }));
  expect(onRemoved).toHaveBeenCalledWith(2);
});

test("add to playlist appends the track id", async () => {
  const { f } = renderList([tr(1), tr(2)], "member");
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌2" }));
  await userEvent.click(screen.getByRole("menuitem", { name: "添加到歌单" }));
  const dialog = await screen.findByRole("dialog");
  await userEvent.click(await within(dialog).findByRole("button", { name: /车上/ }));
  const put = f.mock.calls.find((c) => c[0] === "/api/v1/playlists/7" && c[1]?.method === "PUT")!;
  expect(JSON.parse(put[1]!.body as string)).toEqual({ track_ids: [1, 2] });
});

test("broken tracks are disabled", async () => {
  const { audio } = renderList([tr(1, { broken: true })], "member");
  await userEvent.click(await screen.findByText("歌1"));
  expect(audio.src).not.toContain("/stream"); // (play() may be called once by the tap-unlock)
});

test("switching rows resets a pending delete confirmation", async () => {
  const { f } = renderList([tr(1), tr(2)], "admin");
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌1" }));
  await userEvent.click(await screen.findByRole("menuitem", { name: "删除歌曲" }));
  expect(screen.getByRole("menuitem", { name: "确认删除（30天内可恢复）" })).toBeInTheDocument();

  await userEvent.click(screen.getByRole("button", { name: "更多：歌2" }));
  expect(await screen.findByRole("menuitem", { name: "删除歌曲" })).toBeInTheDocument();
  expect(screen.queryByRole("menuitem", { name: "确认删除（30天内可恢复）" })).toBeNull();
  expect(f).not.toHaveBeenCalledWith("/api/v1/tracks/1", expect.anything());
  expect(f).not.toHaveBeenCalledWith("/api/v1/tracks/2", expect.anything());
});

test("double-clicking 新建 only creates one playlist", async () => {
  const { f } = renderWithApp(<TrackList tracks={[tr(1), tr(2)]} />, {
    routes: {
      "GET /api/v1/playlists": () => ({ body: [] }),
      "POST /api/v1/playlists": () => ({ body: { id: 9, name: "新歌单", track_count: 1, updated_at: 1 } }),
    },
  });
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌2" }));
  await userEvent.click(screen.getByRole("menuitem", { name: "添加到歌单" }));
  const dialog = await screen.findByRole("dialog");
  await userEvent.type(within(dialog).getByPlaceholderText("新歌单名称"), "新歌单");
  const createBtn = within(dialog).getByRole("button", { name: "新建" });
  await userEvent.click(createBtn);
  await userEvent.click(createBtn);
  const posts = f.mock.calls.filter((c) => c[0] === "/api/v1/playlists" && c[1]?.method === "POST");
  expect(posts.length).toBe(1);
});

test("a broken track in the middle is skipped from the queue", async () => {
  function QueueProbe() {
    const { queue } = usePlayer();
    return <div data-testid="queue">{queue.tracks.map((t) => t.id).join(",")}</div>;
  }
  const { audio } = renderWithApp(
    <>
      <TrackList tracks={[tr(1), tr(2, { broken: true }), tr(3)]} />
      <QueueProbe />
    </>,
  );
  await userEvent.click(await screen.findByText("歌3"));
  expect(audio.src).toContain("/tracks/3/stream");
  expect(screen.getByTestId("queue").textContent).toBe("1,3");
});

test("⋯ → 添加到队列 queues the track after what was queued with 下一首播放", async () => {
  let p!: ReturnType<typeof usePlayer>;
  function Probe() {
    p = usePlayer();
    return null;
  }
  const ts = [tr(1), tr(2), tr(3), tr(4)];
  renderWithApp(<><TrackList tracks={ts} /><Probe /></>, { role: "member" });
  await userEvent.click(await screen.findByText("歌1"));
  await userEvent.click(screen.getByRole("button", { name: "更多：歌4" }));
  await userEvent.click(screen.getByRole("menuitem", { name: "下一首播放" }));
  await userEvent.click(screen.getByRole("button", { name: "更多：歌3" }));
  const menu = screen.getByRole("menu");
  expect(within(menu).getAllByRole("menuitem").map((b) => b.textContent).slice(0, 2)).toEqual(["下一首播放", "添加到队列"]);
  await userEvent.click(within(menu).getByRole("menuitem", { name: "添加到队列" }));
  expect(p.queue.tracks.map((x) => x.id)).toEqual([1, 4, 3, 2]);
  expect(p.current?.id).toBe(1);
  expect(screen.queryByRole("menu")).toBeNull();
});

test("the ⋯ menu closes on a tap outside it; ⋯ itself still toggles it", async () => {
  renderList([tr(1), tr(2)], "member");
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌2" }));
  expect(screen.getByRole("menu")).toBeInTheDocument();
  await userEvent.click(document.body);
  expect(screen.queryByRole("menu")).toBeNull();
  await userEvent.click(screen.getByRole("button", { name: "更多：歌2" }));
  expect(screen.getByRole("menu")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "更多：歌2" }));
  expect(screen.queryByRole("menu")).toBeNull();
  // Another row's ⋯ moves the menu there.
  await userEvent.click(screen.getByRole("button", { name: "更多：歌2" }));
  await userEvent.click(screen.getByRole("button", { name: "更多：歌1" }));
  expect(screen.getAllByRole("menu")).toHaveLength(1);
  expect(within(screen.getByText("歌1").closest("li")!).getByRole("menu")).toBeInTheDocument();
});
