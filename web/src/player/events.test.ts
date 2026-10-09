import { EventBuffer } from "./events";

const ev = (track_id: number) => ({ track_id, started_at: 1, played_seconds: 40, skipped: false, quality: "high" });

test("events survive a failed post and a reload, and are removed only after success", async () => {
  const fail = vi.fn().mockRejectedValue(new Error("offline"));
  const b1 = new EventBuffer(fail);
  b1.add(ev(1));
  b1.add(ev(2));
  await b1.flush();
  expect(b1.pending()).toHaveLength(2);
  const ids = b1.pending().map((e) => e.client_event_id);
  expect(new Set(ids).size).toBe(2);

  const ok = vi.fn().mockResolvedValue(2);
  const b2 = new EventBuffer(ok); // "reload": same localStorage
  await b2.flush();
  expect(ok).toHaveBeenCalledWith(expect.arrayContaining([expect.objectContaining({ client_event_id: ids[0] })]));
  expect(b2.pending()).toHaveLength(0);
});

test("an event added during an in-flight flush is kept", async () => {
  let release!: (n: number) => void;
  const post = vi.fn(() => new Promise<number>((r) => (release = r)));
  const b = new EventBuffer(post);
  b.add(ev(1));
  const f = b.flush();
  b.add(ev(2));
  await b.flush(); // no-op while in flight
  release(1);
  await f;
  expect(b.pending().map((e) => e.track_id)).toEqual([2]);
  expect(post).toHaveBeenCalledTimes(1);
});
