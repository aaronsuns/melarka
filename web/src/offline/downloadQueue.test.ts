import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { DownloadQueue, type Job, type JobResult, type QueueDeps } from "./downloadQueue";

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

const job = (trackId: number): Job => ({ trackId, kind: "favorite" });

function setup(over: Partial<QueueDeps> = {}) {
  const started: number[] = [];
  const pending = new Map<number, ReturnType<typeof deferred<JobResult>>>();
  const state = { online: true, busy: false, allowed: true };
  const deps: QueueDeps = {
    run: (j) => {
      started.push(j.trackId);
      const d = deferred<JobResult>();
      pending.set(j.trackId, d);
      return d.promise;
    },
    online: () => state.online,
    busy: () => state.busy,
    allowed: () => state.allowed,
    ...over,
  };
  const q = new DownloadQueue(deps);
  return { q, started, pending, state };
}

describe("DownloadQueue", () => {
  beforeEach(() => vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] }));
  afterEach(() => vi.useRealTimers());

  test("runs one job at a time, in order", async () => {
    const { q, started, pending } = setup();
    q.add([job(1), job(2), job(3)]);
    await vi.waitFor(() => expect(started).toEqual([1]));
    expect(q.progress()).toEqual({ done: 0, total: 3, current: 1 });
    pending.get(1)!.resolve("done");
    await vi.waitFor(() => expect(started).toEqual([1, 2]));
    pending.get(2)!.resolve("skip");
    await vi.waitFor(() => expect(started).toEqual([1, 2, 3]));
    pending.get(3)!.resolve("done");
    await vi.waitFor(() => expect(q.progress()).toBeNull());
  });

  test("a job already queued (or running) isn't added twice", async () => {
    const { q, started, pending } = setup();
    q.add([job(1), job(2)]);
    q.add([job(1), job(2)]);
    await vi.waitFor(() => expect(started).toEqual([1]));
    expect(q.progress()!.total).toBe(2);
    pending.get(1)!.resolve("done");
    await vi.waitFor(() => expect(started).toEqual([1, 2]));
  });

  test("pauses while offline and resumes when poked back online", async () => {
    const { q, started, pending, state } = setup();
    state.online = false;
    q.add([job(1)]);
    await vi.advanceTimersByTimeAsync(100);
    expect(started).toEqual([]);
    state.online = true;
    q.poke();
    await vi.waitFor(() => expect(started).toEqual([1]));
    pending.get(1)!.resolve("done");
  });

  test("a failure keeps the job and retries it with a doubling backoff, then gives up after 3 and moves on", async () => {
    const { q, started, pending } = setup();
    q.add([job(1), job(2)]);
    await vi.waitFor(() => expect(started).toEqual([1]));
    pending.get(1)!.resolve("retry");
    await vi.advanceTimersByTimeAsync(29_000);
    q.poke(); // an online event doesn't cut the backoff short
    expect(started).toEqual([1]);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(started).toEqual([1, 1]);
    pending.get(1)!.resolve("retry");
    await vi.advanceTimersByTimeAsync(59_000);
    expect(started).toEqual([1, 1]);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(started).toEqual([1, 1, 1]);
    pending.get(1)!.resolve("retry");
    await vi.waitFor(() => expect(started).toEqual([1, 1, 1, 2]));
    expect(q.gaveUp(1)).toBe(true);
    pending.get(2)!.resolve("done");
    await vi.advanceTimersByTimeAsync(0);
    q.add([job(1)]); // remembered for the session
    await vi.advanceTimersByTimeAsync(100);
    expect(started).toEqual([1, 1, 1, 2]);
  });

  test("a paused job (playback needed the network) goes back to the front without counting as a failure", async () => {
    const { q, started, pending, state } = setup();
    q.add([job(1), job(2)]);
    await vi.waitFor(() => expect(started).toEqual([1]));
    state.busy = true;
    pending.get(1)!.resolve("paused");
    await vi.advanceTimersByTimeAsync(100);
    expect(started).toEqual([1]);
    state.busy = false;
    q.poke();
    await vi.waitFor(() => expect(started).toEqual([1, 1]));
    expect(q.gaveUp(1)).toBe(false);
  });

  test("a check of a cached track and its re-download are different jobs", async () => {
    const { q, started, pending } = setup();
    q.add([{ trackId: 1, kind: "check" }]);
    await vi.waitFor(() => expect(started).toEqual([1]));
    q.add([job(1)]);
    expect(q.progress()!.total).toBe(2);
    pending.get(1)!.resolve("done");
    await vi.waitFor(() => expect(started).toEqual([1, 1]));
  });

  test("never starts while the player is buffering the current track", async () => {
    const { q, started, state } = setup();
    state.busy = true;
    q.add([job(1)]);
    await vi.advanceTimersByTimeAsync(100);
    expect(started).toEqual([]);
    state.busy = false;
    q.poke();
    await vi.waitFor(() => expect(started).toEqual([1]));
  });

  test("respects the mobile-data rule: nothing runs until allowed", async () => {
    const { q, started, state } = setup();
    state.allowed = false;
    q.add([job(1)]);
    await vi.advanceTimersByTimeAsync(100);
    expect(started).toEqual([]);
    state.allowed = true;
    q.poke();
    await vi.waitFor(() => expect(started).toEqual([1]));
  });

  test("clear drops what's queued", async () => {
    const { q, started, pending } = setup();
    q.add([job(1), job(2)]);
    await vi.waitFor(() => expect(started).toEqual([1]));
    q.clear();
    pending.get(1)!.resolve("done");
    await vi.advanceTimersByTimeAsync(100);
    expect(started).toEqual([1]);
    expect(q.progress()).toBeNull();
  });

  test("an urgent job goes first and may run where the mobile-data rule stops the rest", async () => {
    const allowed = vi.fn((j: Job) => j.urgent === true);
    const { q, started, pending } = setup({ allowed });
    q.add([job(1), job(2)]);
    await Promise.resolve();
    expect(started).toEqual([]);
    q.addFront([{ trackId: 9, kind: "lookahead", urgent: true }]);
    await vi.waitFor(() => expect(started).toEqual([9]));
    pending.get(9)!.resolve("done");
    await Promise.resolve();
    await Promise.resolve();
    expect(started).toEqual([9]);
  });

  test("addFront moves an already-queued download to the front and marks it urgent", async () => {
    const { q, started, pending, state } = setup();
    state.busy = true;
    q.add([job(1), job(2), job(3)]);
    q.addFront([{ trackId: 3, kind: "lookahead", urgent: true }]);
    state.busy = false;
    q.poke();
    await vi.waitFor(() => expect(started).toEqual([3]));
    pending.get(3)!.resolve("done");
    await vi.waitFor(() => expect(started).toEqual([3, 1]));
  });

  test("drop removes queued jobs that are no longer wanted", async () => {
    const { q, started, state } = setup();
    state.busy = true;
    q.add([job(1)]);
    q.addFront([{ trackId: 5, kind: "lookahead", urgent: true }, { trackId: 6, kind: "lookahead", urgent: true }]);
    q.drop((j) => j.kind === "lookahead" && j.trackId === 5);
    state.busy = false;
    q.poke();
    await vi.waitFor(() => expect(started).toEqual([6]));
    expect(q.progress()?.total).toBe(2);
  });
});
