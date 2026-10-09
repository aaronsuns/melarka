import { describe, expect, test } from "vitest";
import { enforceCap, evictionOrder, LOOKAHEAD_MAX_BYTES, planFit, planLookahead, usedBytes, type Entry } from "./policy";

const e = (trackId: number, over: Partial<Entry> = {}): Entry => ({ trackId, bytes: 10, kind: "favorite", lastPlayedAt: 0, cachedAt: trackId, ...over });

describe("eviction order", () => {
  test("recent (never favorited) by oldest play, then unfavorited former favorites, then favorites least recently played", () => {
    const entries = [
      e(1, { kind: "favorite", lastPlayedAt: 50 }),
      e(2, { kind: "recent", lastPlayedAt: 30 }),
      e(3, { kind: "recent", formerFavorite: true, lastPlayedAt: 1 }),
      e(4, { kind: "favorite", lastPlayedAt: 10 }),
      e(5, { kind: "recent", lastPlayedAt: 20 }),
      e(6, { kind: "recent", formerFavorite: true, lastPlayedAt: 99 }),
    ];
    expect(evictionOrder(entries).map((x) => x.trackId)).toEqual([5, 2, 3, 6, 4, 1]);
  });
  test("lookahead copies (the next tracks of a queue) go before anything else", () => {
    const entries = [e(1, { kind: "recent", lastPlayedAt: 1 }), e(2, { kind: "lookahead", lastPlayedAt: 99 }), e(3, { kind: "favorite" })];
    expect(evictionOrder(entries).map((x) => x.trackId)).toEqual([2, 1, 3]);
    expect(planFit(entries, 30, 10)).toEqual([2]);
  });
  test("ties go to the one cached first", () => {
    expect(evictionOrder([e(2, { cachedAt: 9 }), e(1, { cachedAt: 3 })]).map((x) => x.trackId)).toEqual([1, 2]);
  });
});

describe("cap accounting", () => {
  test("used bytes is the sum of entries", () => expect(usedBytes([e(1, { bytes: 5 }), e(2, { bytes: 7 })])).toBe(12));

  test("fits without evicting anything", () => expect(planFit([e(1)], 30, 20)).toEqual([]));

  test("makes room by evicting recents first, oldest play first, and no more than needed", () => {
    const entries = [e(1, { kind: "favorite" }), e(2, { kind: "recent", lastPlayedAt: 5 }), e(3, { kind: "recent", lastPlayedAt: 1 })];
    expect(planFit(entries, 30, 10)).toEqual([3]);
    expect(planFit(entries, 30, 20)).toEqual([3, 2]);
  });

  test("never evicts a current favorite to make room", () => {
    expect(planFit([e(1), e(2)], 25, 10)).toBeNull();
    expect(planFit([e(1), e(2, { kind: "recent", formerFavorite: true })], 25, 10)).toEqual([2]);
  });

  test("a body bigger than the whole cap never fits", () => expect(planFit([], 10, 11)).toBeNull());

  test("lowering the cap evicts in order, favorites last", () => {
    const entries = [e(1, { lastPlayedAt: 9 }), e(2, { lastPlayedAt: 1 }), e(3, { kind: "recent" })];
    expect(enforceCap(entries, 30)).toEqual([]);
    expect(enforceCap(entries, 20)).toEqual([3]);
    expect(enforceCap(entries, 10)).toEqual([3, 2]);
    expect(enforceCap(entries, 0)).toEqual([3, 2, 1]);
  });
});

describe("lookahead budget", () => {
  const la = (id: number, over: Partial<Entry> = {}) => e(id, { kind: "lookahead", ...over });
  test("at most 5 copies: played ones go first, never the upcoming or playing ones", () => {
    const entries = [la(1, { lastPlayedAt: 0, cachedAt: 1 }), la(2, { lastPlayedAt: 9 }), la(3), la(4), la(5)];
    expect(planLookahead(entries, 10, new Set([3, 4, 5]))).toEqual([2]);
    expect(planLookahead(entries, 10, new Set([2, 3, 4, 5]))).toEqual([1]);
    expect(planLookahead(entries, 10, new Set([1, 2, 3, 4, 5]))).toBeNull();
    expect(planLookahead(entries.slice(0, 4), 10, new Set())).toEqual([]);
  });
  test("at most 100 MB", () => {
    const big = LOOKAHEAD_MAX_BYTES / 2;
    expect(planLookahead([la(1, { bytes: big })], big, new Set())).toEqual([]);
    expect(planLookahead([la(1, { bytes: big })], big + 1, new Set())).toEqual([1]);
    expect(planLookahead([la(1, { bytes: big })], big + 1, new Set([1]))).toBeNull();
    expect(planLookahead([], LOOKAHEAD_MAX_BYTES + 1, new Set())).toBeNull();
  });
});
