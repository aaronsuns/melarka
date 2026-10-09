import { describe, expect, test, vi } from "vitest";
import type { Track } from "../api/types";
import { BYPASS, CACHE_NAME, coverKey, INDEX_KEY, lyricsKey, streamKey } from "./keys";
import { DEFAULT_CAP, OfflineCache, type ManagerDeps } from "./manager";
import { FakeCaches } from "../test/offline";

const tr = (id: number, over: Partial<Track> = {}) => ({ id, title: `T${id}`, duration_ms: 1, bitrate: 256, codec: "aac", favorite: true, ...over }) as Track;
const MB = 1024 * 1024;
const DAY = 24 * 3600_000;
const plain = (u: string) => u.replace(`&${BYPASS}`, "").replace(`?${BYPASS}`, "");

interface Opts {
  favorites?: Track[];
  sizes?: Record<number, number>;
  lengths?: Record<number, number>; // a Content-Length header to send instead of the real size
  versions?: Record<number, string>;
  cap?: number;
  mobile?: boolean;
}

function setup(opts: Opts = {}) {
  const caches = new FakeCaches();
  let favorites = opts.favorites ?? [];
  const storage = window.localStorage;
  if (opts.cap) storage.setItem("lark.offline.cap", String(opts.cap));
  if (opts.mobile) storage.setItem("lark.offline.mobileData", "1");
  const fetched: { url: string; range: string | null }[] = [];
  let online = true;
  let busy = false;
  let controlled = true;
  let hold: number | null = null; // a stream id whose download hangs until aborted
  const versions = { ...opts.versions };
  const fetchFn = vi.fn(async (u: string, init: RequestInit = {}) => {
    const url = plain(u);
    const range = new Headers(init.headers).get("Range");
    fetched.push({ url, range });
    if (!online) throw new TypeError("offline");
    const m = /tracks\/(\d+)\/stream/.exec(url);
    if (m) {
      const id = Number(m[1]);
      if (hold === id) {
        await new Promise((_, reject) => init.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError"))));
      }
      const size = opts.sizes?.[id] ?? 100;
      const headers: Record<string, string> = { "Content-Type": "audio/mp4", "Set-Cookie": "lark_session=x", "X-Lark-Version": versions[id] ?? "v1" };
      if (opts.lengths?.[id]) headers["Content-Length"] = String(opts.lengths[id]);
      if (range) return new Response(new Uint8Array(1), { status: 206, headers });
      return new Response(new Uint8Array(size), { headers });
    }
    if (url.includes("/lyrics")) return new Response('{"found":true}', { headers: { "Content-Type": "application/json" } });
    if (url.includes("size=300")) return new Response("c300", { headers: { "Content-Type": "image/jpeg" } });
    return new Response("", { status: 404 });
  });
  const clock = { t: 1_000_000 };
  const notify = vi.fn();
  const listFavorites = vi.fn(async () => favorites);
  const deps: ManagerDeps = {
    caches: caches as unknown as ManagerDeps["caches"],
    fetch: fetchFn as unknown as ManagerDeps["fetch"],
    storage,
    listFavorites,
    online: () => online,
    busy: () => busy,
    now: () => (clock.t += 1),
    notifyWorker: notify,
    persist: vi.fn(async () => true),
    estimate: async () => null,
    controlled: () => controlled,
  };
  const m = new OfflineCache(deps);
  return {
    m, deps, caches, fetched, notify, clock, listFavorites, versions,
    cache: () => caches.open(CACHE_NAME),
    setFavorites: (f: Track[]) => (favorites = f),
    setOnline: (v: boolean) => (online = v),
    setBusy: (v: boolean) => (busy = v),
    setControlled: (v: boolean) => (controlled = v),
    hold: (id: number | null) => (hold = id),
    streams: () => fetched.filter((f) => f.url.includes("/stream") && !f.range).map((f) => f.url),
  };
}

const settled = (m: OfflineCache) => vi.waitFor(() => expect(m.snapshot().progress).toBeNull());
const tick = () => new Promise((r) => setTimeout(r, 20));

describe("OfflineCache", () => {
  test("defaults: 1 GB cap, mobile data off, nothing downloads on its own", async () => {
    const s = setup({ favorites: [tr(1)] });
    await s.m.start();
    expect(s.m.snapshot().cap).toBe(DEFAULT_CAP);
    expect(DEFAULT_CAP).toBe(1024 * MB);
    expect(s.m.snapshot().allowMobile).toBe(false);
    await tick();
    expect(s.streams()).toEqual([]);
  });

  test("Cache favorites now: downloads every favorite at high quality, newest favorited first, with lyrics and covers", async () => {
    const s = setup({ favorites: [tr(3), tr(1), tr(2)] });
    await s.m.start();
    await s.m.cacheFavoritesNow();
    expect(s.deps.persist).toHaveBeenCalled();
    await settled(s.m);
    expect(s.streams()).toEqual([streamKey(3), streamKey(1), streamKey(2)]);
    // Its own requests are marked so the service worker never answers them from the cache.
    expect((s.deps.fetch as unknown as ReturnType<typeof vi.fn>).mock.calls.every((c) => String(c[0]).includes(BYPASS))).toBe(true);
    const snap = s.m.snapshot();
    expect(snap.count).toBe(3);
    expect(snap.usedBytes).toBe(3 * (100 + 14 + 4)); // audio + lyrics + the 300 cover
    expect(s.m.isCached(1)).toBe(true);
    const c = await s.cache();
    const audio = (await c.match(streamKey(1)))!;
    expect(audio.headers.get("Set-Cookie")).toBeNull();
    expect(audio.headers.get("Content-Type")).toBe("audio/mp4");
    expect(await c.match(lyricsKey(1))).toBeDefined();
    expect(await c.match(coverKey(1, 300))).toBeDefined();
    expect(await c.match(coverKey(1, 1000))).toBeUndefined(); // 404: nothing kept
    expect(s.notify).toHaveBeenCalled();
  });

  test("the mobile-data switch lets it run automatically at start", async () => {
    const s = setup({ favorites: [tr(1)], mobile: true });
    await s.m.start();
    await vi.waitFor(() => expect(s.m.isCached(1)).toBe(true));
  });

  test("never starts while the player is buffering", async () => {
    const s = setup({ favorites: [tr(1)] });
    s.setBusy(true);
    await s.m.start();
    await s.m.cacheFavoritesNow();
    await tick();
    expect(s.streams()).toEqual([]);
    s.setBusy(false);
    s.m.bufferingChanged();
    await vi.waitFor(() => expect(s.m.isCached(1)).toBe(true));
  });

  test("a download in flight is aborted when playback starts buffering, and resumed after", async () => {
    const s = setup({ favorites: [tr(1)] });
    await s.m.start();
    s.hold(1);
    await s.m.cacheFavoritesNow();
    await vi.waitFor(() => expect(s.streams()).toEqual([streamKey(1)]));
    s.setBusy(true);
    s.m.bufferingChanged();
    await tick();
    expect(s.m.isCached(1)).toBe(false);
    s.hold(null);
    s.setBusy(false);
    s.m.bufferingChanged();
    await vi.waitFor(() => expect(s.m.isCached(1)).toBe(true));
    expect(s.streams()).toEqual([streamKey(1), streamKey(1)]);
  });

  test("stops at the cap without evicting favorites; the index survives a restart", async () => {
    const s = setup({ favorites: [tr(1), tr(2), tr(3)], sizes: { 1: 400, 2: 400, 3: 400 }, cap: 1000 });
    await s.m.start();
    await s.m.cacheFavoritesNow();
    await settled(s.m);
    expect([1, 2, 3].map((id) => s.m.isCached(id))).toEqual([true, true, false]);
    expect(s.m.snapshot().capReached).toBe(true);
    const again = new OfflineCache(s.deps);
    await again.start();
    expect(again.snapshot().count).toBe(2);
    expect(again.isCached(2)).toBe(true);
    expect(await (await s.cache()).match(INDEX_KEY)).toBeDefined();
  });

  test("a track that didn't fit isn't downloaded again until the cap changes", async () => {
    const s = setup({ favorites: [tr(1), tr(2)], sizes: { 1: 600, 2: 600 }, cap: 1000 });
    await s.m.start();
    await s.m.cacheFavoritesNow();
    await settled(s.m);
    expect(s.streams()).toEqual([streamKey(1), streamKey(2)]);
    await s.m.cacheFavoritesNow();
    await settled(s.m);
    expect(s.streams()).toEqual([streamKey(1), streamKey(2)]);
    await s.m.setCap(2000);
    await vi.waitFor(() => expect(s.m.isCached(2)).toBe(true));
  });

  test("the size estimate (bitrate × duration + 5%) skips a download that can't fit, without fetching it", async () => {
    // 100 s at 128k ≈ 1.68 MB with the margin; the cap is 1.6 MB.
    const s = setup({ favorites: [tr(1, { duration_ms: 100_000, bitrate: 128, codec: "mp3" })], cap: 1_600_000 });
    await s.m.start();
    await s.m.cacheFavoritesNow();
    await settled(s.m);
    expect(s.streams()).toEqual([]);
    expect(s.m.snapshot().capReached).toBe(true);
  });

  test("a Content-Length that can't fit (or is over 150 MB) stops the download before the body", async () => {
    const s = setup({ favorites: [tr(1), tr(2)], lengths: { 1: 5000, 2: 151 * MB }, cap: 1000 });
    await s.m.start();
    await s.m.setCap(1000);
    await s.m.cacheFavoritesNow();
    await settled(s.m);
    expect(s.m.isCached(1)).toBe(false);
    await s.m.setCap(200 * MB);
    await settled(s.m);
    expect(s.m.isCached(1)).toBe(true);
    expect(s.m.isCached(2)).toBe(false);
  });

  test("tracks over an hour are never cached", async () => {
    const s = setup({ favorites: [tr(1, { duration_ms: 61 * 60_000 })], cap: 5120 * MB });
    await s.m.start();
    await s.m.cacheFavoritesNow();
    await settled(s.m);
    expect(s.streams()).toEqual([]);
  });

  test("evicts only after the new copy is stored; a full quota stops automatic caching for the session", async () => {
    const s = setup({ favorites: [], sizes: { 1: 600, 2: 600, 9: 300 }, cap: 1000, mobile: true });
    await s.m.start();
    s.m.notePlayed(tr(9, { favorite: false }), true);
    await vi.waitFor(() => expect(s.m.isCached(9)).toBe(true));
    const c = await s.cache();
    c.failPut = (k) => k.includes("/stream");
    s.setFavorites([tr(1), tr(2)]);
    await s.m.cacheFavoritesNow();
    await settled(s.m);
    expect(s.m.isCached(1)).toBe(false);
    expect(s.m.snapshot().storageFull).toBe(true);
    expect(s.m.snapshot().active).toBe(false);
    expect(s.streams().filter((u) => u === streamKey(2))).toEqual([]); // stopped: no wasted download
  });

  test("a put that fails once is retried after making room", async () => {
    const s = setup({ favorites: [], sizes: { 1: 600, 9: 600 }, cap: 1000, mobile: true });
    await s.m.start();
    s.m.notePlayed(tr(9, { favorite: false }), true);
    await vi.waitFor(() => expect(s.m.isCached(9)).toBe(true));
    const c = await s.cache();
    let failed = false;
    c.failPut = (k) => k === streamKey(1) && !failed && (failed = true);
    s.setFavorites([tr(1)]);
    s.m.favoriteChanged(1, true);
    await vi.waitFor(() => expect(s.m.isCached(1)).toBe(true));
    expect(s.m.isCached(9)).toBe(false);
    expect(s.m.snapshot().storageFull).toBe(false);
  });

  test("lowering the cap evicts recents first, then favorites least recently played", async () => {
    const s = setup({ favorites: [tr(1), tr(2)], sizes: { 1: 400, 2: 400, 9: 100 }, cap: 2000 });
    await s.m.start();
    await s.m.cacheFavoritesNow();
    await settled(s.m);
    s.m.notePlayed(tr(9, { favorite: false }), true);
    await vi.waitFor(() => expect(s.m.isCached(9)).toBe(true));
    s.m.notePlayed(tr(1), false); // 1 played more recently than 2
    await s.m.setCap(600);
    expect([1, 2, 9].map((id) => s.m.isCached(id))).toEqual([true, false, false]);
    expect(await (await s.cache()).match(streamKey(2))).toBeUndefined();
    expect(s.m.snapshot().cap).toBe(600);
    expect(window.localStorage.getItem("lark.offline.cap")).toBe("600");
  });

  test("an unfavorited track is no longer pinned: it goes before the favorites", async () => {
    const s = setup({ favorites: [tr(1), tr(2)], sizes: { 1: 400, 2: 400 }, cap: 2000 });
    await s.m.start();
    await s.m.cacheFavoritesNow();
    await settled(s.m);
    s.m.notePlayed(tr(2), false); // 2 is the most recently played …
    s.m.favoriteChanged(2, false); // … but no longer a favorite
    await s.m.setCap(600);
    expect([1, 2].map((id) => s.m.isCached(id))).toEqual([true, false]);
  });

  test("a recently played track is kept on finish only when it fits within the cap", async () => {
    const s = setup({ favorites: [tr(1)], sizes: { 1: 900, 8: 50, 9: 200 }, cap: 1100 });
    await s.m.start();
    await s.m.cacheFavoritesNow();
    await settled(s.m);
    s.m.notePlayed(tr(9, { favorite: false }), false); // not finished: nothing
    await tick();
    expect(s.streams()).not.toContain(streamKey(9));
    s.m.notePlayed(tr(9, { favorite: false }), true);
    await settled(s.m);
    expect(s.m.isCached(9)).toBe(false); // 900+ + 200 > 1100, and favorites are never evicted for it
    s.m.notePlayed(tr(8, { favorite: false }), true);
    await settled(s.m);
    expect(s.m.isCached(8)).toBe(true);
  });

  test("recents follow the mobile-data rule too", async () => {
    const s = setup();
    await s.m.start();
    s.m.notePlayed(tr(9, { favorite: false }), true);
    await tick();
    expect(s.streams()).toEqual([]);
  });

  test("offline, a download waits and resumes when back online; online syncs are at most once a minute", async () => {
    const s = setup({ favorites: [tr(1)] });
    await s.m.start();
    s.setOnline(false);
    await s.m.cacheFavoritesNow().catch(() => {});
    await tick();
    expect(s.streams()).toEqual([]);
    s.setOnline(true);
    const before = s.listFavorites.mock.calls.length;
    s.m.wentOnline();
    await vi.waitFor(() => expect(s.m.isCached(1)).toBe(true));
    s.m.wentOnline();
    s.m.wentOnline();
    expect(s.listFavorites.mock.calls.length).toBe(before + 1);
    s.clock.t += 61_000;
    s.m.wentOnline();
    expect(s.listFavorites.mock.calls.length).toBe(before + 2);
  });

  test("a new favorite is cached while automatic caching runs", async () => {
    const s = setup({ favorites: [], mobile: true });
    await s.m.start();
    s.setFavorites([tr(5)]);
    s.m.favoriteChanged(5, true);
    await vi.waitFor(() => expect(s.m.isCached(5)).toBe(true));
  });

  test("on load, cache entries the index doesn't know are deleted, and index entries without audio are dropped", async () => {
    const s = setup();
    const c = await s.cache();
    await c.put(streamKey(5), new Response("orphan"));
    await c.put(coverKey(5, 300), new Response("orphan"));
    await c.put(lyricsKey(6), new Response("{}"));
    await c.put(INDEX_KEY, new Response(JSON.stringify([{ trackId: 6, bytes: 10, kind: "favorite", lastPlayedAt: 0, cachedAt: 1 }])));
    await s.m.start();
    expect(s.m.isCached(6)).toBe(false);
    expect(await c.match(streamKey(5))).toBeUndefined();
    expect(await c.match(coverKey(5, 300))).toBeUndefined();
    expect(await c.match(lyricsKey(6))).toBeUndefined();
  });

  test("once a week a cached copy is checked; a changed source file is downloaded again, covers refreshed", async () => {
    const s = setup({ favorites: [tr(1), tr(2)], mobile: true });
    await s.m.start();
    await vi.waitFor(() => expect(s.m.isCached(1) && s.m.isCached(2)).toBe(true));
    await settled(s.m);
    const downloads = s.streams().length;
    s.clock.t += 8 * DAY;
    s.versions[2] = "v2";
    await s.m.start(); // the next app start
    await vi.waitFor(() => expect(s.streams().length).toBe(downloads + 1));
    await settled(s.m);
    expect(s.streams().at(-1)).toBe(streamKey(2));
    expect(s.fetched.filter((f) => f.range === "bytes=0-0").map((f) => f.url).sort()).toEqual([streamKey(1), streamKey(2)]);
    expect(s.fetched.filter((f) => f.url === coverKey(1, 300)).length).toBe(2);
    expect(s.m.isCached(1) && s.m.isCached(2)).toBe(true);
    // Checked now: not again until next week.
    const checks = s.fetched.length;
    await s.m.start();
    await settled(s.m);
    expect(s.fetched.filter((f, i) => i >= checks && f.range).length).toBe(0);
  });

  test("ending the session (sign-out) drops the tap's permission and anything queued", async () => {
    const s = setup({ favorites: [tr(1)] });
    s.setBusy(true);
    await s.m.start();
    await s.m.cacheFavoritesNow();
    expect(s.m.snapshot().progress).not.toBeNull();
    s.m.endSession();
    expect(s.m.snapshot().active).toBe(false);
    expect(s.m.snapshot().progress).toBeNull();
    s.setBusy(false);
    s.m.bufferingChanged();
    await tick();
    expect(s.streams()).toEqual([]);
  });

  test("playableOffline: online everything is; offline only cached tracks are (when anything is cached)", async () => {
    const s = setup({ favorites: [tr(1)] });
    await s.m.start();
    s.setOnline(false);
    expect(s.m.playableOffline(5)).toBe(true); // nothing cached: behave as before
    s.setOnline(true);
    await s.m.cacheFavoritesNow();
    await settled(s.m);
    expect(s.m.playableOffline(5)).toBe(true);
    s.setOnline(false);
    expect(s.m.playableOffline(1)).toBe(true);
    expect(s.m.playableOffline(5)).toBe(false);
  });

  test("clear removes everything", async () => {
    const s = setup({ favorites: [tr(1)] });
    await s.m.start();
    await s.m.cacheFavoritesNow();
    await settled(s.m);
    await s.m.clear();
    expect(s.m.snapshot().count).toBe(0);
    expect(s.m.snapshot().usedBytes).toBe(0);
    expect(s.m.isCached(1)).toBe(false);
    expect(await (await s.cache()).match(streamKey(1))).toBeUndefined();
  });
});

describe("lookahead: the next tracks of the queue", () => {
  test("downloads them even with mobile data off, first in line, as evictable copies the worker hears about", async () => {
    const s = setup({ favorites: [tr(1), tr(2)] });
    await s.m.start();
    expect(s.m.lookahead([tr(7, { favorite: false }), tr(8, { favorite: false })])).toEqual([7, 8]);
    await vi.waitFor(() => expect(s.m.isCached(8)).toBe(true));
    expect(s.streams()).toEqual([streamKey(7), streamKey(8)]); // no favorites: mobile data is off
    expect(s.m.localKind(7)).toBe("lookahead");
    expect(s.m.localKind(1)).toBeNull();
    expect(s.notify).toHaveBeenCalled();
  });

  test("goes before a running favorites sync's queue, never while the player buffers", async () => {
    const s = setup({ favorites: [tr(1), tr(2), tr(3)] });
    await s.m.start();
    s.setBusy(true);
    await s.m.cacheFavoritesNow();
    s.m.lookahead([tr(9, { favorite: false })]);
    await tick();
    expect(s.streams()).toEqual([]);
    s.setBusy(false);
    s.m.bufferingChanged();
    await settled(s.m);
    expect(s.streams()[0]).toBe(streamKey(9));
  });

  test("a new lookahead drops the stale ones still waiting", async () => {
    const s = setup();
    await s.m.start();
    s.setBusy(true);
    s.m.lookahead([tr(5, { favorite: false }), tr(6, { favorite: false })]);
    s.m.lookahead([tr(6, { favorite: false }), tr(7, { favorite: false })]);
    s.setBusy(false);
    s.m.bufferingChanged();
    await settled(s.m);
    expect(s.streams()).toEqual([streamKey(6), streamKey(7)]);
  });

  test("has its own budget, outside the cap: a cap full of favorites still takes them", async () => {
    const s = setup({ favorites: [tr(1), tr(2)], cap: 240, sizes: { 1: 100, 2: 100, 7: 100 } });
    await s.m.start();
    await s.m.cacheFavoritesNow();
    await settled(s.m);
    expect(s.m.lookahead([tr(7, { favorite: false })], [2])).toEqual([7]);
    await vi.waitFor(() => expect(s.m.isCached(7)).toBe(true));
    expect(s.m.isCached(1) && s.m.isCached(2)).toBe(true);
    expect(s.m.snapshot().count).toBe(2); // not shown as cached songs
  });

  test("at most 5 copies; the upcoming, playing and previous tracks are never evicted for another", async () => {
    const s = setup();
    await s.m.start();
    const t = (id: number) => tr(id, { favorite: false });
    s.m.lookahead([t(1), t(2), t(3), t(4), t(5)], []);
    await vi.waitFor(() => expect([1, 2, 3, 4, 5].every((id) => s.m.isCached(id))).toBe(true));
    // 1 is playing, 2–5 are upcoming: no room for 6 without evicting one of them.
    expect(s.m.lookahead([t(2), t(3), t(4), t(5), t(6)], [1])).toEqual([2, 3, 4, 5]);
    // 1 finished: its copy stays while it is the previous track (a car's "previous").
    s.m.notePlayed(t(1), true);
    expect(s.m.lookahead([t(3), t(4), t(5)], [2, 1])).toEqual([3, 4, 5]);
    await new Promise((r) => setTimeout(r, 20));
    expect(s.m.isCached(1)).toBe(true);
    // Two tracks on, it goes, and 6 fits.
    expect(s.m.lookahead([t(4), t(5), t(6)], [3, 2])).toEqual([4, 5, 6]);
    await vi.waitFor(() => expect(s.m.isCached(6)).toBe(true));
    expect(s.m.isCached(1)).toBe(false);
  });

  test("at most 100 MB of copies", async () => {
    const s = setup({ sizes: { 7: 60 * MB, 8: 60 * MB } });
    await s.m.start();
    s.m.lookahead([tr(7, { favorite: false })], []);
    await vi.waitFor(() => expect(s.m.isCached(7)).toBe(true));
    s.m.lookahead([tr(7, { favorite: false }), tr(8, { favorite: false })], []);
    await settled(s.m);
    expect(s.m.isCached(8)).toBe(false);
    expect(s.m.isCached(7)).toBe(true);
  });

  test("copies the queue moved past are deleted", async () => {
    const s = setup();
    await s.m.start();
    s.m.lookahead([tr(5, { favorite: false }), tr(6, { favorite: false })], [4]);
    await vi.waitFor(() => expect(s.m.isCached(6)).toBe(true));
    s.m.lookahead([tr(9, { favorite: false })], [8]); // jumped elsewhere
    await vi.waitFor(() => expect(s.m.isCached(5) || s.m.isCached(6)).toBe(false));
    expect(await (await s.cache()).match(streamKey(5))).toBeUndefined();
  });

  test("a finished copy is kept as a recent when automatic caching is on", async () => {
    const s = setup({ mobile: true });
    await s.m.start();
    s.m.lookahead([tr(5, { favorite: false })], [4]);
    await vi.waitFor(() => expect(s.m.isCached(5)).toBe(true));
    s.m.notePlayed(tr(5, { favorite: false }), true);
    expect(s.m.localKind(5)).toBe("recent");
  });

  test("not accepted when there's no offline cache to play from: no worker control, or storage full", async () => {
    const s = setup();
    await s.m.start();
    s.m.lookahead([tr(6)]);
    await vi.waitFor(() => expect(s.m.isCached(6)).toBe(true));
    s.setControlled(false);
    expect(s.m.lookahead([tr(7)])).toEqual([]);
    expect(s.m.localKind(6)).toBeNull(); // cached, but nothing would serve it
    expect(s.m.cachedFavorites()).toEqual([]);
    s.setControlled(true);
    expect(s.m.lookahead([tr(7)])).toEqual([7]);
  });

  test("a lookahead copy of a favorite is pinned on the next sync only while automatic caching runs", async () => {
    const s = setup({ favorites: [] });
    await s.m.start();
    s.m.lookahead([tr(4)], []);
    await vi.waitFor(() => expect(s.m.isCached(4)).toBe(true));
    s.setFavorites([tr(4)]);
    await s.m.syncFavorites();
    expect(s.m.localKind(4)).toBe("lookahead");
    s.m.favoriteChanged(4, true);
    expect(s.m.localKind(4)).toBe("lookahead");
    s.m.setAllowMobile(true);
    await s.m.syncFavorites();
    expect(s.m.localKind(4)).toBe("favorite");
  });

  test("cachedFavorites lists the cached favorites with their last play, as full tracks", async () => {
    const s = setup({ favorites: [tr(1, { artist: "邓丽君", album: "精选" }), tr(2)] });
    await s.m.start();
    await s.m.cacheFavoritesNow();
    await settled(s.m);
    s.m.notePlayed(tr(2), false);
    const favs = s.m.cachedFavorites();
    expect(favs.map((f) => f.track.id).sort()).toEqual([1, 2]);
    const one = favs.find((f) => f.track.id === 1)!;
    expect(one.track).toMatchObject({ title: "T1", artist: "邓丽君", album: "精选", favorite: true });
    expect(one.lastPlayedAt).toBe(0);
    expect(favs.find((f) => f.track.id === 2)!.lastPlayedAt).toBeGreaterThan(0);
  });
});
