import { appendable, current, emptyQueue, needsRefill, queueReducer as r, shuffled, upcoming } from "./queue";
import type { Track } from "../api/types";

const tr = (id: number) => ({ id, title: `t${id}` }) as Track;
const list = [tr(1), tr(2), tr(3), tr(4), tr(5)];

test("playList and navigation", () => {
  let s = r(emptyQueue, { type: "playList", tracks: list, start: 2 });
  expect(current(s)?.id).toBe(3);
  s = r(s, { type: "next" });
  expect(current(s)?.id).toBe(4);
  s = r(r(r(s, { type: "prev" }), { type: "prev" }), { type: "prev" });
  s = r(s, { type: "prev" });
  expect(s.index).toBe(0);
  s = r(s, { type: "jump", index: 4 });
  expect(current(s)?.id).toBe(5);
  expect(r(s, { type: "next" }).index).toBe(4); // next at the end stays (player refills or stops)
});

test("enqueueNext inserts after current and moves duplicates", () => {
  let s = r(emptyQueue, { type: "playList", tracks: list, start: 0 });
  s = r(s, { type: "enqueueNext", track: tr(4) });
  expect(s.tracks.map((t) => t.id)).toEqual([1, 4, 2, 3, 5]);
  expect(current(s)?.id).toBe(1);
});

test("remove keeps the current track stable", () => {
  let s = r(emptyQueue, { type: "playList", tracks: list, start: 2 });
  s = r(s, { type: "remove", trackId: 1 });
  expect(current(s)?.id).toBe(3);
  expect(s.index).toBe(1);
  s = r(s, { type: "remove", trackId: 3 }); // removing the current one moves to the next
  expect(current(s)?.id).toBe(4);
  s = r(r(emptyQueue, { type: "playList", tracks: [tr(9)], start: 0 }), { type: "remove", trackId: 9 });
  expect(current(s)).toBeNull();
});

test("append from radio skips duplicates; needsRefill", () => {
  let s = r(emptyQueue, { type: "playList", tracks: list, start: 2 });
  expect(needsRefill(s)).toBe(true); // 2 left after current
  s = r(s, { type: "append", tracks: [tr(5), tr(6), tr(7)], source: "radio" });
  expect(s.tracks.map((t) => t.id)).toEqual([1, 2, 3, 4, 5, 6, 7]);
  expect(s.source).toBe("radio");
  expect(needsRefill(s)).toBe(false);
  expect(upcoming(s).map((t) => t.id)).toEqual([4, 5, 6, 7]);
});

test("restore clamps a bad index", () => {
  const s = r(emptyQueue, { type: "restore", tracks: list, index: 99 });
  expect(s.index).toBe(4);
  expect(s.source).toBe("restored");
  expect(r(emptyQueue, { type: "restore", tracks: [], index: 3 }).index).toBe(0);
});

test("remove with duplicate ids before current index", () => {
  // tracks=[2,2,1,3], index=2 (current=1)
  // remove(2) should remove both 2s, adjust index by 2, current stays at 1
  let s = r(emptyQueue, { type: "playList", tracks: [tr(2), tr(2), tr(1), tr(3)], start: 2 });
  expect(current(s)?.id).toBe(1);
  s = r(s, { type: "remove", trackId: 2 });
  expect(s.tracks.map((t) => t.id)).toEqual([1, 3]);
  expect(current(s)?.id).toBe(1);
  expect(s.index).toBe(0);
});

test("enqueueNext with duplicates of current track id", () => {
  // tracks=[1,2,1], index=2 (current=1 at pos 2)
  // enqueueNext(3) should insert 3 after position 2, index stays 2
  let s = r(emptyQueue, { type: "playList", tracks: [tr(1), tr(2), tr(1)], start: 2 });
  expect(current(s)?.id).toBe(1);
  s = r(s, { type: "enqueueNext", track: tr(3) });
  expect(s.tracks.map((t) => t.id)).toEqual([1, 2, 1, 3]);
  expect(current(s)?.id).toBe(1);
  expect(s.index).toBe(2);
});

test("append dedupes within the incoming batch", () => {
  let s = r(emptyQueue, { type: "playList", tracks: list, start: 0 });
  s = r(s, { type: "append", tracks: [tr(6), tr(7), tr(6)] });
  expect(s.tracks.map((t) => t.id)).toEqual([1, 2, 3, 4, 5, 6, 7]);
});

test("playList with source:shuffle sets source, and append keeps it", () => {
  let s = r(emptyQueue, { type: "playList", tracks: list, start: 0, source: "shuffle" });
  expect(s.source).toBe("shuffle");
  s = r(s, { type: "append", tracks: [tr(6)] });
  expect(s.source).toBe("shuffle");
  expect(s.tracks.map((t) => t.id)).toEqual([1, 2, 3, 4, 5, 6]);
});

test("updateTrack replaces all entries with that id, leaving index/others untouched", () => {
  let s = r(emptyQueue, { type: "playList", tracks: [tr(1), tr(2), tr(1)], start: 1 });
  const updated = { ...tr(1), title: "新标题" } as Track;
  s = r(s, { type: "updateTrack", track: updated });
  expect(s.tracks.map((t) => t.title)).toEqual(["新标题", "t2", "新标题"]);
  expect(s.index).toBe(1);
  expect(current(s)?.id).toBe(2);
});

test("updateTrack is a no-op when the id isn't in the queue", () => {
  const s = r(emptyQueue, { type: "playList", tracks: list, start: 0 });
  expect(r(s, { type: "updateTrack", track: tr(99) })).toBe(s);
});

// A favorites queue loops — a refill may bring back songs already
// played (or playing); only the upcoming part and the batch itself are deduped.
test("append from favorites allows a new pass, deduping only upcoming and the batch", () => {
  let s = r(emptyQueue, { type: "playList", tracks: [tr(7), tr(8), tr(9)], start: 1, source: "favorites" });
  s = r(s, { type: "append", tracks: [tr(9), tr(7), tr(8), tr(7)], source: "favorites" });
  expect(s.tracks.map((t) => t.id)).toEqual([7, 8, 9, 7, 8]); // 9 is upcoming; the 2nd 7 is a batch dup
  expect(s.index).toBe(1);
  expect(s.source).toBe("favorites");
  expect(appendable(s, [tr(7), tr(8)], "favorites")).toEqual([]); // both already upcoming
  // Radio/shuffle keep the whole-queue dedupe.
  expect(r(s, { type: "append", tracks: [tr(7), tr(10)], source: "shuffle" }).tracks.map((t) => t.id)).toEqual([7, 8, 9, 7, 8, 10]);
});

test("a queue holding the same id twice navigates by position", () => {
  let s = r(emptyQueue, { type: "playList", tracks: [tr(7), tr(8), tr(7), tr(8)], start: 0, source: "favorites" });
  s = r(r(s, { type: "next" }), { type: "next" });
  expect(s.index).toBe(2);
  expect(current(s)?.id).toBe(7);
  s = r(s, { type: "prev" });
  expect(s.index).toBe(1);
  expect(current(s)?.id).toBe(8);
  s = r(s, { type: "jump", index: 3 });
  expect(s.index).toBe(3);
  expect(current(s)?.id).toBe(8);
  expect(upcoming(r(s, { type: "jump", index: 0 })).map((t) => t.id)).toEqual([8, 7, 8]);
});

test("shuffled returns a permutation and leaves the input alone", () => {
  const xs = [5, 4, 3];
  expect(shuffled(xs, () => 0)).toEqual([4, 3, 5]); // Fisher–Yates with j = 0 every step
  expect(xs).toEqual([5, 4, 3]);
  expect([...shuffled(xs)].sort()).toEqual([3, 4, 5]);
});

test("needsRefill counts only upcoming tracks that haven't failed", () => {
  const s = { tracks: [1, 2, 3, 4, 5].map((id) => ({ id }) as Track), index: 0, source: "radio" as const };
  expect(needsRefill(s)).toBe(false);
  expect(needsRefill(s, (id) => id === 2 || id === 3)).toBe(true);
});
