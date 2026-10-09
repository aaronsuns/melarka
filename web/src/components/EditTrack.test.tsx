import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TrackList } from "./TrackList";
import { MiniPlayer } from "../player/MiniPlayer";
import { usePlayer, type Player } from "../player/PlayerProvider";
import { renderWithApp } from "../test/render";
import type { Track } from "../api/types";

const tr = (id: number, extra: Partial<Track> = {}) =>
  ({
    id,
    title: `歌${id}`,
    artist: "邓丽君",
    album: "精选",
    year: 1980,
    duration_ms: 200000,
    codec: "flac",
    lossless: true,
    bitrate: 900,
    favorite: false,
    disliked: false,
    broken: false,
    broken_reason: "",
    ...extra,
  }) as Track;

function renderList(tracks: Track[], role: "admin" | "member", props: Partial<Parameters<typeof TrackList>[0]> = {}) {
  return renderWithApp(<TrackList tracks={tracks} {...props} />, {
    role,
    routes: { "PATCH /api/v1/tracks/2": () => ({ status: 204 }) },
  });
}

test("member doesn't see 编辑信息", async () => {
  renderList([tr(1), tr(2)], "member");
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌2" }));
  expect(screen.queryByRole("menuitem", { name: "编辑信息" })).toBeNull();
});

test("admin edits artist only: PATCH carries just that field, and the row updates", async () => {
  const onChange = vi.fn();
  const { f } = renderList([tr(1), tr(2)], "admin", { onChange });
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌2" }));
  await userEvent.click(await screen.findByRole("menuitem", { name: "编辑信息" }));
  const dialog = await screen.findByRole("dialog");
  const artistInput = within(dialog).getByLabelText("歌手");
  await userEvent.clear(artistInput);
  await userEvent.type(artistInput, "邓丽君2");
  await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));

  const patch = f.mock.calls.find((c) => c[0] === "/api/v1/tracks/2" && c[1]?.method === "PATCH")!;
  expect(JSON.parse(patch[1]!.body as string)).toEqual({ artist: "邓丽君2" });
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ id: 2, artist: "邓丽君2" }));
  expect(screen.queryByRole("dialog")).toBeNull();
});

test("clearing a field sends an empty string to clear the override", async () => {
  const { f } = renderList([tr(1), tr(2)], "admin");
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌2" }));
  await userEvent.click(await screen.findByRole("menuitem", { name: "编辑信息" }));
  const dialog = await screen.findByRole("dialog");
  await userEvent.clear(within(dialog).getByLabelText("专辑"));
  await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));

  const patch = f.mock.calls.find((c) => c[0] === "/api/v1/tracks/2" && c[1]?.method === "PATCH")!;
  expect(JSON.parse(patch[1]!.body as string)).toEqual({ album: "" });
});

test("clearing the year sends year: 0", async () => {
  const { f } = renderList([tr(1), tr(2)], "admin");
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌2" }));
  await userEvent.click(await screen.findByRole("menuitem", { name: "编辑信息" }));
  const dialog = await screen.findByRole("dialog");
  await userEvent.clear(within(dialog).getByLabelText("年份"));
  await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));

  const patch = f.mock.calls.find((c) => c[0] === "/api/v1/tracks/2" && c[1]?.method === "PATCH")!;
  expect(JSON.parse(patch[1]!.body as string)).toEqual({ year: 0 });
});

test("server error shows inline and keeps the dialog open", async () => {
  renderWithApp(<TrackList tracks={[tr(1), tr(2)]} />, {
    role: "admin",
    routes: { "PATCH /api/v1/tracks/2": () => ({ status: 500, body: { error: "保存失败了" } }) },
  });
  await userEvent.click(await screen.findByRole("button", { name: "更多：歌2" }));
  await userEvent.click(await screen.findByRole("menuitem", { name: "编辑信息" }));
  const dialog = await screen.findByRole("dialog");
  await userEvent.clear(within(dialog).getByLabelText("歌手"));
  await userEvent.type(within(dialog).getByLabelText("歌手"), "新歌手");
  await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));
  expect(await within(dialog).findByRole("alert")).toHaveTextContent("保存失败了");
});

let p!: Player;
function Grab() {
  p = usePlayer();
  return null;
}

test("admin sees an edit button in NowPlaying; saving updates the queue/now-playing in place", async () => {
  const { f } = renderWithApp(<><Grab /><MiniPlayer /></>, {
    role: "admin",
    routes: { "PATCH /api/v1/tracks/1": () => ({ status: 204 }) },
  });
  p.playList([tr(1), tr(2)], 0);
  await userEvent.click(await screen.findByText("歌1"));
  await screen.findByRole("dialog", { name: "正在播放" });
  await userEvent.click(screen.getByRole("button", { name: "编辑信息" }));
  const dialog = await screen.findByRole("dialog", { name: "编辑信息" });
  const titleInput = within(dialog).getByLabelText("歌名");
  await userEvent.clear(titleInput);
  await userEvent.type(titleInput, "新标题");
  await userEvent.click(within(dialog).getByRole("button", { name: "保存" }));

  const patch = f.mock.calls.find((c) => c[0] === "/api/v1/tracks/1" && c[1]?.method === "PATCH")!;
  expect(JSON.parse(patch[1]!.body as string)).toEqual({ title: "新标题" });
  expect(await within(screen.getByRole("dialog", { name: "正在播放" })).findByText("新标题")).toBeInTheDocument();
  expect(p.queue.tracks.find((t) => t.id === 1)?.title).toBe("新标题");
});

test("member doesn't see the edit button in NowPlaying", async () => {
  renderWithApp(<><Grab /><MiniPlayer /></>, { role: "member" });
  p.playList([tr(1), tr(2)], 0);
  await userEvent.click(await screen.findByText("歌1"));
  await screen.findByRole("dialog", { name: "正在播放" });
  expect(screen.queryByRole("button", { name: "编辑信息" })).toBeNull();
});
