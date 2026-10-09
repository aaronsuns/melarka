import { afterEach, describe, expect, test, vi } from "vitest";
import type { Track } from "../api/types";
import type { QueueState } from "./queue";
import { defaultModes, loadModes, modesKey, newPass, nextRepeat, saveModes, shuffleUpcoming, unshuffleUpcoming } from "./modes";

const tr = (id: number) => ({ id, title: `t${id}` }) as Track;
const id = (t: Track) => t.id;
const q = (ids: number[], index = 0): QueueState => ({ tracks: ids.map(tr), index, source: "list" });
// A repeatable "random" source: the given values, round and round.
const seq = (...xs: number[]) => {
  let i = 0;
  return () => xs[i++ % xs.length];
};

describe("shuffle", () => {
  test("shuffle keeps the current track and permutes only what follows", () => {
    const { queue, original } = shuffleUpcoming(q([1, 2, 3, 4, 5], 1), seq(0.1, 0.9, 0.5));
    expect(queue.tracks.slice(0, 2).map(id)).toEqual([1, 2]);
    expect(queue.index).toBe(1);
    expect([...queue.tracks.slice(2).map(id)].sort()).toEqual([3, 4, 5]);
    expect(original).toEqual([3, 4, 5]);
    expect(queue.source).toBe("list");
  });

  test("unshuffle restores the order; play-next stays in front, appended stays at the end", () => {
    const shuffled = q([1, 9, 5, 3, 4, 7], 0); // 9 = play next, 7 = add to queue, original [3,4,5]
    expect(unshuffleUpcoming(shuffled, [3, 4, 5]).tracks.map(id)).toEqual([1, 9, 3, 4, 5, 7]);
  });

  test("unshuffle drops removed and already-played tracks", () => {
    // Shuffled from [1, 2, 3, 4, 5] at 1; then 4 played (now current) and 5 removed.
    const s = q([1, 4, 3, 2], 1);
    const out = unshuffleUpcoming(s, [2, 3, 4, 5]);
    expect(out.tracks.map(id)).toEqual([1, 4, 2, 3]);
    expect(out.index).toBe(1);
  });

  test("unshuffle matches repeated ids as a multiset", () => {
    // original [2, 3, 2]; one 2 played already (current), the other still upcoming.
    expect(unshuffleUpcoming(q([1, 2, 3], 1), [2, 3, 2]).tracks.map(id)).toEqual([1, 2, 3]);
    expect(unshuffleUpcoming(q([1, 2, 3, 2], 0), [3, 2, 2]).tracks.map(id)).toEqual([1, 3, 2, 2]);
  });

  test("a shuffled new pass never starts with the track that just ended", () => {
    // Without the guard this rand puts 4 (just ended) first: [4, 2, 3, 1].
    const pass = newPass(q([1, 2, 3, 4], 3), true, seq(0, 0.99, 0.99));
    expect(pass.tracks[0].id).not.toBe(4);
    expect([...pass.tracks.map(id)].sort()).toEqual([1, 2, 3, 4]);
    for (let i = 0; i < 200; i++) expect(newPass(q([1, 2, 3], 2), true).tracks[0].id).not.toBe(3);
    expect(newPass(q([5], 0), true).tracks.map(id)).toEqual([5]); // nothing else to start with
  });

  test("unshuffle with nothing remembered keeps the current order", () => {
    expect(unshuffleUpcoming(q([1, 3, 2], 0), []).tracks.map(id)).toEqual([1, 3, 2]);
  });

  test("newPass: same order from 0, or a fresh shuffle", () => {
    const end = q([1, 2, 3, 4], 3);
    const plain = newPass(end, false);
    expect(plain.tracks.map(id)).toEqual([1, 2, 3, 4]);
    expect(plain.index).toBe(0);
    expect(plain.source).toBe("list");
    const mixed = newPass(end, true, seq(0, 0, 0));
    expect(mixed.index).toBe(0);
    expect([...mixed.tracks.map(id)].sort()).toEqual([1, 2, 3, 4]);
    expect(mixed.tracks.map(id)).not.toEqual([1, 2, 3, 4]);
  });
});

test("nextRepeat cycles off → all → one → off", () => {
  expect(nextRepeat("off")).toBe("all");
  expect(nextRepeat("all")).toBe("one");
  expect(nextRepeat("one")).toBe("off");
});

describe("persistence", () => {
  afterEach(() => vi.restoreAllMocks());

  test("per user key", () => {
    expect(modesKey()).toBe("lark.modes");
    expect(modesKey(7)).toBe("lark.modes.7");
  });

  test("save and load round trip, per user", () => {
    saveModes(1, { shuffle: true, repeat: "one", original: [3, 4] });
    expect(loadModes(1)).toEqual({ shuffle: true, repeat: "one", original: [3, 4] });
    expect(loadModes(2)).toEqual(defaultModes);
    expect(loadModes()).toEqual(defaultModes);
  });

  test("bad JSON or bad fields fall back to the defaults", () => {
    localStorage.setItem("lark.modes", "{not json");
    expect(loadModes()).toEqual(defaultModes);
    localStorage.setItem("lark.modes", JSON.stringify({ shuffle: "yes", repeat: "sometimes", original: "x" }));
    expect(loadModes()).toEqual(defaultModes);
    localStorage.setItem("lark.modes", JSON.stringify({ repeat: "all" }));
    expect(loadModes()).toEqual({ shuffle: false, repeat: "all", original: null });
  });

  test("loadModes survives broken storage", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(loadModes(1)).toEqual(defaultModes);
    expect(() => saveModes(1, { shuffle: true, repeat: "all", original: [] })).not.toThrow();
  });
});
