import { moveEntry, removeEntry, swipeRemoves, targetIndex } from "./dragReorder";

const s = { from: 2, startY: 100, rowH: 40, count: 5 };

test("targetIndex rounds to the nearest row", () => {
  expect(targetIndex(s, 100)).toBe(2);
  expect(targetIndex(s, 119)).toBe(2); // under half a row
  expect(targetIndex(s, 121)).toBe(3);
  expect(targetIndex(s, 60)).toBe(1);
  expect(targetIndex(s, 179)).toBe(4);
});

test("targetIndex clamps to the list", () => {
  expect(targetIndex(s, -1000)).toBe(0);
  expect(targetIndex(s, 1000)).toBe(4);
  expect(targetIndex({ ...s, count: 1, from: 0 }, 500)).toBe(0);
  expect(targetIndex({ ...s, rowH: 0 }, 500)).toBe(2); // no height measured: stays
});

test("swipeRemoves past 35% to the left only", () => {
  expect(swipeRemoves(-35, 100)).toBe(true);
  expect(swipeRemoves(-34, 100)).toBe(false);
  expect(swipeRemoves(80, 100)).toBe(false);
  expect(swipeRemoves(-10, 0)).toBe(false);
});

test("moveEntry keeps the current entry current", () => {
  expect(moveEntry([1, 2, 3, 4], 1, 1, 3)).toEqual({ list: [1, 3, 4, 2], index: 3 });
  expect(moveEntry([1, 2, 3, 4], 1, 3, 0)).toEqual({ list: [4, 1, 2, 3], index: 2 });
  expect(moveEntry([1, 2, 3, 4], 2, 0, 3)).toEqual({ list: [2, 3, 4, 1], index: 1 });
  expect(moveEntry([1, 2, 3, 4], 1, 2, 3)).toEqual({ list: [1, 2, 4, 3], index: 1 });
  expect(moveEntry([1, 2, 3], 0, 1, 1)).toEqual({ list: [1, 2, 3], index: 0 });
  expect(moveEntry([1, 2, 3], 0, 5, 1)).toBeNull(); // out of range
});

test("removeEntry removes one entry, never the current", () => {
  expect(removeEntry([1, 2, 3], 1, 2)).toEqual({ list: [1, 2], index: 1 });
  expect(removeEntry([1, 2, 3], 1, 0)).toEqual({ list: [2, 3], index: 0 });
  expect(removeEntry([1, 2, 3], 1, 1)).toBeNull();
  expect(removeEntry([1, 2, 3], 1, 3)).toBeNull();
});
