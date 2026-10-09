import { expect, test } from "vitest";
import { gainFactor } from "./gain";

test("a negative gain becomes a volume factor below 1", () => {
  expect(gainFactor(-6, true)).toBeCloseTo(0.501, 3);
  expect(gainFactor(-20, true)).toBeCloseTo(0.1, 5);
});

test("the switch off, an unmeasured track, or a non-negative gain plays at unity", () => {
  expect(gainFactor(-6, false)).toBe(1);
  expect(gainFactor(null, true)).toBe(1);
  expect(gainFactor(undefined, true)).toBe(1);
  expect(gainFactor(0, true)).toBe(1);
  expect(gainFactor(3, true)).toBe(1); // never a boost
  expect(gainFactor(Number.NaN, true)).toBe(1);
  expect(gainFactor(Number.NEGATIVE_INFINITY, true)).toBe(1);
});
