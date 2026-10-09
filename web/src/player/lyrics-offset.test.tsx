import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { MiniPlayer } from "./MiniPlayer";
import { usePlayer, type Player } from "./PlayerProvider";
import { activeLine, forgetLyrics, formatOffset } from "./lyricsCache";
import { renderWithApp } from "../test/render";
import type { Track } from "../api/types";

let p!: Player;
function Probe() { p = usePlayer(); return null; }
const tr = (id: number) =>
  ({ id, title: id === 1 ? "甜蜜蜜" : `t${id}`, artist: "邓丽君", album: "Album1", duration_ms: 200000, codec: "mp3", lossless: false, bitrate: 320, favorite: false, disliked: false, broken: false, broken_reason: "" }) as Track;

const lines = [{ t_ms: 8620, text: "You were the shadow to my light" }, { t_ms: 12210, text: "Did you feel us?" }, { t_ms: 15910, text: "Another star" }];
// Shifted 2 s later: at 12.5 s the first line is still being sung.
const shifted = { found: true, id: 7, source: "lrclib", synced: true, lines, offset_ms: 2000 };
const unshifted = { ...shifted, offset_ms: 0 };

const nowDialog = () => screen.getByRole("dialog", { name: "正在播放" });
const openNow = () => fireEvent.click(screen.getByRole("button", { name: /甜蜜蜜/ }));
const miniArtistLine = () => document.querySelector(".mini-info .track-text .muted")!.textContent;
const offsetBodies = (f: ReturnType<typeof renderWithApp>["f"]) =>
  f.mock.calls.filter((c) => String(c[0]).endsWith("/tracks/1/lyrics/offset") && c[1]?.method === "PUT").map((c) => JSON.parse(String(c[1]!.body)));
const offsetPuts = (f: ReturnType<typeof renderWithApp>["f"]) => offsetBodies(f).map((b) => b.offset_ms);
const offsetLabel = () => document.querySelector(".lyrics-offset")?.textContent;
const sleep = (ms: number) => act(() => new Promise((r) => setTimeout(r, ms)));

beforeEach(() => forgetLyrics(1));

function at(audio: ReturnType<typeof renderWithApp>["audio"], s: number) {
  audio.currentTime = s;
  act(() => audio.fire("timeupdate"));
}

test("activeLine with an offset and formatOffset", () => {
  expect(activeLine(lines, 12.5, 2000)).toBe(0);
  expect(activeLine(lines, 12.5, 0)).toBe(1);
  expect(activeLine(lines, 12.5)).toBe(1);
  expect(activeLine(lines, 8, -1000)).toBe(0);
  expect(formatOffset(1500)).toBe("+1.5s");
  expect(formatOffset(-500)).toBe("−0.5s");
  expect(formatOffset(0)).toBe("0.0s");
});

test("the synced lyrics view applies the offset to the active line and to tap-to-seek", async () => {
  const { audio } = renderWithApp(<><Probe /><MiniPlayer /></>, { routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body: shifted }) } });
  act(() => p.playList([tr(1)], 0));
  openNow();
  const first = await within(nowDialog()).findByRole("button", { name: "You were the shadow to my light" });
  at(audio, 12.5);
  await waitFor(() => expect(first).toHaveAttribute("aria-current", "true"));
  expect(offsetLabel()).toBe("+2.0s");
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "Another star" }));
  expect(audio.currentTime).toBeCloseTo(17.91);
});

test("the cover-mode strip and the mini player's line apply the offset", async () => {
  localStorage.setItem("lark.nowView", "cover");
  const { audio } = renderWithApp(<><Probe /><MiniPlayer /></>, { routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body: shifted }) } });
  act(() => p.playList([tr(1)], 0));
  openNow();
  await waitFor(() => expect(document.querySelector(".now-lyric-strip")).not.toBeNull());
  at(audio, 12.5);
  await waitFor(() => expect(document.querySelector(".now-lyric-strip .active")!.textContent).toBe("You were the shadow to my light"));
  expect(document.querySelector(".now-lyric-strip .muted")!.textContent).toBe("Did you feel us?");
  expect(miniArtistLine()).toBe("You were the shadow to my light");
});

test("car lyrics: the Media Session title applies the offset", async () => {
  const sets: MediaMetadataInit[] = [];
  const ms = {
    setActionHandler: vi.fn(), setPositionState: vi.fn(), playbackState: "none",
    get metadata() { return sets.length ? { init: sets[sets.length - 1] } : null; },
    set metadata(m: { init: MediaMetadataInit } | null) { if (m) sets.push(m.init); },
  };
  Object.defineProperty(navigator, "mediaSession", { value: ms, configurable: true, writable: true });
  vi.stubGlobal("MediaMetadata", class { constructor(public init: MediaMetadataInit) {} });
  try {
    const { audio } = renderWithApp(<><Probe /><MiniPlayer /></>, { routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body: shifted }) } });
    await act(async () => {});
    act(() => p.playList([tr(1)], 0));
    await act(async () => {});
    at(audio, 10); // 8.62 + 2 = 10.62: not yet
    expect(sets[sets.length - 1].title).toBe("甜蜜蜜");
    at(audio, 12.5);
    expect(sets[sets.length - 1].title).toBe("You were the shadow to my light");
    at(audio, 14.3);
    expect(sets[sets.length - 1].title).toBe("Did you feel us?");
  } finally {
    delete (navigator as unknown as { mediaSession?: unknown }).mediaSession;
  }
});

test("−0.5s / +0.5s shift at once everywhere and save once, debounced; reset shows only when shifted", async () => {
  const { audio, f } = renderWithApp(<><Probe /><MiniPlayer /></>, {
    routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body: unshifted }), "PUT /api/v1/tracks/1/lyrics/offset": () => ({ status: 204 }) },
  });
  act(() => p.playList([tr(1)], 0));
  openNow();
  await within(nowDialog()).findByRole("button", { name: "Did you feel us?" });
  expect(within(nowDialog()).queryByRole("button", { name: "复位" })).toBeNull();
  at(audio, 12.5);
  await waitFor(() => expect(miniArtistLine()).toBe("Did you feel us?"));
  const later = within(nowDialog()).getByRole("button", { name: "歌词延后 0.5 秒" });
  fireEvent.click(later);
  fireEvent.click(later);
  expect(offsetLabel()).toBe("+1.0s");
  await waitFor(() => expect(miniArtistLine()).toBe("You were the shadow to my light")); // 12.21 + 1 > 12.5
  expect(offsetPuts(f)).toEqual([]); // not yet
  await waitFor(() => expect(offsetPuts(f)).toEqual([1000]), { timeout: 2000 });
  expect(offsetBodies(f)[0]).toEqual({ offset_ms: 1000, lyrics_id: 7 }); // names the lyrics it shifts
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "歌词提前 0.5 秒" }));
  expect(offsetLabel()).toBe("+0.5s");
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "复位" }));
  expect(within(nowDialog()).queryByRole("button", { name: "复位" })).toBeNull();
  expect(within(nowDialog()).getByText("已复位")).toBeInTheDocument();
  await waitFor(() => expect(offsetPuts(f)).toEqual([1000, 0]), { timeout: 2000 });
  // The reset can be taken back: the previous shift, saved at once.
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "撤销" }));
  expect(offsetLabel()).toBe("+0.5s");
  await waitFor(() => expect(offsetPuts(f)).toEqual([1000, 0, 500]));
});

test("long-press a line aligns it to now and saves at once; a short tap still seeks; right-click aligns too", async () => {
  const { audio, f } = renderWithApp(<><Probe /><MiniPlayer /></>, {
    routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body: unshifted }), "PUT /api/v1/tracks/1/lyrics/offset": () => ({ status: 204 }) },
  });
  act(() => p.playList([tr(1)], 0));
  openNow();
  const line = await within(nowDialog()).findByRole("button", { name: "Did you feel us?" });
  // Short tap: seeks, no offset.
  at(audio, 20);
  fireEvent.pointerDown(line, { pointerType: "touch" });
  await sleep(100);
  fireEvent.pointerUp(line, { pointerType: "touch" });
  fireEvent.click(line);
  expect(audio.currentTime).toBeCloseTo(12.21);
  expect(offsetPuts(f)).toEqual([]);
  // Long press starting at 20 s while the song plays on: the position at the
  // press counts. offset = 20000 − 12210 → 7790, rounded to 7800; no seek.
  at(audio, 20);
  fireEvent.pointerDown(line, { pointerType: "touch" });
  await sleep(300);
  at(audio, 20.4);
  await sleep(300);
  fireEvent.pointerUp(line, { pointerType: "touch" });
  fireEvent.click(line);
  expect(audio.currentTime).toBe(20.4);
  await waitFor(() => expect(offsetPuts(f)).toEqual([7800]));
  expect(offsetLabel()).toBe("+7.8s");
  expect(within(nowDialog()).getByText("已对齐")).toBeInTheDocument();
  at(audio, 20.1); // rounded to 100 ms: the line is current from 20.01 s
  await waitFor(() => expect(line).toHaveAttribute("aria-current", "true"));
  // The alignment can be taken back.
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "撤销" }));
  expect(offsetLabel()).toBe("0.0s");
  await waitFor(() => expect(offsetPuts(f)).toEqual([7800, 0]));
  // A long press whose click never comes (released elsewhere) doesn't eat the next tap.
  at(audio, 30);
  fireEvent.pointerDown(line, { pointerType: "touch" });
  await sleep(600);
  fireEvent.pointerLeave(line, { pointerType: "touch" });
  await waitFor(() => expect(offsetPuts(f)).toHaveLength(3));
  fireEvent.pointerDown(line, { pointerType: "touch" });
  fireEvent.pointerUp(line, { pointerType: "touch" });
  fireEvent.click(line);
  expect(audio.currentTime).toBeCloseTo((12210 + offsetPuts(f)[2]) / 1000);
  // Right-click (the context menu) aligns too.
  at(audio, 9);
  fireEvent.contextMenu(within(nowDialog()).getByRole("button", { name: "You were the shadow to my light" }));
  await waitFor(() => expect(offsetPuts(f)).toHaveLength(4));
  expect(offsetPuts(f)[3]).toBe(400); // 9000 − 8620 = 380 → 400
});

test("歌词不对: after the inline confirm the next version shows at once and can be undone for 10 s; with none left it says it was reported", async () => {
  let wrong: unknown = { found: true, id: 8, source: "netease", synced: false, text: "另一版本", offset_ms: 0, report_id: 3 };
  const post = vi.fn(() => ({ body: wrong }));
  const undo = vi.fn((_init?: RequestInit) => ({ body: shifted }));
  const { f } = renderWithApp(<><Probe /><MiniPlayer /></>, {
    routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body: shifted }), "POST /api/v1/tracks/1/lyrics/wrong": post, "POST /api/v1/tracks/1/lyrics/wrong/undo": undo },
  });
  act(() => p.playList([tr(1)], 0));
  openNow();
  await within(nowDialog()).findByRole("button", { name: "Another star" });
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "歌词不对" }));
  expect(post).not.toHaveBeenCalled();
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "是，不对" }));
  expect(await within(nowDialog()).findByText("另一版本")).toBeInTheDocument();
  expect(within(nowDialog()).getByText("已换成另一版本歌词")).toBeInTheDocument();
  expect(JSON.parse(String(f.mock.calls.find((c) => String(c[0]).endsWith("/lyrics/wrong"))![1]!.body))).toEqual({ lyrics_id: 7 });
  // Undo brings the reported lyrics back.
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "撤销" }));
  expect(await within(nowDialog()).findByRole("button", { name: "Another star" })).toBeInTheDocument();
  expect(within(nowDialog()).getByText("已撤销")).toBeInTheDocument();
  expect(JSON.parse(String(undo.mock.calls[0][0]?.body ?? "{}"))).toEqual({ report_id: 3 });
  const gets = f.mock.calls.filter((c) => String(c[0]).endsWith("/tracks/1/lyrics") && (c[1]?.method ?? "GET") === "GET").length;
  // None left.
  wrong = { found: false, synced: false, offset_ms: 0, report_id: 4 };
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "歌词不对" }));
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "是，不对" }));
  expect(await within(nowDialog()).findByText("已标记，稍后自动修复")).toBeInTheDocument();
  expect(within(nowDialog()).getByText("暂无歌词")).toBeInTheDocument();
  expect(f.mock.calls.filter((c) => String(c[0]).endsWith("/tracks/1/lyrics") && (c[1]?.method ?? "GET") === "GET").length).toBe(gets); // the answer is used as is
});

test("歌词不对 on lyrics someone replaced meanwhile: 409 lyrics_changed shows the current ones", async () => {
  let body: unknown = shifted;
  renderWithApp(<><Probe /><MiniPlayer /></>, {
    routes: {
      "GET /api/v1/tracks/1/lyrics": () => ({ body }),
      "POST /api/v1/tracks/1/lyrics/wrong": () => ({ status: 409, body: { error: "changed", code: "lyrics_changed" } }),
    },
  });
  act(() => p.playList([tr(1)], 0));
  openNow();
  await within(nowDialog()).findByRole("button", { name: "Another star" });
  body = { found: true, id: 9, source: "qq", synced: false, text: "现在的版本", offset_ms: 0 };
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "歌词不对" }));
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "是，不对" }));
  expect(await within(nowDialog()).findByText("现在的版本")).toBeInTheDocument();
  expect(within(nowDialog()).getByText("歌词已被更换，已刷新")).toBeInTheDocument();
});

test("admin: the lyrics sheet lists rejected lyrics (who, preview) and 恢复 lifts one", async () => {
  let rejected = [
    { id: 5, source: "netease", preview: "错的第一句\n错的第二句", reported_by: "kid", rejected_at: 1_800_000_000, restorable: true },
    { id: 6, source: "qq", preview: "", reported_by: null, rejected_at: 1_700_000_000, restorable: false },
  ];
  const del = vi.fn(() => {
    rejected = rejected.slice(1);
    return { status: 204 };
  });
  const { f } = renderWithApp(<><Probe /><MiniPlayer /></>, {
    role: "admin",
    routes: {
      "GET /api/v1/tracks/1/lyrics": () => ({ body: shifted }),
      "GET /api/v1/tracks/1/lyrics/candidates": () => ({ body: [] }),
      "GET /api/v1/tracks/1/lyrics/rejected": () => ({ body: rejected }),
      "DELETE /api/v1/tracks/1/lyrics/rejected/5": del,
    },
  });
  act(() => p.playList([tr(1)], 0));
  openNow();
  fireEvent.click(await screen.findByRole("button", { name: "更换歌词" }));
  const dlg = await screen.findByRole("dialog", { name: "更换歌词" });
  expect(await within(dlg).findByRole("heading", { name: "已拒绝的歌词" })).toBeInTheDocument();
  const row = within(dlg).getByText(/错的第一句/).closest(".lyrics-rejected")! as HTMLElement;
  expect(within(row).getByText(/kid/)).toBeInTheDocument();
  expect(within(dlg).getByText("（无预览）")).toBeInTheDocument();
  expect(within(dlg).getAllByRole("button", { name: "恢复" })).toHaveLength(2);
  fireEvent.click(within(row).getByRole("button", { name: "恢复" }));
  await waitFor(() => expect(del).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(within(dlg).queryByText(/错的第一句/)).toBeNull());
  expect(f.mock.calls.filter((c) => String(c[0]).endsWith("/lyrics/candidates")).length).toBe(2); // reloaded
});
