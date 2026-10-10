import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MiniPlayer } from "./MiniPlayer";
import { usePlayer, type Player } from "./PlayerProvider";
import SettingsPage from "../pages/SettingsPage";
import { TrackList } from "../components/TrackList";
import { renderWithApp } from "../test/render";
import type { Track } from "../api/types";

const tr = (id: number) => ({ id, title: `歌${id}`, artist: "邓丽君", album: "精选", duration_ms: 200000, codec: "flac", bitrate: 900, lossless: true, favorite: false }) as Track;

let p!: Player;
function Grab() {
  p = usePlayer();
  return null;
}

test("mini player controls and now playing", async () => {
  const { audio, f } = renderWithApp(<><Grab /><MiniPlayer /></>, { routes: { "PUT /api/v1/favorites/1": () => ({ status: 204 }) } });
  expect(screen.queryByRole("button", { name: "暂停" })).toBeNull();
  act(() => p.playList([tr(1), tr(2), tr(3), tr(4)], 0));
  expect(await screen.findByRole("button", { name: "暂停" })).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "暂停" }));
  expect(audio.pause).toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "下一首" }));
  expect(await screen.findByText("歌2")).toBeInTheDocument();
  expect(document.querySelector(".mini .cover img")).toHaveAttribute("src", "/api/v1/tracks/2/cover?size=300");

  await userEvent.click(screen.getByText("歌2"));
  const dialog = await screen.findByRole("dialog", { name: "正在播放" });
  expect(dialog).toBeInTheDocument();
  expect(dialog.querySelector(".now-art .cover img")).toHaveAttribute("src", "/api/v1/tracks/2/cover?size=1000");
  await userEvent.click(screen.getByRole("button", { name: "队列" }));
  await userEvent.click(await screen.findByRole("button", { name: /^歌4/ }));
  expect(p.current?.id).toBe(4);
  await userEvent.click(screen.getByRole("button", { name: "收起" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  void f;
});

// React tracks a controlled input's value on the DOM node itself (to detect
// whether a later native event actually changed anything). Assigning
// `.value` directly goes through that same wrapper and pre-syncs the
// tracker, so the "input"/"change" dispatch that follows looks like a
// no-op and onChange never fires. The standard RTL workaround is to write
// through the *original*, unwrapped prototype setter instead.
const nativeInputValueSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!;

test("seek slider previews on input but only commits (seeks) on release", async () => {
  const { audio } = renderWithApp(<><Grab /><MiniPlayer /></>);
  act(() => p.playList([tr(1), tr(2), tr(3), tr(4)], 0));
  await userEvent.click(await screen.findByText("歌1"));
  const slider = await screen.findByRole("slider", { name: "进度" });
  act(() => {
    nativeInputValueSetter.call(slider, "60");
    slider.dispatchEvent(new Event("input", { bubbles: true }));
  });
  // Still just a preview: dragging alone must not issue a seek (which would
  // mean a burst of range requests while scrubbing on a mobile connection).
  expect(audio.currentTime).toBe(0);

  act(() => slider.dispatchEvent(new Event("change", { bubbles: true })));
  expect(audio.currentTime).toBe(60);
});

test("an in-flight favorite request does not land on a track the user has already left", async () => {
  let rejectPut!: (e: Error) => void;
  const pending = new Promise<Response>((_resolve, reject) => {
    rejectPut = reject;
  });
  renderWithApp(<><Grab /><MiniPlayer /></>);
  act(() => p.playList([tr(1), tr(2), tr(3)], 0));
  await userEvent.click(await screen.findByText("歌1"));
  const dialog = await screen.findByRole("dialog", { name: "正在播放" });

  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init: RequestInit = {}) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.includes("/favorites/1") && (init.method ?? "GET").toUpperCase() === "PUT") return pending;
      return Promise.resolve(new Response(JSON.stringify({}), { status: 200, headers: { "Content-Type": "application/json" } }));
    }),
  );

  await userEvent.click(within(dialog).getByRole("button", { name: "收藏" }));
  // Move on to the next track before the favorite request settles.
  await userEvent.click(within(dialog).getByRole("button", { name: "下一首" }));
  await within(dialog).findByText("歌2");
  expect(within(dialog).getByRole("button", { name: "收藏" })).toBeInTheDocument();

  await act(async () => {
    rejectPut(new Error("network down"));
    await Promise.resolve();
    await Promise.resolve();
  });
  // The stale result (for track 1) must not repaint track 2's favorite
  // state, and its failure must not surface as an error on track 2 either.
  expect(within(dialog).getByRole("button", { name: "收藏" })).toBeInTheDocument();
  expect(within(dialog).queryByText(/操作失败|network down/)).toBeNull();
});

test("now playing resets the armed delete confirmation when the track changes", async () => {
  renderWithApp(<><Grab /><MiniPlayer /></>, { role: "admin" });
  act(() => p.playList([tr(1), tr(2), tr(3)], 0));
  await userEvent.click(await screen.findByText("歌1"));
  const dialog = await screen.findByRole("dialog", { name: "正在播放" });
  await userEvent.click(within(dialog).getByRole("button", { name: "删除歌曲" }));
  expect(within(dialog).getByText("确认删除")).toBeInTheDocument();

  await userEvent.click(within(dialog).getByRole("button", { name: "下一首" }));
  await within(dialog).findByText("歌2");
  expect(within(dialog).queryByText("确认删除")).toBeNull();
  expect(within(dialog).getByRole("button", { name: "删除歌曲" })).toBeInTheDocument();
});

test("settings changes quality and logs out", async () => {
  const { f } = renderWithApp(<><Grab /><SettingsPage /></>, { routes: { "POST /api/v1/auth/logout": () => ({ status: 204 }) } });
  await userEvent.selectOptions(await screen.findByLabelText("音质"), "saver");
  expect(localStorage.getItem("lark.quality")).toBe("saver");
  expect(p.quality).toBe("saver");
  await userEvent.click(screen.getByRole("button", { name: "退出登录" }));
  expect(f).toHaveBeenCalledWith("/api/v1/auth/logout", expect.objectContaining({ method: "POST" }));
});

// Position ticks must not re-render every track row.
// TrackList subscribes via usePlayer(); the probe below subscribes the same
// way and counts its renders. (A <Profiler> can't be used for this: when the
// Profiler itself bails out, React doesn't report context-driven re-renders
// of its children.)
let probeRenders = 0;
function RenderProbe() {
  usePlayer();
  probeRenders += 1;
  return null;
}

test("timeupdate ticks re-render the mini player but not usePlayer() consumers like a track list", async () => {
  probeRenders = 0;
  const tracks = [tr(1), tr(2), tr(3)];
  const { audio } = renderWithApp(
    <>
      <Grab />
      <MiniPlayer />
      <TrackList tracks={tracks} />
      <RenderProbe />
    </>,
  );
  act(() => p.playList(tracks, 0));
  await screen.findByRole("button", { name: "暂停" });
  const before = probeRenders;
  expect(before).toBeGreaterThan(0);
  for (let t = 1; t <= 10; t++) {
    audio.currentTime = t;
    act(() => audio.fire("timeupdate"));
  }
  expect(probeRenders).toBe(before);
  // …while the progress bar did follow the ticks.
  expect((document.querySelector(".mini-progress") as HTMLElement).style.width).toBe("5%");
});

// A drag that ends at its start value fires no
// "change"; the preview must still be dropped when the gesture ends.
test("the seek preview is dropped when a drag ends without a change", async () => {
  const { audio } = renderWithApp(<><Grab /><MiniPlayer /></>);
  act(() => p.playList([tr(1), tr(2)], 0));
  await userEvent.click(await screen.findByText("歌1"));
  const slider = (await screen.findByRole("slider", { name: "进度" })) as HTMLInputElement;
  act(() => {
    nativeInputValueSetter.call(slider, "60");
    slider.dispatchEvent(new Event("input", { bubbles: true }));
  });
  expect(screen.getByText("1:00")).toBeInTheDocument();
  act(() => slider.dispatchEvent(new Event("pointerup", { bubbles: true })));
  await waitFor(() => expect(slider.value).toBe("0"));
  expect(audio.currentTime).toBe(0); // no seek happened
  audio.currentTime = 7;
  act(() => audio.fire("timeupdate"));
  expect(slider.value).toBe("7"); // readout follows playback again
});

// Buffered play events go out before the session ends.
test("logout sends pending play events before the logout request", async () => {
  const { audio, f } = renderWithApp(<><Grab /><SettingsPage /></>, {
    routes: {
      "POST /api/v1/auth/logout": () => ({ status: 204 }),
      "POST /api/v1/events/play": (init) => ({ body: { accepted: JSON.parse(init.body as string).events.length } }),
    },
  });
  await screen.findByLabelText("音质");
  act(() => p.playList([tr(1), tr(2)], 0));
  for (const t of [1, 2, 3, 4]) {
    audio.currentTime = t;
    act(() => audio.fire("timeupdate"));
  }
  await userEvent.click(screen.getByRole("button", { name: "退出登录" }));
  await waitFor(() => expect(f).toHaveBeenCalledWith("/api/v1/auth/logout", expect.anything()));
  const urls = f.mock.calls.map((c) => String(c[0]));
  const ev = urls.indexOf("/api/v1/events/play");
  expect(ev).toBeGreaterThanOrEqual(0);
  expect(ev).toBeLessThan(urls.indexOf("/api/v1/auth/logout"));
});

// ---- Autoplay refused on open ----

const notAllowed = () => Promise.reject(Object.assign(new Error("blocked"), { name: "NotAllowedError" }));
const refusedFavorites = {
  onOpen: "shuffle_favorites" as const,
  routes: { "GET /api/v1/tracks/random": () => ({ body: { source: "favorites", tracks: [tr(7), tr(8), tr(9)] } }) },
  tweak: (a: { play: { mockImplementationOnce(f: () => Promise<void>): unknown } }) => a.play.mockImplementationOnce(notAllowed),
};

test("refused autoplay: the mini player shows the tap prompt", async () => {
  renderWithApp(<><Grab /><MiniPlayer /></>, refusedFavorites);
  const tap = await screen.findByRole("button", { name: "▶ 点一下播放你的收藏" });
  expect(tap.closest(".mini-info")).toBeNull(); // never nested in the mini-info button
  expect(screen.getByText("歌7")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "播放" })).toBeInTheDocument();
});

// The prompt has no click handler of its own — the capture-phase
// prime() starts playback, so tapping the prompt plays exactly once.
test("tapping the prompt itself starts the favorites queue exactly once", async () => {
  const { audio } = renderWithApp(<><Grab /><MiniPlayer /></>, refusedFavorites);
  const tap = await screen.findByRole("button", { name: "▶ 点一下播放你的收藏" });
  const src = audio.src;
  const plays = audio.play.mock.calls.length;
  await userEvent.click(tap);
  await waitFor(() => expect(screen.queryByRole("button", { name: "▶ 点一下播放你的收藏" })).toBeNull());
  expect(audio.play.mock.calls.length).toBe(plays + 1);
  expect(audio.pause).not.toHaveBeenCalled();
  expect(audio.src).toBe(src);
  expect(await screen.findByRole("button", { name: "暂停" })).toBeInTheDocument();
});

// The play button is the most likely first tap: the start made by the
// capture-phase prime() must not be undone by that same tap's toggle().
test("a first tap on ▶ starts the favorites queue and leaves it playing", async () => {
  const { audio } = renderWithApp(<><Grab /><MiniPlayer /></>, refusedFavorites);
  await screen.findByRole("button", { name: "▶ 点一下播放你的收藏" });
  const plays = audio.play.mock.calls.length;
  await userEvent.click(screen.getByRole("button", { name: "播放" }));
  expect(await screen.findByRole("button", { name: "暂停" })).toBeInTheDocument();
  expect(audio.paused).toBe(false);
  expect(audio.pause).not.toHaveBeenCalled();
  expect(audio.play.mock.calls.length).toBe(plays + 1);
});

// iOS: the touchend (which starts playback via prime()) and the click on ▶
// that follows it arrive in separate tasks — the click must not pause again.
test("iOS tap on ▶: touchend starts the queue, the later click doesn't pause it", async () => {
  const { audio } = renderWithApp(<><Grab /><MiniPlayer /></>, refusedFavorites);
  await screen.findByRole("button", { name: "▶ 点一下播放你的收藏" });
  const btn = screen.getByRole("button", { name: "播放" });
  const plays = audio.play.mock.calls.length;
  act(() => {
    btn.dispatchEvent(new Event("touchend", { bubbles: true, cancelable: true }));
  });
  await new Promise((r) => setTimeout(r, 50));
  act(() => btn.click());
  expect(await screen.findByRole("button", { name: "暂停" })).toBeInTheDocument();
  expect(audio.paused).toBe(false);
  expect(audio.pause).not.toHaveBeenCalled();
  expect(audio.play.mock.calls.length).toBe(plays + 1);
});

test("the mini player's ⏮: within the first 3 s the previous track, later a restart", async () => {
  const { audio } = renderWithApp(<><Grab /><MiniPlayer /></>);
  act(() => p.playList([tr(1), tr(2), tr(3)], 1));
  expect(await screen.findByText("歌2")).toBeInTheDocument();
  const mini = document.querySelector(".mini") as HTMLElement;
  // ⏮ ▶ ⏭, in that order.
  expect(within(mini).getAllByRole("button").slice(1).map((b) => b.getAttribute("aria-label"))).toEqual(["上一首", "暂停", "下一首"]);
  // Glyphs, not text: SVG icons that render the same in every font.
  for (const b of within(mini).getAllByRole("button").slice(1)) expect(b.querySelector("svg")).not.toBeNull();
  audio.currentTime = 12;
  await userEvent.click(within(mini).getByRole("button", { name: "上一首" }));
  expect(audio.currentTime).toBe(0);
  expect(p.current?.id).toBe(2);
  audio.currentTime = 2;
  await userEvent.click(within(mini).getByRole("button", { name: "上一首" }));
  expect(await screen.findByText("歌1")).toBeInTheDocument();
  expect(p.current?.id).toBe(1);
});
