import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { MiniPlayer } from "./MiniPlayer";
import { usePlayer, type Player } from "./PlayerProvider";
import { activeLine, forgetLyrics, getLyrics, isBlankLine } from "./lyricsCache";
import { TrackList } from "../components/TrackList";
import { renderWithApp } from "../test/render";
import type { Track } from "../api/types";

let p!: Player;
function Probe() { p = usePlayer(); return null; }
const tr = (id: number) =>
  ({ id, title: id === 1 ? "甜蜜蜜" : `t${id}`, artist: "邓丽君", album: "", duration_ms: 200000, codec: "mp3", lossless: false, bitrate: 320, favorite: false, disliked: false, broken: false, broken_reason: "" }) as Track;

const synced = { found: true, source: "lrclib", synced: true, lines: [{ t_ms: 8620, text: "You were the shadow to my light" }, { t_ms: 12210, text: "Did you feel us?" }, { t_ms: 15910, text: "Another star" }] };

const lyricsCalls = (f: ReturnType<typeof renderWithApp>["f"], id: number) =>
  f.mock.calls.filter((c) => String(c[0]).split("?")[0].endsWith(`/tracks/${id}/lyrics`) && (c[1]?.method ?? "GET") === "GET").length;

beforeEach(() => {
  for (const id of [1, 2, 3, 4]) forgetLyrics(id);
});

const nowDialog = () => screen.getByRole("dialog", { name: "正在播放" });
const openNow = (title: RegExp = /甜蜜蜜/) => fireEvent.click(screen.getByRole("button", { name: title }));
const plain = { found: true, source: "embedded", synced: false, text: "第一行\n第二行" };
const miniArtistLine = () => document.querySelector(".mini-info .track-text .muted")!.textContent;

test("Now Playing opens on the synced lyrics when the track has them; the current line follows playback; a tap seeks", async () => {
  const { audio } = renderWithApp(<><Probe /><MiniPlayer /></>, { routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body: synced }) } });
  act(() => p.playList([tr(1)], 0));
  openNow();
  const line = await within(nowDialog()).findByRole("button", { name: "Did you feel us?" });
  expect(within(nowDialog()).queryByRole("button", { name: "歌词" })).toBeNull(); // not on the cover
  expect(within(nowDialog()).getByRole("button", { name: "封面" })).toBeInTheDocument();
  audio.currentTime = 12.5;
  act(() => audio.fire("timeupdate"));
  await waitFor(() => expect(line).toHaveAttribute("aria-current", "true"));
  expect(line).toHaveClass("active");
  expect(within(nowDialog()).getByRole("button", { name: "Another star" })).not.toHaveAttribute("aria-current");
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "Another star" }));
  expect(audio.currentTime).toBeCloseTo(15.91);
});

test("Now Playing opens on plain lyrics too, and on the cover when there are none", async () => {
  let body: unknown = plain;
  renderWithApp(<><Probe /><MiniPlayer /></>, {
    routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body }), "GET /api/v1/tracks/2/lyrics": () => ({ body: { found: false, synced: false } }) },
  });
  act(() => p.playList([tr(1), tr(2)], 0));
  openNow();
  expect(await within(nowDialog()).findByText("第一行")).toBeInTheDocument();
  expect(within(nowDialog()).queryByRole("button", { name: "第一行" })).toBeNull();
  act(() => p.next());
  expect(await within(nowDialog()).findByRole("button", { name: "歌词" })).toBeInTheDocument(); // the cover
  expect(within(nowDialog()).queryByText("第一行")).toBeNull();
  expect(within(nowDialog()).queryByRole("button", { name: "封面" })).toBeNull();
  body = null;
});

test("on a track without lyrics, tapping the cover still shows the lyrics view and asks again", async () => {
  let body: unknown = { found: false, synced: false };
  const { f } = renderWithApp(<><Probe /><MiniPlayer /></>, { routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body }) } });
  act(() => p.playList([tr(1)], 0));
  openNow();
  const cover = await within(nowDialog()).findByRole("button", { name: "歌词" });
  await waitFor(() => expect(lyricsCalls(f, 1)).toBeGreaterThanOrEqual(1));
  const before = lyricsCalls(f, 1);
  fireEvent.click(cover);
  expect(await within(nowDialog()).findByText("暂无歌词")).toBeInTheDocument();
  await waitFor(() => expect(lyricsCalls(f, 1)).toBe(before + 1)); // a miss is asked again on a look

  fireEvent.click(within(nowDialog()).getByRole("button", { name: "封面" }));
  body = { found: false, instrumental: true, synced: false };
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "歌词" }));
  expect(await within(nowDialog()).findByText("纯音乐，没有歌词")).toBeInTheDocument();

  fireEvent.click(within(nowDialog()).getByRole("button", { name: "封面" }));
  body = plain;
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "歌词" }));
  expect(await within(nowDialog()).findByText("第一行")).toBeInTheDocument();
  expect(within(nowDialog()).getByText("第二行")).toBeInTheDocument();
});

test("the last explicit choice is remembered on this device and wins over the default", async () => {
  renderWithApp(<><Probe /><MiniPlayer /></>, {
    routes: {
      "GET /api/v1/tracks/1/lyrics": () => ({ body: synced }),
      "GET /api/v1/tracks/2/lyrics": () => ({ body: { found: true, source: "lrclib", synced: true, lines: [{ t_ms: 1000, text: "second song" }] } }),
      "GET /api/v1/tracks/3/lyrics": () => ({ body: { found: false, synced: false } }),
      "GET /api/v1/tracks/4/lyrics": () => ({ body: synced }),
    },
  });
  act(() => p.playList([tr(1), tr(2), tr(3), tr(4)], 0));
  openNow();
  await within(nowDialog()).findByRole("button", { name: "Another star" });
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "封面" }));
  expect(localStorage.getItem("lark.nowView")).toBe("cover");
  // Close and reopen: still the cover, though the track has lyrics.
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "收起" }));
  openNow();
  await waitFor(() => expect(document.querySelector(".now-lyric-strip")).not.toBeNull());
  expect(within(nowDialog()).getByRole("button", { name: "歌词" })).toBeInTheDocument();
  // ...and for the next track with lyrics.
  act(() => p.next());
  await waitFor(() => expect(within(nowDialog()).getByText("second song")).toBeInTheDocument()); // the strip for t2
  expect(within(nowDialog()).getByRole("button", { name: "歌词" })).toBeInTheDocument();
  // Choosing the lyrics brings them back, here and for later tracks that have them.
  fireEvent.click(within(nowDialog()).getByRole("button", { name: "歌词" }));
  expect(localStorage.getItem("lark.nowView")).toBe("lyrics");
  await within(nowDialog()).findByRole("button", { name: "second song" });
  act(() => p.next()); // t3: no lyrics → the cover
  expect(await within(nowDialog()).findByRole("button", { name: "歌词" })).toBeInTheDocument();
  act(() => p.next()); // t4: lyrics again
  expect(await within(nowDialog()).findByRole("button", { name: "Another star" })).toBeInTheDocument();
});

test("a stored choice that can't be read or written doesn't break Now Playing", async () => {
  // Only this key: the rest of the app has its own storage handling.
  const realGet = Storage.prototype.getItem;
  const realSet = Storage.prototype.setItem;
  const get = vi.spyOn(Storage.prototype, "getItem").mockImplementation(function (this: Storage, k: string) {
    if (k === "lark.nowView") throw new Error("denied");
    return realGet.call(this, k);
  });
  const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (this: Storage, k: string, v: string) {
    if (k === "lark.nowView") throw new Error("denied");
    realSet.call(this, k, v);
  });
  try {
    renderWithApp(<><Probe /><MiniPlayer /></>, { routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body: synced }) } });
    act(() => p.playList([tr(1)], 0));
    openNow();
    await within(nowDialog()).findByRole("button", { name: "Another star" });
    fireEvent.click(within(nowDialog()).getByRole("button", { name: "封面" }));
    expect(within(nowDialog()).getByRole("button", { name: "歌词" })).toBeInTheDocument();
  } finally {
    get.mockRestore();
    set.mockRestore();
  }
});

test("switching tracks keeps the lyrics view up (no flash of the cover) and keeps the same audio", async () => {
  const { audio } = renderWithApp(<><Probe /><MiniPlayer /></>, {
    routes: {
      "GET /api/v1/tracks/1/lyrics": () => ({ body: synced }),
      "GET /api/v1/tracks/2/lyrics": () => ({ body: { found: true, source: "lrclib", synced: true, lines: [{ t_ms: 1000, text: "second song" }] } }),
    },
  });
  act(() => p.playList([tr(1), tr(2)], 0));
  openNow();
  await within(nowDialog()).findByRole("button", { name: "Another star" });
  const dialog = nowDialog();
  act(() => p.next());
  expect(within(dialog).queryByRole("button", { name: "歌词" })).toBeNull(); // still the lyrics view, not the cover
  expect(await within(dialog).findByRole("button", { name: "second song" })).toBeInTheDocument();
  expect(nowDialog()).toBe(dialog); // the overlay wasn't remounted
  expect(audio.src).toContain("/tracks/2/stream");
});

test("opening Now Playing before the lyrics have loaded doesn't flash the cover", async () => {
  renderWithApp(<><Probe /><MiniPlayer /></>, { routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body: synced }) } });
  act(() => p.playList([tr(1)], 0));
  openNow(); // the mini player's request hasn't answered yet
  expect(within(nowDialog()).queryByRole("button", { name: "歌词" })).toBeNull();
  expect(within(nowDialog()).getByText("正在找歌词…")).toBeInTheDocument();
  expect(await within(nowDialog()).findByRole("button", { name: "Another star" })).toBeInTheDocument();
});

test("in cover mode, the current and the next line show under the title and follow playback", async () => {
  localStorage.setItem("lark.nowView", "cover");
  const { audio } = renderWithApp(<><Probe /><MiniPlayer /></>, { routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body: synced }) } });
  act(() => p.playList([tr(1)], 0));
  openNow();
  const strip = await waitFor(() => {
    const el = document.querySelector<HTMLElement>(".now-lyric-strip");
    expect(el).not.toBeNull();
    return el!;
  });
  expect(within(nowDialog()).getByRole("button", { name: "歌词" })).toBeInTheDocument(); // on the cover
  expect(within(strip).getByText("You were the shadow to my light")).not.toHaveAttribute("aria-current"); // before the first line: it's next
  audio.currentTime = 12.5;
  act(() => audio.fire("timeupdate"));
  await waitFor(() => expect(within(strip).getByText("Did you feel us?")).toHaveAttribute("aria-current", "true"));
  expect(within(strip).getByText("Did you feel us?")).toHaveClass("active");
  expect(within(strip).getByText("Another star")).not.toHaveClass("active");
  expect(within(strip).queryByText("You were the shadow to my light")).toBeNull();
});

test("no lyric strip under the cover for plain lyrics or none", async () => {
  localStorage.setItem("lark.nowView", "cover");
  renderWithApp(<><Probe /><MiniPlayer /></>, { routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body: plain }) } });
  act(() => p.playList([tr(1)], 0));
  openNow();
  await waitFor(() => expect(within(nowDialog()).getByRole("button", { name: "歌词" })).toBeInTheDocument());
  await act(async () => {});
  expect(document.querySelector(".now-lyric-strip")).toBeNull();
});

test("the mini player shows the current synced line in place of the artist; with car lyrics off the lock screen keeps the song", async () => {
  const ms = { setActionHandler: vi.fn(), setPositionState: vi.fn(), metadata: null as null | { init: MediaMetadataInit }, playbackState: "none" };
  Object.defineProperty(navigator, "mediaSession", { value: ms, configurable: true, writable: true });
  vi.stubGlobal("MediaMetadata", class { constructor(public init: MediaMetadataInit) {} });
  try {
    const { audio } = renderWithApp(<><Probe /><MiniPlayer /></>, {
      routes: {
        "GET /api/v1/me/preferences": () => ({ body: { language: null, on_open: "resume", car_lyrics: false } }),
        "GET /api/v1/tracks/1/lyrics": () => ({ body: synced }),
        "GET /api/v1/tracks/2/lyrics": () => ({ body: { found: false, synced: false } }),
      },
    });
    await act(async () => {}); // preferences answered
    act(() => p.playList([tr(1), tr(2)], 0));
    expect(miniArtistLine()).toBe("邓丽君"); // before the first line
    audio.currentTime = 12.5;
    act(() => audio.fire("timeupdate"));
    await waitFor(() => expect(miniArtistLine()).toBe("Did you feel us?"));
    expect(ms.metadata!.init.artist).toBe("邓丽君");
    expect(ms.metadata!.init.title).toBe("甜蜜蜜");
    act(() => p.next()); // no lyrics: back to the artist
    await act(async () => {});
    audio.currentTime = 12.5;
    act(() => audio.fire("timeupdate"));
    expect(miniArtistLine()).toBe("邓丽君");
    expect(ms.metadata!.init.artist).toBe("邓丽君");
  } finally {
    delete (navigator as unknown as { mediaSession?: unknown }).mediaSession;
  }
});

test("the next track's lyrics are prefetched with its audio and reused", async () => {
  const { f } = renderWithApp(<><Probe /><MiniPlayer /></>, {
    routes: {
      "GET /api/v1/tracks/1/lyrics": () => ({ body: synced }),
      "GET /api/v1/tracks/2/lyrics": () => ({ body: { found: true, source: "lrclib", synced: false, text: "second song words" } }),
    },
  });
  act(() => p.playList([tr(1), tr(2)], 0));
  await waitFor(() => expect(lyricsCalls(f, 2)).toBe(1));
  act(() => p.next());
  openNow(/t2/);
  expect(await within(nowDialog()).findByText("second song words")).toBeInTheDocument();
  expect(lyricsCalls(f, 2)).toBe(1);
  expect(lyricsCalls(f, 1)).toBe(1); // the current track's, once, for the mini player and Now Playing alike
});

test("a failed lyrics request is not cached", async () => {
  let status = 500;
  const { f } = renderWithApp(<><Probe /><MiniPlayer /></>, {
    routes: { "GET /api/v1/tracks/1/lyrics": () => (status === 500 ? { status, body: { error: "boom" } } : { body: synced }) },
  });
  act(() => p.playList([tr(1)], 0));
  await waitFor(() => expect(lyricsCalls(f, 1)).toBe(1));
  status = 200;
  openNow(); // opening Now Playing looks again
  expect(await within(nowDialog()).findByRole("button", { name: "Another star" })).toBeInTheDocument();
  expect(lyricsCalls(f, 1)).toBe(2);
});

const candidates = [
  { id: 3, source: "lrclib", synced: true, selected: true, preview: "You were the shadow\nDid you feel us?\nAnother star" },
  { id: 9, source: "netease", synced: false, selected: false, preview: "甜蜜蜜 你笑得甜蜜蜜" },
];

test("admin change lyrics: lists candidates, picks one, searches again", async () => {
  const { f } = renderWithApp(<><Probe /><MiniPlayer /></>, {
    role: "admin",
    routes: {
      "GET /api/v1/tracks/1/lyrics": () => ({ body: synced }),
      "GET /api/v1/tracks/1/lyrics/candidates": () => ({ body: candidates }),
      "PUT /api/v1/tracks/1/lyrics": () => ({ status: 204 }),
      "POST /api/v1/tracks/1/lyrics/refresh": () => ({ body: synced }),
    },
  });
  act(() => p.playList([tr(1)], 0));
  fireEvent.click(screen.getByRole("button", { name: /甜蜜蜜/ }));
  fireEvent.click(await screen.findByRole("button", { name: "更换歌词" }));
  const dlg = await screen.findByRole("dialog", { name: "更换歌词" });
  const first = await within(dlg).findByRole("button", { name: /LRCLIB/ });
  expect(first).toHaveAttribute("aria-pressed", "true");
  expect(within(first).getByText("滚动")).toBeInTheDocument();
  const second = within(dlg).getByRole("button", { name: /网易云音乐/ });
  expect(second).toHaveAttribute("aria-pressed", "false");
  expect(within(second).queryByText("滚动")).toBeNull();

  fireEvent.click(within(dlg).getByRole("button", { name: "搜索" }));
  await waitFor(() => expect(f.mock.calls.filter((c) => String(c[0]).endsWith("/tracks/1/lyrics/candidates")).length).toBe(2));
  expect(f.mock.calls.some((c) => String(c[0]).endsWith("/tracks/1/lyrics/refresh") && c[1]?.method === "POST")).toBe(true);

  fireEvent.click(within(dlg).getByRole("button", { name: /网易云音乐/ }));
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "更换歌词" })).toBeNull());
  const put = f.mock.calls.find((c) => String(c[0]).endsWith("/tracks/1/lyrics") && c[1]?.method === "PUT")!;
  expect(JSON.parse(put[1]!.body as string)).toEqual({ candidate_id: 9 });

  // The search and the pick each dropped the cached lyrics: the view asked again.
  await waitFor(() => expect(lyricsCalls(f, 1)).toBe(3));
  expect(await within(screen.getByRole("dialog", { name: "正在播放" })).findByRole("button", { name: "Another star" })).toBeInTheDocument();
});

test("admin: the lyrics sheet searches with edited title/artist and reloads the candidates", async () => {
  const refresh = vi.fn(() => ({ body: synced }));
  const cands = vi.fn(() => ({ body: candidates }));
  renderWithApp(<><Probe /><MiniPlayer /></>, {
    role: "admin",
    routes: {
      "GET /api/v1/tracks/1/lyrics": () => ({ body: synced }),
      "GET /api/v1/tracks/1/lyrics/candidates": cands,
      "POST /api/v1/tracks/1/lyrics/refresh": refresh,
    },
  });
  act(() => p.playList([tr(1)], 0));
  fireEvent.click(screen.getByRole("button", { name: /甜蜜蜜/ }));
  fireEvent.click(await screen.findByRole("button", { name: "更换歌词" }));
  const dlg = await screen.findByRole("dialog", { name: "更换歌词" });
  expect(within(dlg).getByLabelText("歌名")).toHaveValue("甜蜜蜜");
  expect(within(dlg).getByLabelText("歌手")).toHaveValue("邓丽君");
  fireEvent.click(within(dlg).getByRole("button", { name: "搜索" }));
  await vi.waitFor(() => expect(refresh).toHaveBeenCalledTimes(1));
  expect((refresh.mock.calls[0] as unknown[] as RequestInit[])[0].body).toBeUndefined(); // nothing edited: server cleans
  await vi.waitFor(() => expect(within(dlg).getByRole("button", { name: "搜索" })).toBeEnabled()); // the first search has finished
  fireEvent.change(within(dlg).getByLabelText("歌名"), { target: { value: " 女儿情 " } });
  fireEvent.click(within(dlg).getByRole("button", { name: "搜索" }));
  await vi.waitFor(() => expect(refresh).toHaveBeenCalledTimes(2));
  expect(JSON.parse(String((refresh.mock.calls[1] as unknown[] as RequestInit[])[0].body))).toEqual({ title: "女儿情" });
  await vi.waitFor(() => expect(cands.mock.calls.length).toBeGreaterThanOrEqual(3)); // open + two reloads
  fireEvent.change(within(dlg).getByLabelText("歌名"), { target: { value: "" } });
  expect(within(dlg).getByRole("button", { name: "搜索" })).toBeDisabled();
});

test("admin: untouched fields are not sent even when the track's own title/artist carry stray spaces", async () => {
  const refresh = vi.fn(() => ({ body: synced }));
  renderWithApp(<><Probe /><MiniPlayer /></>, {
    role: "admin",
    routes: {
      "GET /api/v1/tracks/1/lyrics": () => ({ body: synced }),
      "GET /api/v1/tracks/1/lyrics/candidates": () => ({ body: candidates }),
      "POST /api/v1/tracks/1/lyrics/refresh": refresh,
    },
  });
  act(() => p.playList([{ ...tr(1), title: " 甜蜜蜜 ", artist: "邓丽君 " }], 0));
  fireEvent.click(screen.getByRole("button", { name: /甜蜜蜜/ }));
  fireEvent.click(await screen.findByRole("button", { name: "更换歌词" }));
  const dlg = await screen.findByRole("dialog", { name: "更换歌词" });
  fireEvent.click(within(dlg).getByRole("button", { name: "搜索" }));
  await vi.waitFor(() => expect(refresh).toHaveBeenCalledTimes(1));
  expect((refresh.mock.calls[0] as unknown[] as RequestInit[])[0].body).toBeUndefined();
});

test("admin track menu offers change lyrics", async () => {
  const { f } = renderWithApp(<TrackList tracks={[tr(1)]} />, { role: "admin", routes: { "GET /api/v1/tracks/1/lyrics/candidates": () => ({ body: candidates }) } });
  fireEvent.click(screen.getByRole("button", { name: "更多：甜蜜蜜" }));
  fireEvent.click(await screen.findByRole("menuitem", { name: "更换歌词" }));
  const dlg = await screen.findByRole("dialog", { name: "更换歌词" });
  expect(await within(dlg).findByRole("button", { name: /网易云音乐/ })).toBeInTheDocument();
  expect(f.mock.calls.some((c) => String(c[0]).endsWith("/tracks/1/lyrics/candidates"))).toBe(true);
});

test("members don't see change lyrics", async () => {
  renderWithApp(<><Probe /><MiniPlayer /><TrackList tracks={[tr(1)]} /></>, { role: "member" });
  act(() => p.playList([tr(1)], 0));
  fireEvent.click(screen.getByRole("button", { name: "更多：甜蜜蜜" }));
  await screen.findByRole("menu");
  expect(screen.queryByRole("menuitem", { name: "更换歌词" })).toBeNull();
  fireEvent.click(document.querySelector(".mini-info")!); // open Now Playing
  expect(await screen.findByRole("dialog", { name: "正在播放" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "更换歌词" })).toBeNull();
});

test("activeLine", () => {
  const lines = [{ t_ms: 1000 }, { t_ms: 2000 }];
  expect(activeLine(lines, 0.5)).toBe(-1);
  expect(activeLine(lines, 1.5)).toBe(0);
  expect(activeLine(lines, 9)).toBe(1);
  expect(activeLine([], 9)).toBe(-1);
});

test("a miss is not cached for the session, but concurrent asks share one request", async () => {
  let body: unknown = { found: false, synced: false };
  const { f } = renderWithApp(<Probe />, { routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body }) } });
  const [a, b] = await Promise.all([getLyrics(1), getLyrics(1)]);
  expect(a.found).toBe(false);
  expect(b).toBe(a);
  expect(lyricsCalls(f, 1)).toBe(1);
  body = synced; // e.g. the background prefetch found them meanwhile
  expect((await getLyrics(1)).found).toBe(true);
  expect(lyricsCalls(f, 1)).toBe(2);
  await getLyrics(1);
  expect(lyricsCalls(f, 1)).toBe(2); // a hit stays cached
});

async function openPicker() {
  act(() => p.playList([tr(1)], 0));
  fireEvent.click(screen.getByRole("button", { name: /甜蜜蜜/ }));
  fireEvent.click(await screen.findByRole("button", { name: "更换歌词" }));
  return screen.findByRole("dialog", { name: "更换歌词" });
}

test("admin: broad search sends broad with the edited title and reloads the candidates", async () => {
  const refresh = vi.fn(() => ({ body: synced }));
  const cands = vi.fn(() => ({ body: candidates }));
  renderWithApp(<><Probe /><MiniPlayer /></>, {
    role: "admin",
    routes: {
      "GET /api/v1/tracks/1/lyrics": () => ({ body: synced }),
      "GET /api/v1/tracks/1/lyrics/candidates": cands,
      "POST /api/v1/tracks/1/lyrics/refresh": refresh,
    },
  });
  const dlg = await openPicker();
  fireEvent.click(within(dlg).getByRole("button", { name: "宽搜索" }));
  await vi.waitFor(() => expect(refresh).toHaveBeenCalledTimes(1));
  expect(JSON.parse(String((refresh.mock.calls[0] as unknown[] as RequestInit[])[0].body))).toEqual({ broad: true });
  await vi.waitFor(() => expect(within(dlg).getByRole("button", { name: "宽搜索" })).toBeEnabled());
  fireEvent.change(within(dlg).getByLabelText("歌名"), { target: { value: "甜蜜蜜 " } });
  fireEvent.change(within(dlg).getByLabelText("歌手"), { target: { value: "" } });
  fireEvent.click(within(dlg).getByRole("button", { name: "宽搜索" }));
  await vi.waitFor(() => expect(refresh).toHaveBeenCalledTimes(2));
  expect(JSON.parse(String((refresh.mock.calls[1] as unknown[] as RequestInit[])[0].body))).toEqual({ broad: true, artist: "" });
  await vi.waitFor(() => expect(cands.mock.calls.length).toBeGreaterThanOrEqual(3));
  fireEvent.change(within(dlg).getByLabelText("歌名"), { target: { value: " " } });
  expect(within(dlg).getByRole("button", { name: "宽搜索" })).toBeDisabled();
});

test("admin: a candidate is deleted only after the inline confirm, and the lyrics view reloads", async () => {
  let list = candidates;
  const del = vi.fn(() => {
    list = [{ ...candidates[1], selected: true }];
    return { status: 204 };
  });
  const { f } = renderWithApp(<><Probe /><MiniPlayer /></>, {
    role: "admin",
    routes: {
      "GET /api/v1/tracks/1/lyrics": () => ({ body: list.length === 2 ? synced : plain }),
      "GET /api/v1/tracks/1/lyrics/candidates": () => ({ body: list }),
      "DELETE /api/v1/tracks/1/lyrics/candidates/3": del,
    },
  });
  const dlg = await openPicker();
  await within(dlg).findByRole("button", { name: /LRCLIB/ });
  const rows = () => Array.from(dlg.querySelectorAll<HTMLElement>(".lyrics-cand"));
  expect(rows()).toHaveLength(2);
  expect(within(rows()[0]).getByText("滚动")).toBeInTheDocument(); // the synced badge stays
  fireEvent.click(within(rows()[0]).getByRole("button", { name: "删除" }));
  expect(del).not.toHaveBeenCalled();
  expect(within(rows()[0]).getByText("删除这份歌词？")).toBeInTheDocument();
  fireEvent.click(within(rows()[0]).getByRole("button", { name: "保留" })); // changed my mind
  expect(within(rows()[0]).queryByText("删除这份歌词？")).toBeNull();
  expect(del).not.toHaveBeenCalled();

  fireEvent.click(within(rows()[0]).getByRole("button", { name: "删除" }));
  fireEvent.click(within(rows()[0]).getByRole("button", { name: "确认删除" }));
  await vi.waitFor(() => expect(del).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(rows()).toHaveLength(1));
  expect(within(dlg).queryByRole("button", { name: /LRCLIB/ })).toBeNull();
  expect(within(dlg).getByRole("button", { name: /网易云音乐/ })).toHaveAttribute("aria-pressed", "true");
  await waitFor(() => expect(lyricsCalls(f, 1)).toBe(2)); // the cache was dropped and the view asked again
  fireEvent.click(within(dlg).getByRole("button", { name: "取消" }));
  expect(await within(nowDialog()).findByText("第一行")).toBeInTheDocument();
});

test("admin: this song has no lyrics", async () => {
  let body: unknown = synced;
  const put = vi.fn(() => {
    body = { found: false, synced: false };
    return { status: 204 };
  });
  const { f } = renderWithApp(<><Probe /><MiniPlayer /></>, {
    role: "admin",
    routes: {
      "GET /api/v1/tracks/1/lyrics": () => ({ body }),
      "GET /api/v1/tracks/1/lyrics/candidates": () => ({ body: candidates }),
      "PUT /api/v1/tracks/1/lyrics": put,
    },
  });
  const dlg = await openPicker();
  fireEvent.click(await within(dlg).findByRole("button", { name: "这首没有歌词" }));
  expect(within(dlg).getByText("这首歌没有歌词？之后不再自动查找。")).toBeInTheDocument(); // asks first, in the sheet
  fireEvent.click(within(dlg).getByRole("button", { name: "不，继续找" }));
  expect(within(dlg).queryByText("这首歌没有歌词？之后不再自动查找。")).toBeNull();
  expect(put).not.toHaveBeenCalled();
  fireEvent.click(within(dlg).getByRole("button", { name: "这首没有歌词" }));
  fireEvent.click(within(dlg).getByRole("button", { name: "确认没有" }));
  await vi.waitFor(() => expect(put).toHaveBeenCalledTimes(1));
  expect(JSON.parse(String((put.mock.calls[0] as unknown[] as RequestInit[])[0].body))).toEqual({ none: true });
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "更换歌词" })).toBeNull());
  await waitFor(() => expect(lyricsCalls(f, 1)).toBe(2));
  expect(within(nowDialog()).queryByRole("button", { name: "Another star" })).toBeNull();
});

// ---- Car lyrics: the current line goes to the Bluetooth / lock-screen title ----

const carSynced = {
  found: true, source: "lrclib", synced: true,
  lines: [
    { t_ms: 8000, text: "Line one" },
    { t_ms: 10000, text: "" },
    { t_ms: 12000, text: "Line two" },
    { t_ms: 14000, text: " ♪ " },
    { t_ms: 15000, text: "(Instrumental)" },
    { t_ms: 16000, text: "Line three" },
  ],
};

// Every metadata assignment, in order (a getter/setter pair, so re-assigning
// the same content still counts).
function stubSession() {
  const sets: MediaMetadataInit[] = [];
  const ms = {
    setActionHandler: vi.fn(), setPositionState: vi.fn(), playbackState: "none",
    get metadata() { return sets.length ? { init: sets[sets.length - 1] } : null; },
    set metadata(m: { init: MediaMetadataInit } | null) { if (m) sets.push(m.init); },
  };
  Object.defineProperty(navigator, "mediaSession", { value: ms, configurable: true, writable: true });
  vi.stubGlobal("MediaMetadata", class { constructor(public init: MediaMetadataInit) {} });
  return { sets, last: () => sets[sets.length - 1], cleanup: () => { delete (navigator as unknown as { mediaSession?: unknown }).mediaSession; } };
}

const art = (id: number) => [
  { src: `${location.origin}/api/v1/tracks/${id}/cover?size=300`, sizes: "300x300", type: "image/jpeg" },
  { src: `${location.origin}/api/v1/tracks/${id}/cover?size=1000`, sizes: "1000x1000", type: "image/jpeg" },
];
const song1 = { title: "甜蜜蜜", artist: "邓丽君", album: "Album1", artwork: art(1) };
const trA = (id: number) => ({ ...tr(id), album: `Album${id}` }) as Track;

function at(audio: ReturnType<typeof renderWithApp>["audio"], s: number) {
  audio.currentTime = s;
  act(() => audio.fire("timeupdate"));
}

test("car lyrics (on by default): each new line becomes the title once, with song · artist as the artist and the same artwork", async () => {
  const s = stubSession();
  try {
    const { audio, f } = renderWithApp(<><Probe /><MiniPlayer /></>, { routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body: carSynced }) } });
    await act(async () => {});
    act(() => p.playList([trA(1)], 0));
    await act(async () => {});
    expect(s.last()).toEqual(song1); // before the first line: the song
    at(audio, 8.1);
    expect(s.last()).toEqual({ title: "Line one", artist: "甜蜜蜜 · 邓丽君", album: "Album1", artwork: art(1) });
    const n = s.sets.length;
    at(audio, 8.4); // same line: no churn
    at(audio, 9.9);
    expect(s.sets.length).toBe(n);
    at(audio, 10.5); // the blank line between: the song
    at(audio, 12.3);
    expect(s.last()).toEqual({ title: "Line two", artist: "甜蜜蜜 · 邓丽君", album: "Album1", artwork: art(1) });
    expect(s.sets.length).toBe(n + 2); // the blank line between, then "Line two"
    at(audio, 16.2);
    expect(s.last().title).toBe("Line three");
    expect(s.sets.filter((m) => m.title === "Line one")).toHaveLength(1);
    expect(s.sets.filter((m) => m.title === "Line two")).toHaveLength(1);
    expect(lyricsCalls(f, 1)).toBe(1); // the mini player's fetch, shared
  } finally {
    s.cleanup();
  }
});

test("car lyrics: blank and instrumental lines, and before the first line, show the song", async () => {
  const s = stubSession();
  try {
    const { audio } = renderWithApp(<><Probe /><MiniPlayer /></>, { routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body: carSynced }) } });
    await act(async () => {});
    act(() => p.playList([trA(1)], 0));
    await act(async () => {});
    at(audio, 8.5);
    expect(s.last().title).toBe("Line one");
    at(audio, 10.5); // empty line
    expect(s.last()).toEqual(song1);
    at(audio, 12.5);
    expect(s.last().title).toBe("Line two");
    at(audio, 14.5); // "♪"
    expect(s.last()).toEqual(song1);
    const n = s.sets.length;
    at(audio, 15.5); // "(Instrumental)": still the song, not set again
    expect(s.last()).toEqual(song1);
    expect(s.sets.length).toBe(n);
    at(audio, 3); // seek back before the first line
    expect(s.last()).toEqual(song1);
  } finally {
    s.cleanup();
  }
});

test("car lyrics off: the metadata is always the song", async () => {
  const s = stubSession();
  try {
    const { audio } = renderWithApp(<><Probe /><MiniPlayer /></>, {
      routes: {
        "GET /api/v1/me/preferences": () => ({ body: { language: null, on_open: "resume", car_lyrics: false } }),
        "GET /api/v1/tracks/1/lyrics": () => ({ body: carSynced }),
      },
    });
    await act(async () => {});
    act(() => p.playList([trA(1)], 0));
    await waitFor(() => expect(miniArtistLine()).toBe("邓丽君"));
    for (const t of [8.5, 12.5, 16.5]) at(audio, t);
    await waitFor(() => expect(miniArtistLine()).toBe("Line three")); // the mini player still follows
    expect(s.sets.every((m) => m.title === "甜蜜蜜" && m.artist === "邓丽君")).toBe(true);
  } finally {
    s.cleanup();
  }
});

test("car lyrics: switching tracks shows the new song at once; plain lyrics keep the song", async () => {
  const s = stubSession();
  try {
    const { audio } = renderWithApp(<><Probe /><MiniPlayer /></>, {
      routes: {
        "GET /api/v1/tracks/1/lyrics": () => ({ body: carSynced }),
        "GET /api/v1/tracks/2/lyrics": () => ({ body: plain }),
        "GET /api/v1/tracks/3/lyrics": () => ({ body: { found: false, synced: false } }),
      },
    });
    await act(async () => {});
    act(() => p.playList([trA(1), trA(2), trA(3)], 0));
    await act(async () => {});
    at(audio, 12.5);
    expect(s.last().title).toBe("Line two");
    act(() => p.next());
    expect(s.last()).toEqual({ title: "t2", artist: "邓丽君", album: "Album2", artwork: art(2) }); // synchronously, before any lyrics answer
    await act(async () => {});
    at(audio, 12.5);
    expect(s.last()).toEqual({ title: "t2", artist: "邓丽君", album: "Album2", artwork: art(2) });
    act(() => p.next());
    await act(async () => {});
    at(audio, 12.5);
    expect(s.last()).toEqual({ title: "t3", artist: "邓丽君", album: "Album3", artwork: art(3) });
  } finally {
    s.cleanup();
  }
});

test("car lyrics: lock-screen action handlers are still registered", async () => {
  const s = stubSession();
  try {
    const { audio } = renderWithApp(<><Probe /><MiniPlayer /></>, { routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body: carSynced }) } });
    await act(async () => {});
    act(() => p.playList([trA(1), trA(2)], 0));
    await act(async () => {});
    at(audio, 8.5);
    const ms = navigator.mediaSession as unknown as { setActionHandler: ReturnType<typeof vi.fn> };
    const actions = ms.setActionHandler.mock.calls.filter((c) => typeof c[1] === "function").map((c) => c[0]);
    expect(actions).toEqual(expect.arrayContaining(["nexttrack", "previoustrack", "play", "pause"]));
  } finally {
    s.cleanup();
  }
});

test("isBlankLine: empty, symbol-only and instrumental markers are blank; words are not", () => {
  for (const t of ["", "   ", "♪", "♪ ♫ ♬", "...", "(Instrumental)", "[Music]", "【间奏】", "（間奏）", "纯音乐", "Interlude"]) expect(isBlankLine(t)).toBe(true);
  for (const t of ["甜蜜蜜 你笑得甜蜜蜜", "Music of the night", "1, 2, 3", "Oh"]) expect(isBlankLine(t)).toBe(false);
});

test("car lyrics: replacing the current track object (a favorite toggle) keeps the lyric title and asks for nothing", async () => {
  const s = stubSession();
  try {
    const { audio, f } = renderWithApp(<><Probe /><MiniPlayer /></>, {
      routes: { "GET /api/v1/tracks/1/lyrics": () => ({ body: carSynced }), "GET /api/v1/tracks/2/lyrics": () => ({ body: { found: false, synced: false } }) },
    });
    await act(async () => {});
    act(() => p.playList([trA(1), trA(2)], 0));
    await act(async () => {});
    at(audio, 8.5);
    const n = s.sets.length;
    act(() => p.updateTrack({ ...trA(1), favorite: true } as Track));
    await act(async () => {});
    expect(s.sets.length).toBe(n); // no flicker back to the song
    expect(s.last().title).toBe("Line one");
    at(audio, 12.5);
    expect(s.last().title).toBe("Line two");
    // An edit that changes what's shown still applies, on the current line.
    act(() => p.updateTrack({ ...trA(1), favorite: true, title: "新标题" } as Track));
    expect(s.last()).toEqual({ title: "Line two", artist: "新标题 · 邓丽君", album: "Album1", artwork: art(1) });
    // A track without lyrics: a favorite toggle doesn't ask again.
    act(() => p.next());
    await act(async () => {});
    const calls = lyricsCalls(f, 2);
    const m = s.sets.length;
    act(() => p.updateTrack({ ...trA(2), favorite: true } as Track));
    await act(async () => {});
    expect(lyricsCalls(f, 2)).toBe(calls);
    expect(s.sets.length).toBe(m);
  } finally {
    s.cleanup();
  }
});
