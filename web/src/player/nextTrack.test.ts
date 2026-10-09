import { describe, expect, test } from "vitest";
import type { Track } from "../api/types";
import { chooseNext, type ChooseDeps } from "./nextTrack";
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
