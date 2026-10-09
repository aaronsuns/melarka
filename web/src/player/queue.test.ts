import { appendable, current, emptyQueue, needsRefill, queueReducer as r, saveUpNext, shuffled, storedUpNext, upcoming } from "./queue";
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

test("setOrder replaces the order and index but keeps the source", () => {
  const s = r(emptyQueue, { type: "playList", tracks: list, start: 1, source: "favorites" });
  const o = r(s, { type: "setOrder", tracks: [tr(1), tr(2), tr(5), tr(3), tr(4)], index: 1 });
  expect(o.tracks.map((t) => t.id)).toEqual([1, 2, 5, 3, 4]);
  expect(o.index).toBe(1);
  expect(o.source).toBe("favorites");
  expect(r(s, { type: "setOrder", tracks: [tr(1)], index: 9 }).index).toBe(0);
});

// Queue editing. "Add to queue" goes after the explicit up-next entries (play
// next, earlier adds), ahead of the rest of the list, so it plays before what
// the queue was already going to play.
const q = (ids: number[], index: number) => r(emptyQueue, { type: "playList", tracks: ids.map(tr), start: index });
const ids = (s: { tracks: Track[] }) => s.tracks.map((t) => t.id);

test("addToQueue appends after explicit up-next items", () => {
  let s = r(q([1, 2, 3], 0), { type: "enqueueNext", track: tr(9) }); // [1,9,2,3]
  s = r(s, { type: "addToQueue", track: tr(7) });
  expect(ids(s)).toEqual([1, 9, 7, 2, 3]);
  s = r(s, { type: "addToQueue", track: tr(8) }); // after the earlier add, in order
  expect(ids(s)).toEqual([1, 9, 7, 8, 2, 3]);
  s = r(s, { type: "enqueueNext", track: tr(6) }); // play next still goes first
  expect(ids(s)).toEqual([1, 6, 9, 7, 8, 2, 3]);
  expect(s.index).toBe(0);
});

test("addToQueue with nothing queued goes right after the current entry", () => {
  const s = r(q([1, 2, 3], 1), { type: "addToQueue", track: tr(7) });
  expect(ids(s)).toEqual([1, 2, 7, 3]);
  expect(s.index).toBe(1);
  expect(ids(r(emptyQueue, { type: "addToQueue", track: tr(7) }))).toEqual([7]);
});

test("addToQueue moves an upcoming copy, leaves played and current entries", () => {
  let s = r(q([5, 1, 2, 3, 5], 1), { type: "addToQueue", track: tr(3) });
  expect(ids(s)).toEqual([5, 1, 3, 2, 5]);
  s = r(s, { type: "addToQueue", track: tr(5) }); // the played 5 stays; the upcoming one moves
  expect(ids(s)).toEqual([5, 1, 3, 5, 2]);
  expect(s.index).toBe(1);
  s = r(s, { type: "addToQueue", track: tr(3) }); // a queued copy moves to the end of the queued ones
  expect(ids(s)).toEqual([5, 1, 5, 3, 2]);
  s = r(s, { type: "addToQueue", track: tr(1) }); // the current track: played again, current untouched
  expect(ids(s)).toEqual([5, 1, 5, 3, 1, 2]);
  expect(s.index).toBe(1);
});

test("the queued block shrinks as playback reaches it", () => {
  let s = r(q([1, 2, 3], 0), { type: "addToQueue", track: tr(7) }); // [1,7,2,3]
  s = r(s, { type: "addToQueue", track: tr(8) }); // [1,7,8,2,3]
  s = r(s, { type: "next" }); // on 7; 8 still queued
  s = r(s, { type: "addToQueue", track: tr(9) });
  expect(ids(s)).toEqual([1, 7, 8, 9, 2, 3]);
  s = r(r(r(s, { type: "next" }), { type: "next" }), { type: "next" }); // on 2: nothing queued now
  s = r(s, { type: "addToQueue", track: tr(4) });
  expect(ids(s)).toEqual([1, 7, 8, 9, 2, 4, 3]);
});

test("jumping past the queued entries ends them; prev keeps them ahead", () => {
  let s = r(q([1, 2, 3, 4], 0), { type: "addToQueue", track: tr(7) }); // [1,7,2,3,4]
  s = r(s, { type: "jump", index: 3 }); // on 3
  s = r(s, { type: "addToQueue", track: tr(8) });
  expect(ids(s)).toEqual([1, 7, 2, 3, 8, 4]);
  let p = r(q([1, 2, 3], 1), { type: "addToQueue", track: tr(7) }); // [1,2,7,3]
  p = r(p, { type: "prev" }); // on 1
  p = r(p, { type: "addToQueue", track: tr(8) });
  expect(ids(p)).toEqual([1, 2, 7, 8, 3]);
});

test("a new list or a restore forgets the queued entries", () => {
  let s = r(q([1, 2], 0), { type: "addToQueue", track: tr(7) });
  s = r(s, { type: "playList", tracks: [tr(4), tr(5)], start: 0 });
  expect(ids(r(s, { type: "addToQueue", track: tr(8) }))).toEqual([4, 8, 5]);
  s = r(r(q([1, 2], 0), { type: "addToQueue", track: tr(7) }), { type: "restore", tracks: [tr(4), tr(5)], index: 0 });
  expect(ids(r(s, { type: "addToQueue", track: tr(8) }))).toEqual([4, 8, 5]);
});

test("move keeps the current entry current", () => {
  const s = r(q([1, 2, 3, 4], 1), { type: "move", from: 1, to: 3 });
  expect(ids(s)).toEqual([1, 3, 4, 2]);
  expect(s.index).toBe(3);
});

test("move of another entry across the current shifts index", () => {
  let s = r(q([1, 2, 3, 4], 1), { type: "move", from: 3, to: 0 });
  expect(ids(s)).toEqual([4, 1, 2, 3]);
  expect(s.index).toBe(2);
  s = r(s, { type: "move", from: 0, to: 3 });
  expect(ids(s)).toEqual([1, 2, 3, 4]);
  expect(s.index).toBe(1);
  s = r(s, { type: "move", from: 2, to: 3 }); // both after the current: index unchanged
  expect(ids(s)).toEqual([1, 2, 4, 3]);
  expect(s.index).toBe(1);
  expect(r(s, { type: "move", from: 1, to: 9 })).toBe(s); // out of range: no change
  expect(r(s, { type: "move", from: 2, to: 2 })).toBe(s);
});

test("move within or into the queued entries keeps them queued", () => {
  let s = r(q([1, 2, 3], 0), { type: "addToQueue", track: tr(7) });
  s = r(s, { type: "addToQueue", track: tr(8) }); // [1,7,8,2,3]
  s = r(s, { type: "move", from: 4, to: 1 }); // 3 dragged to the front of the queued ones
  expect(ids(s)).toEqual([1, 3, 7, 8, 2]);
  s = r(s, { type: "addToQueue", track: tr(9) });
  expect(ids(s)).toEqual([1, 3, 7, 8, 9, 2]);
  s = r(s, { type: "move", from: 1, to: 5 }); // 3 dragged out to the end: no longer queued
  s = r(s, { type: "addToQueue", track: tr(6) });
  expect(ids(s)).toEqual([1, 7, 8, 9, 6, 2, 3]);
});

test("removeAt removes one entry, never the current", () => {
  let s = r(q([1, 2, 3, 2], 1), { type: "removeAt", index: 3 });
  expect(ids(s)).toEqual([1, 2, 3]);
  expect(s.index).toBe(1);
  s = r(s, { type: "removeAt", index: 0 }); // before the current: index follows
  expect(ids(s)).toEqual([2, 3]);
  expect(s.index).toBe(0);
  expect(r(s, { type: "removeAt", index: 0 })).toBe(s); // the current one: no-op
  expect(r(s, { type: "removeAt", index: 7 })).toBe(s);
});

test("removeAt of a queued entry shrinks the queued block", () => {
  let s = r(q([1, 2, 3], 0), { type: "addToQueue", track: tr(7) });
  s = r(s, { type: "addToQueue", track: tr(8) }); // [1,7,8,2,3]
  s = r(s, { type: "removeAt", index: 1 });
  s = r(s, { type: "addToQueue", track: tr(9) });
  expect(ids(s)).toEqual([1, 8, 9, 2, 3]);
});

test("setOrder can reset the queued block (a reshuffle)", () => {
  let s = r(q([1, 2, 3], 0), { type: "addToQueue", track: tr(7) }); // [1,7,2,3]
  s = r(s, { type: "setOrder", tracks: [tr(1), tr(3), tr(7), tr(2)], index: 0, upNext: 0 });
  expect(ids(r(s, { type: "addToQueue", track: tr(8) }))).toEqual([1, 8, 3, 7, 2]);
  let k = r(q([1, 2, 3], 0), { type: "addToQueue", track: tr(7) });
  k = r(k, { type: "setOrder", tracks: [tr(1), tr(7), tr(3), tr(2)], index: 0 }); // kept by default
  expect(ids(r(k, { type: "addToQueue", track: tr(8) }))).toEqual([1, 7, 8, 3, 2]);
});

// The queued block is saved per user on this device: a reload (a restore of
// the same queue) keeps add to queue after the tracks queued before it.
describe("stored upNext", () => {
  afterEach(() => localStorage.clear());

  test("restore takes upNext", () => {
    const s = r(emptyQueue, { type: "restore", tracks: [tr(1), tr(2), tr(3)], index: 0, upNext: 1 });
    expect(ids(r(s, { type: "addToQueue", track: tr(9) }))).toEqual([1, 2, 9, 3]);
    expect(r(emptyQueue, { type: "restore", tracks: [tr(1)], index: 0, upNext: 4 }).upNext).toBeUndefined(); // clamped
  });

  test("saved with the queue, given back only for the same ids and index", () => {
    const s = r(q([1, 2, 3], 0), { type: "addToQueue", track: tr(7) }); // [1,7,2,3], upNext 1
    saveUpNext(5, s);
    expect(JSON.parse(localStorage.getItem("lark.upNext.5")!)).toEqual({ ids: [1, 7, 2, 3], index: 0, upNext: 1 });
    expect(storedUpNext(5, [1, 7, 2, 3], 0)).toBe(1);
    expect(storedUpNext(5, [1, 7, 2, 3], 1)).toBe(0); // another index
    expect(storedUpNext(5, [1, 2, 3], 0)).toBe(0); // another queue
    expect(storedUpNext(6, [1, 7, 2, 3], 0)).toBe(0); // another user
    saveUpNext(5, q([1, 2], 0)); // nothing queued: forgotten
    expect(localStorage.getItem("lark.upNext.5")).toBeNull();
    localStorage.setItem("lark.upNext.5", "{bad");
    expect(storedUpNext(5, [1], 0)).toBe(0);
  });
});
