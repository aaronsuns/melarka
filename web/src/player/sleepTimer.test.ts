import { describe, expect, test } from "vitest";
import { FADE_MS, FADE_STEP_MS, SLEEP_MINUTES, fadeSteps, formatRemaining } from "./sleepTimer";

describe("fadeSteps", () => {
  test("40 steps over 10 s, from just under 1 down to 0, never rising", () => {
    const s = fadeSteps();
    expect(FADE_MS / FADE_STEP_MS).toBe(40);
    expect(s).toHaveLength(40);
    expect(s[0]).toBeLessThan(1);
    expect(s[0]).toBeGreaterThan(0.9);
    expect(s[s.length - 1]).toBe(0);
    for (let i = 1; i < s.length; i++) expect(s[i]).toBeLessThan(s[i - 1]);
  });

  test("linear in amplitude: halfway through is half the volume", () => {
    expect(fadeSteps()[19]).toBeCloseTo(0.5, 10);
  });

  test("other lengths", () => {
    expect(fadeSteps(1000, 250)).toEqual([0.75, 0.5, 0.25, 0]);
  });
});

describe("formatRemaining", () => {
  test("m:ss", () => {
    expect(formatRemaining(61_000)).toBe("1:01");
    expect(formatRemaining(15 * 60_000)).toBe("15:00");
    expect(formatRemaining(60 * 60_000)).toBe("60:00");
    expect(formatRemaining(9_000)).toBe("0:09");
  });

  test("a part second counts as a whole one; never negative", () => {
    expect(formatRemaining(899_001)).toBe("15:00");
    expect(formatRemaining(0)).toBe("0:00");
    expect(formatRemaining(-500)).toBe("0:00");
  });
});

test("the menu's choices", () => {
  expect([...SLEEP_MINUTES]).toEqual([15, 30, 45, 60]);
});
