import { describe, expect, test } from "vitest";
import type { Track } from "../api/types";
import { chooseNext, type ChooseDeps } from "./nextTrack";
import type { ChooseOpts } from "./nextTrack";
import { queueReducer, type QueueState } from "./queue";

const tr = (id: number) => ({ id, title: `t${id}` }) as Track;
const q = (ids: number[], index = 0): QueueState => ({ tracks: ids.map(tr), index, source: "favorites" });
const NOW = 10 * 3600_000;

function deps(over: Partial<ChooseDeps> & { cached?: number[]; favs?: [number, number][]; failedIds?: number[] } = {}): ChooseDeps {
  const cached = new Set(over.cached ?? []);
  const failed = new Set(over.failedIds ?? []);
  return {
    local: (id) => cached.has(id),
    failed: (id) => failed.has(id),
    playable: () => true,
    favorites: () => (over.favs ?? []).map(([id, lastPlayedAt]) => ({ track: tr(id), lastPlayedAt })),
    now: () => NOW,
    random: () => 0,
    ...over,
  };
}

describe("chooseNext", () => {
  test("a cached next track just plays", () => {
    expect(chooseNext(q([1, 2, 3]), { mustBeLocal: true, favoritesAtEnd: true }, deps({ cached: [2] }))).toMatchObject({ kind: "queue", index: 1, local: true });
  });

  test("visible: the next track plays normally, cached or not", () => {
    expect(chooseNext(q([1, 2, 3]), { mustBeLocal: false, favoritesAtEnd: false }, deps({ cached: [3] }))).toMatchObject({ kind: "queue", index: 1, local: false });
  });

  test("hidden and the next isn't cached: the next cached one in the queue", () => {
    const c = chooseNext(q([1, 2, 3, 4]), { mustBeLocal: true, favoritesAtEnd: true }, deps({ cached: [4] }));
    expect(c).toMatchObject({ kind: "queue", index: 3, local: true });
    // …and the skipped ones are moved after it, to play once downloaded.
    const nq = queueReducer(q([1, 2, 3, 4]), { type: "advance", to: 3 });
    expect(nq.tracks.map((t) => t.id)).toEqual([1, 4, 2, 3]);
    expect(nq.index).toBe(1);
  });

  test("hidden with nothing cached in the queue: a cached favorite not played recently", () => {
    const c = chooseNext(q([1, 2, 3]), { mustBeLocal: true, favoritesAtEnd: true }, deps({ favs: [[10, NOW - 60_000], [11, 0], [1, 0]] }));
    expect(c).toEqual({ kind: "favorite", track: tr(11) });
    const nq = queueReducer(q([1, 2, 3]), { type: "advanceInsert", track: tr(11) });
    expect(nq.tracks.map((t) => t.id)).toEqual([1, 11, 2, 3]);
    expect(nq.index).toBe(1);
  });

  test("every favorite played recently: any cached favorite but the current track", () => {
    const c = chooseNext(q([1, 2]), { mustBeLocal: true, favoritesAtEnd: true }, deps({ favs: [[1, NOW], [10, NOW - 1000]] }));
    expect(c).toEqual({ kind: "favorite", track: tr(10) });
  });

  test("a favorite that was in this queue a moment ago counts as recently played", () => {
    const c = chooseNext(q([5, 1, 2], 1), { mustBeLocal: true, favoritesAtEnd: true }, deps({ favs: [[5, 0], [6, 0]] }));
    expect(c).toEqual({ kind: "favorite", track: tr(6) });
  });

  test("nothing cached at all: the next track as before", () => {
    expect(chooseNext(q([1, 2]), { mustBeLocal: true, favoritesAtEnd: true }, deps())).toMatchObject({ kind: "queue", index: 1, local: false });
  });

  test("tracks that failed this session are skipped", () => {
    expect(chooseNext(q([1, 2, 3]), { mustBeLocal: false, favoritesAtEnd: false }, deps({ failedIds: [2] }))).toMatchObject({ kind: "queue", index: 2 });
  });

  test("at the end of the queue: a cached favorite when asked for one, else none", () => {
    expect(chooseNext(q([1, 2], 1), { mustBeLocal: false, favoritesAtEnd: true }, deps({ favs: [[9, 0]] }))).toEqual({ kind: "favorite", track: tr(9) });
    expect(chooseNext(q([1, 2], 1), { mustBeLocal: false, favoritesAtEnd: false }, deps({ favs: [[9, 0]] }))).toEqual({ kind: "none" });
  });

  test("offline, tracks that can't play are skipped (and said so)", () => {
    const c = chooseNext(q([1, 2, 3]), { mustBeLocal: false, favoritesAtEnd: false }, deps({ playable: (id) => id === 3 }));
    expect(c).toMatchObject({ kind: "queue", index: 2, skippedOffline: true });
  });
});

describe("chooseNext with wrap (repeat all)", () => {
  // Every existing case, as [queue, opts, deps].
  const cases: [string, QueueState, ChooseOpts, ChooseDeps][] = [
    ["cached next", q([1, 2, 3]), { mustBeLocal: true, favoritesAtEnd: true }, deps({ cached: [2] })],
    ["visible uncached", q([1, 2, 3]), { mustBeLocal: false, favoritesAtEnd: false }, deps({ cached: [3] })],
    ["hidden, later cached", q([1, 2, 3, 4]), { mustBeLocal: true, favoritesAtEnd: true }, deps({ cached: [4] })],
    ["hidden, favorite", q([1, 2, 3]), { mustBeLocal: true, favoritesAtEnd: true }, deps({ favs: [[10, NOW - 60_000], [11, 0], [1, 0]] })],
    ["all favorites recent", q([1, 2]), { mustBeLocal: true, favoritesAtEnd: true }, deps({ favs: [[1, NOW], [10, NOW - 1000]] })],
    ["favorite in history", q([5, 1, 2], 1), { mustBeLocal: true, favoritesAtEnd: true }, deps({ favs: [[5, 0], [6, 0]] })],
    ["nothing cached", q([1, 2]), { mustBeLocal: true, favoritesAtEnd: true }, deps()],
    ["failed skipped", q([1, 2, 3]), { mustBeLocal: false, favoritesAtEnd: false }, deps({ failedIds: [2] })],
    ["end, favorite", q([1, 2], 1), { mustBeLocal: false, favoritesAtEnd: true }, deps({ favs: [[9, 0]] })],
    ["end, none", q([1, 2], 1), { mustBeLocal: false, favoritesAtEnd: false }, deps({ favs: [[9, 0]] })],
    ["offline skip", q([1, 2, 3]), { mustBeLocal: false, favoritesAtEnd: false }, deps({ playable: (id) => id === 3 })],
  ];

  test.each(cases)("wrap: false changes nothing (%s)", (_, s, opts, d) => {
    expect(chooseNext(s, { ...opts, wrap: false }, d)).toEqual(chooseNext(s, opts, d));
  });

  test.each(cases.filter(([, s]) => s.index < s.tracks.length - 1))("wrap: true with tracks left changes nothing (%s)", (_, s, opts, d) => {
    expect(chooseNext(s, { ...opts, wrap: true }, d)).toEqual(chooseNext(s, opts, d));
  });

  test("at the end of the queue, wrap starts over instead of a favorite or none", () => {
    const favs = deps({ favs: [[9, 0]], cached: [9] });
    expect(chooseNext(q([1, 2], 1), { mustBeLocal: false, favoritesAtEnd: true, wrap: true }, favs)).toEqual({ kind: "wrap" });
    // Must be local, with nothing local in the queue: as without wrap, a cached favorite.
    expect(chooseNext(q([1, 2], 1), { mustBeLocal: true, favoritesAtEnd: true, wrap: true }, favs)).toEqual({ kind: "favorite", track: tr(9) });
    expect(chooseNext(q([1, 2], 1), { mustBeLocal: true, favoritesAtEnd: true, wrap: true }, deps({ cached: [1] }))).toEqual({ kind: "wrap", local: true });
    expect(chooseNext(q([1, 2], 1), { mustBeLocal: false, favoritesAtEnd: false, wrap: true }, deps())).toEqual({ kind: "wrap" });
    // Only failed tracks left: nothing to play after the current one either.
    expect(chooseNext(q([1, 2, 3], 1), { mustBeLocal: false, favoritesAtEnd: false, wrap: true }, deps({ failedIds: [3] }))).toEqual({ kind: "wrap" });
  });

  // Repeat all never loops on what can't play: with nothing playable in the
  // whole queue, the choice is the one without wrap (a favorite, or none).
  test("every track failed: no wrap, never-stop's choice instead", () => {
    const s = q([1, 2, 3], 2);
    const all = { failedIds: [1, 2, 3] };
    expect(chooseNext(s, { mustBeLocal: false, favoritesAtEnd: true, wrap: true }, deps({ ...all, favs: [[9, 0]] }))).toEqual({ kind: "favorite", track: tr(9) });
    expect(chooseNext(s, { mustBeLocal: false, favoritesAtEnd: false, wrap: true }, deps(all))).toEqual({ kind: "none" });
    // The failure brake (must be local) with nothing cached: none, not a loop.
    expect(chooseNext(s, { mustBeLocal: true, favoritesAtEnd: true, wrap: true }, deps({ failedIds: [3] }))).toEqual({ kind: "none" });
  });

  test("offline with nothing cached: no wrap", () => {
    const off = deps({ playable: () => false });
    expect(chooseNext(q([1, 2], 1), { mustBeLocal: false, favoritesAtEnd: true, wrap: true }, off)).toEqual({ kind: "none" });
    expect(chooseNext(q([1, 2], 1), { mustBeLocal: false, favoritesAtEnd: true, wrap: true }, deps({ playable: (id) => id === 1 }))).toEqual({ kind: "wrap" });
  });
});
