import { afterEach, expect, test } from "vitest";
import type { Episode } from "../api/types";
import { buildQueue, loadStoredQueue, saveStoredQueue, clearStoredQueue } from "./episodeQueue";

const ep = (n: number, extra: Partial<Episode> = {}): Episode => ({
  video_id: `episode000${n}`, channel_id: "UC0e5c4U67Vm6sAVK0vxN3Uw", channel_title: "频道",
  title: `第${n}集`, published_at: 1_790_000_000 + n, duration_s: 1800, kind: "video",
  thumbnail: "", audio: { status: "done", progress: 100, bytes: 1, error: "" }, video: null,
  position_s: 0, played: false, kept: false, ...extra,
});
const ids = (l: Episode[]) => l.map((e) => e.video_id.slice(-1)).join("");

afterEach(() => localStorage.clear());

test("newest and oldest sort by publish time and start at the tapped episode", () => {
  const src = [ep(2), ep(3), ep(1)];
  const n = buildQueue(src, "episode0003", "newest", true);
  expect(ids(n.list)).toBe("321");
  expect(n.index).toBe(0);
  const o = buildQueue(src, "episode0003", "oldest", true);
  expect(ids(o.list)).toBe("123");
  expect(o.index).toBe(2);
  // Play all: no tapped episode, the first in order.
  expect(buildQueue(src, null, "oldest", true).index).toBe(0);
});

test("played episodes are left out unless included; the tapped one always stays", () => {
  const src = [ep(1, { played: true }), ep(2), ep(3, { played: true }), ep(4)];
  expect(ids(buildQueue(src, null, "newest", false).list)).toBe("42");
  const tapped = buildQueue(src, "episode0003", "newest", false);
  expect(ids(tapped.list)).toBe("432");
  expect(tapped.index).toBe(1);
  expect(ids(buildQueue(src, null, "newest", true).list)).toBe("4321");
});

test("shuffle puts the tapped episode first and the rest in a random order", () => {
  const src = [ep(1), ep(2), ep(3), ep(4), ep(5)];
  let r = 0;
  const rand = () => ((r = (r * 7 + 3) % 10) / 10);
  const s = buildQueue(src, "episode0003", "shuffle", true, rand);
  expect(s.index).toBe(0);
  expect(s.list[0].video_id).toBe("episode0003");
  expect([...ids(s.list)].sort().join("")).toBe("12345");
  expect(ids(s.list)).not.toBe("31245"); // actually reordered by rand
  const all = buildQueue(src, null, "shuffle", false, () => 0);
  expect(all.index).toBe(0);
  expect(all.list).toHaveLength(5);
});

test("the queue is stored per device and read back; a bad or foreign value is ignored", () => {
  expect(loadStoredQueue(7)).toBeNull();
  saveStoredQueue(7, { source: [ep(1, { description: "long" }), ep(2)], ids: ["episode0002", "episode0001"], index: 1, order: "oldest", includePlayed: true, active: true });
  const q = loadStoredQueue(7)!;
  expect(q.ids).toEqual(["episode0002", "episode0001"]);
  expect(q.index).toBe(1);
  expect(q.order).toBe("oldest");
  expect(q.includePlayed).toBe(true);
  expect(q.active).toBe(true);
  expect(q.source[0].description).toBeUndefined(); // lists stay small
  expect(loadStoredQueue(8)).toBeNull(); // another account on this phone
  clearStoredQueue(7);
  expect(loadStoredQueue(7)).toBeNull();
  localStorage.setItem("lark.episodeQueue.7", "{nope");
  expect(loadStoredQueue(7)).toBeNull();
  localStorage.setItem("lark.episodeQueue.7", JSON.stringify({ v: 1, source: [], ids: ["x"], index: 0, order: "sideways" }));
  expect(loadStoredQueue(7)).toBeNull();
});
