import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MiniPlayer } from "./MiniPlayer";
import { usePlayer, type Player } from "./PlayerProvider";
import { renderWithApp } from "../test/render";
import { installFakeNative } from "../native/fakeNative";
import { trackItem } from "../native/items";
import { resetSessionOwner } from "./sessionOwner";
import type { Track } from "../api/types";

const tr = (id: number) => ({ id, title: `歌${id}`, artist: "邓丽君", album: "精选", duration_ms: 200000, codec: "flac", bitrate: 900, lossless: true, favorite: false }) as Track;

let p!: Player;
function Grab() {
  p = usePlayer();
  return null;
}

async function openNowPlaying(title: string) {
  await userEvent.click(await screen.findByText(title));
  return screen.findByRole("dialog", { name: "正在播放" });
}

test("the repeat button cycles off → all → one → off, and says which", async () => {
  renderWithApp(<><Grab /><MiniPlayer /></>);
  act(() => p.playList([tr(1), tr(2)], 0));
  await openNowPlaying("歌1");
  const repeat = screen.getByRole("button", { name: "循环：关" });
  expect(repeat).toHaveAttribute("aria-pressed", "false");
  expect(repeat).toHaveAttribute("data-repeat", "off");
  await userEvent.click(repeat);
  expect(screen.getByRole("button", { name: "列表循环" })).toHaveAttribute("aria-pressed", "true");
  expect(p.modes.repeat).toBe("all");
  await userEvent.click(screen.getByRole("button", { name: "列表循环" }));
  const one = screen.getByRole("button", { name: "单曲循环" });
  expect(one).toHaveAttribute("aria-pressed", "true");
  await userEvent.click(one);
  expect(screen.getByRole("button", { name: "循环：关" })).toHaveAttribute("data-repeat", "off");
  expect(p.modes.repeat).toBe("off");
});

test("the shuffle button's pressed state follows shuffle", async () => {
  renderWithApp(<><Grab /><MiniPlayer /></>);
  act(() => p.playList([tr(1), tr(2), tr(3)], 0));
  await openNowPlaying("歌1");
  const shuffle = screen.getByRole("button", { name: "随机播放" });
  expect(shuffle).toHaveAttribute("aria-pressed", "false");
  await userEvent.click(shuffle);
  expect(shuffle).toHaveAttribute("aria-pressed", "true");
  expect(p.modes.shuffle).toBe(true);
  await userEvent.click(shuffle);
  expect(shuffle).toHaveAttribute("aria-pressed", "false");
});

test("the queue's end line says the queue starts over with repeat all", async () => {
  renderWithApp(<><Grab /><MiniPlayer /></>);
  act(() => p.playList([tr(1)], 0));
  await openNowPlaying("歌1");
  await userEvent.click(screen.getByRole("button", { name: "队列" }));
  expect(screen.getByText("队列结束后会自动播放私人电台")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "循环：关" }));
  expect(screen.getByText("播完后从头再来")).toBeInTheDocument();
});

test("in an iPhone app that reports no modes (an older app, or no state yet) the buttons are hidden", async () => {
  const n = installFakeNative();
  try {
    renderWithApp(<><Grab /><MiniPlayer /></>);
    n.emit({ type: "queue", kind: "track", items: [trackItem(tr(1)), trackItem(tr(2))], index: 0, source: "list" });
    const dialog = await openNowPlaying("歌1");
    expect(p.modesAvailable).toBe(false);
    expect(within(dialog).getByRole("button", { name: "下一首" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "随机播放" })).toBeNull();
    expect(screen.queryByRole("button", { name: /循环/ })).toBeNull();
  } finally {
    n.uninstall();
    resetSessionOwner();
  }
});

test("the mode buttons draw line icons; repeat one adds a 1 badge", async () => {
  renderWithApp(<><Grab /><MiniPlayer /></>);
  act(() => p.playList([tr(1), tr(2)], 0));
  const dialog = await openNowPlaying("歌1");
  const shuffle = within(dialog).getByRole("button", { name: "随机播放" });
  expect(shuffle.querySelector("svg")).not.toBeNull();
  expect(shuffle).not.toHaveTextContent(/🔀/u);
  const off = within(dialog).getByRole("button", { name: "循环：关" });
  expect(off.querySelector("svg")).not.toBeNull();
  expect(off.querySelector(".repeat-one-badge")).toBeNull();
  await userEvent.click(off);
  const all = within(dialog).getByRole("button", { name: "列表循环" });
  expect(all.querySelector("svg")).not.toBeNull();
  expect(all.querySelector(".repeat-one-badge")).toBeNull();
  await userEvent.click(all);
  const one = within(dialog).getByRole("button", { name: "单曲循环" });
  expect(one).toHaveAttribute("data-repeat", "one");
  expect(one.querySelector(".repeat-one-badge")).toHaveTextContent("1");
  expect(one).not.toHaveTextContent(/[🔁🔂]/u);
});
