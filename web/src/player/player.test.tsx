import { StrictMode } from "react";
import { act, render, waitFor } from "@testing-library/react";
import { PlayerProvider, usePlayer, type Player } from "./PlayerProvider";
import { FakeAudio, mockFetch } from "../test/setup";
import type { OnOpen, Track } from "../api/types";

const tr = (id: number, extra: Partial<Track> = {}) => ({ id, title: `t${id}`, artist: "a", album: "b", duration_ms: 200000, ...extra }) as Track;

function setup(routes: Parameters<typeof mockFetch>[0] = {}, onOpen?: OnOpen, tweak?: (a: FakeAudio) => void) {
  const f = mockFetch({
    "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] } }),
    "PUT /api/v1/queue": () => ({ status: 200, body: {} }),
    "POST /api/v1/events/play": (init) => ({ body: { accepted: JSON.parse(init.body as string).events.length } }),
    "GET /api/v1/radio/next": () => ({ body: [] }),
    ...routes,
  });
  const audio = new FakeAudio();
  tweak?.(audio);
  let p!: Player;
  function Probe() {
    p = usePlayer();
    return null;
  }
  const { unmount } = render(<PlayerProvider audio={audio as unknown as HTMLAudioElement} onOpen={onOpen}><Probe /></PlayerProvider>);
  return { audio, f, player: () => p, unmount };
}

test("playList plays synchronously with the chosen quality", () => {
  localStorage.setItem("lark.quality", "saver");
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2)], 0));
  expect(audio.play).toHaveBeenCalledTimes(1);
  expect(audio.src).toContain("/api/v1/tracks/1/stream?quality=saver");
});

test("ended advances and keeps playing", async () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2), tr(3), tr(4), tr(5)], 0));
  act(() => audio.fire("ended"));
  await waitFor(() => expect(audio.src).toContain("/tracks/2/"));
  expect(audio.play).toHaveBeenCalledTimes(2);
  expect(player().current?.id).toBe(2);
});

test("a load error skips to the next track, and stops after 3 in a row", async () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2), tr(3), tr(4), tr(5), tr(6)], 0));
  act(() => audio.fire("error"));
  await waitFor(() => expect(player().current?.id).toBe(2));
  act(() => audio.fire("error"));
  await waitFor(() => expect(player().current?.id).toBe(3));
  act(() => audio.fire("error"));
  await waitFor(() => expect(player().error).toBeTruthy());
  expect(player().current?.id).toBe(3);
});

test("skip before 30 s reports skipped=true with listened seconds", async () => {
  const { audio, f, player } = setup();
  act(() => player().playList([tr(1), tr(2), tr(3), tr(4)], 0));
  for (const t of [1, 2, 3, 4, 5]) {
    audio.currentTime = t;
    act(() => audio.fire("timeupdate"));
  }
  act(() => player().next());
  await waitFor(() => expect(f.mock.calls.some((c) => String(c[0]).includes("/events/play"))).toBe(true));
  const call = f.mock.calls.find((c) => String(c[0]).includes("/events/play"))!;
  const [e] = JSON.parse(call[1]!.body as string).events;
  expect(e).toMatchObject({ track_id: 1, skipped: true, played_seconds: 5 });
});

test("near the end of the queue, radio refills it once", async () => {
  const radio = vi.fn(() => ({ body: [tr(10), tr(11)] }));
  const { player } = setup({ "GET /api/v1/radio/next": radio });
  act(() => player().playList([tr(1), tr(2)], 0));
  await waitFor(() => expect(player().queue.tracks.map((t) => t.id)).toEqual([1, 2, 10, 11]));
  expect(radio).toHaveBeenCalledTimes(1);
  expect(String((radio.mock.calls[0] as unknown[])[1])).toContain("exclude=1%2C2");
});

// A long session's queue must not turn the refill into an
// unbounded URL — only the last 300 queue ids are sent as exclude.
test("refill sends at most the last 300 queue ids in exclude", async () => {
  const radio = vi.fn(() => ({ body: [tr(1000)] }));
  const { player } = setup({ "GET /api/v1/radio/next": radio });
  const many = Array.from({ length: 350 }, (_, i) => tr(i + 1));
  act(() => player().playList(many, 349));
  await waitFor(() => expect(radio).toHaveBeenCalledTimes(1));
  const url = new URL(String((radio.mock.calls[0] as unknown[])[1]), "http://x");
  const sent = url.searchParams.get("exclude")!.split(",").map(Number);
  expect(sent).toHaveLength(300);
  expect(sent[0]).toBe(51);
  expect(sent[299]).toBe(350);
});

// A shuffle-sourced queue refills from /tracks/random, not radio.
test("near the end of a shuffle queue, random tracks refill it (not radio)", async () => {
  const radio = vi.fn(() => ({ body: [tr(99)] }));
  const random = vi.fn(() => ({ body: [tr(10), tr(11)] }));
  const { player } = setup({ "GET /api/v1/radio/next": radio, "GET /api/v1/tracks/random": random });
  act(() => player().playList([tr(1), tr(2)], 0, { source: "shuffle" }));
  await waitFor(() => expect(player().queue.tracks.map((t) => t.id)).toEqual([1, 2, 10, 11]));
  expect(random).toHaveBeenCalledTimes(1);
  expect(String((random.mock.calls[0] as unknown[])[1])).toContain("exclude=1%2C2");
  expect(radio).not.toHaveBeenCalled();
  expect(player().queue.source).toBe("shuffle");
});

// A plain (non-shuffle) queue must still use radio, unaffected by the new branch.
test("a list queue still calls radio, not random tracks", async () => {
  const random = vi.fn(() => ({ body: [tr(99)] }));
  const radio = vi.fn(() => ({ body: [tr(10), tr(11)] }));
  const { player } = setup({ "GET /api/v1/radio/next": radio, "GET /api/v1/tracks/random": random });
  act(() => player().playList([tr(1), tr(2)], 0));
  await waitFor(() => expect(player().queue.tracks.map((t) => t.id)).toEqual([1, 2, 10, 11]));
  expect(radio).toHaveBeenCalledTimes(1);
  expect(random).not.toHaveBeenCalled();
});

test("shuffleAll primes synchronously, then plays a random slice of the library", async () => {
  const random = vi.fn(() => ({ body: [tr(20), tr(21)] }));
  const { audio, player } = setup({ "GET /api/v1/tracks/random": random });
  act(() => {
    void player().shuffleAll();
  });
  // prime() ran synchronously, inside this act(), before the fetch resolved.
  expect(audio.play).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(audio.src).toContain("/tracks/20/"));
  expect(player().queue.source).toBe("shuffle");
  expect(String((random.mock.calls[0] as unknown[])[1])).toContain("n=50");
});

// The old 2-byte range prefetch warmed nothing for a passthrough track; the
// server is now asked to prepare the next few tracks instead (once per
// change of what is upcoming).
test("the server is asked to prepare the next tracks once, instead of a 2-byte prefetch", async () => {
  const pre = vi.fn(() => ({ status: 206 }));
  const prepare = vi.fn(() => ({ status: 202, body: { queued: 3 } }));
  const { player } = setup({ "GET /api/v1/tracks/2/stream": pre, "POST /api/v1/tracks/prepare": prepare });
  act(() => player().playList([tr(1), tr(2), tr(3), tr(4)], 0));
  await waitFor(() => expect(prepare).toHaveBeenCalledTimes(1));
  expect(JSON.parse(((prepare.mock.calls[0] as unknown[])[0] as RequestInit).body as string).ids).toEqual([2, 3, 4]);
  act(() => player().updateTrack(tr(3, { title: "edited" })));
  await new Promise((r) => setTimeout(r, 20));
  expect(prepare).toHaveBeenCalledTimes(1);
  expect(pre).not.toHaveBeenCalled();
});

test("restores the server queue paused at its position", async () => {
  const { audio, player } = setup({
    "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [5, 6], current_index: 1, position_ms: 42000, version: 3, updated_by: "x", updated_at: 1 }, tracks: [tr(5), tr(6)] } }),
  });
  await waitFor(() => expect(player().current?.id).toBe(6));
  expect(audio.src).toContain("/tracks/6/");
  expect(audio.play).not.toHaveBeenCalled();
  audio.duration = 200;
  act(() => audio.fire("loadedmetadata"));
  expect(audio.currentTime).toBe(42);
});

test("queue changes are saved to the server", async () => {
  const save = vi.fn(() => ({ body: {} }));
  const { player } = setup({ "PUT /api/v1/queue": save });
  await waitFor(() => expect(player()).toBeTruthy());
  act(() => player().playList([tr(1), tr(2), tr(3), tr(4)], 1));
  await waitFor(() => expect(save).toHaveBeenCalled(), { timeout: 2500 });
  const body = JSON.parse(((save.mock.calls.at(-1) as unknown[])[0] as RequestInit).body as string);
  expect(body).toMatchObject({ track_ids: [1, 2, 3, 4], current_index: 1 });
});

test("prev restarts after 3 s, otherwise goes back", () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2), tr(3), tr(4)], 1));
  audio.currentTime = 10;
  act(() => player().prev());
  expect(audio.currentTime).toBe(0);
  expect(player().current?.id).toBe(2);
  act(() => player().prev());
  expect(player().current?.id).toBe(1);
});

// Controller addition: StrictMode double-invokes effects in dev (main.tsx wraps
// the app in <StrictMode>). The restore effect must survive that without
// loading or duplicating anything.
test("under StrictMode, restore runs its effect twice but loads the queue only once", async () => {
  let p!: Player;
  function Probe() {
    p = usePlayer();
    return null;
  }
  const f = mockFetch({
    "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [5, 6], current_index: 1, position_ms: 42000, version: 3, updated_by: "x", updated_at: 1 }, tracks: [tr(5), tr(6)] } }),
    "PUT /api/v1/queue": () => ({ status: 200, body: {} }),
    "POST /api/v1/events/play": (init) => ({ body: { accepted: JSON.parse(init.body as string).events.length } }),
    "GET /api/v1/radio/next": () => ({ body: [] }),
  });
  const audio = new FakeAudio();
  render(
    <StrictMode>
      <PlayerProvider audio={audio as unknown as HTMLAudioElement}>
        <Probe />
      </PlayerProvider>
    </StrictMode>,
  );
  await waitFor(() => expect(p.current?.id).toBe(6));
  // Both StrictMode invocations hit the network (confirms the double-invoke
  // actually happened here), but only the first result is ever applied.
  expect(f.mock.calls.filter((c) => String(c[0]).includes("/queue") && (c[1]?.method ?? "GET") === "GET").length).toBe(2);
  expect(p.queue.tracks.map((t) => t.id)).toEqual([5, 6]);
  expect(audio.play).not.toHaveBeenCalled();
  expect(audio.src).toContain("/tracks/6/");
});

// Controller addition: duplicate ids in the queue must still advance and reload.
test("duplicate ids: ending on the first of two identical tracks replays from the top", async () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(1), tr(2)], 0));
  const playsBefore = audio.play.mock.calls.length;
  audio.currentTime = 50;
  act(() => audio.fire("ended"));
  await waitFor(() => expect(player().queue.index).toBe(1));
  expect(player().current?.id).toBe(1);
  expect(audio.currentTime).toBe(0);
  expect(audio.play.mock.calls.length).toBeGreaterThan(playsBefore);
});

// Only an actual navigation (next/prev/jump/
// ended/error) should restart a same-id slot. A plain queue edit that
// renumbers the currently-playing instance must leave it alone.
test("removing an earlier track does not restart the one that's playing", async () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2), tr(3)], 1)); // track 2 is current
  const playsBefore = audio.play.mock.calls.length;
  audio.currentTime = 90;
  act(() => player().remove(1));
  await waitFor(() => expect(player().queue.tracks.map((t) => t.id)).toEqual([2, 3]));
  expect(player().queue.index).toBe(0);
  expect(player().current?.id).toBe(2);
  expect(audio.currentTime).toBe(90);
  expect(audio.play.mock.calls.length).toBe(playsBefore);
});

test("enqueueNext of an earlier track does not restart the one that's playing", async () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2), tr(3)], 1)); // track 2 is current
  const playsBefore = audio.play.mock.calls.length;
  audio.currentTime = 42;
  act(() => player().enqueueNext(tr(1))); // track 1 is already earlier in the queue
  await waitFor(() => expect(player().queue.tracks.map((t) => t.id)).toEqual([2, 1, 3]));
  expect(player().queue.index).toBe(0);
  expect(player().current?.id).toBe(2);
  expect(audio.currentTime).toBe(42);
  expect(audio.play.mock.calls.length).toBe(playsBefore);
});

// A broken track that repeats in the queue
// must never leave the element silently erroring. Since never-stop, a
// track that failed is remembered for the session: its repeat is skipped
// and the next track is loaded for real (src reassigned).
test("a broken track that repeats in the queue is skipped, not silently left erroring", async () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(1), tr(2)], 0));
  act(() => audio.fire("error"));
  await waitFor(() => expect(player().current?.id).toBe(2));
  expect(audio.errored).toBe(false);
  expect(audio.src).toContain("/tracks/2/");
  expect(player().error).toBeNull();
});

test("a duplicate of a track that played fine is still reloaded when navigation reaches it", async () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(1), tr(2)], 0));
  act(() => player().next());
  expect(player().queue.index).toBe(1);
  expect(audio.errored).toBe(false);
  expect(audio.play.mock.calls.length).toBe(2);
});

// The audio element must be released on
// unmount (e.g. logout drops PlayerProvider) instead of playing on,
// detached, with no controls.
test("unmounting pauses the audio element", () => {
  const { audio, player, unmount } = setup();
  act(() => player().playList([tr(1), tr(2)], 0));
  act(() => unmount());
  expect(audio.pause).toHaveBeenCalled();
  expect(audio.removeAttribute).toHaveBeenCalledWith("src");
});

// next() at the last track is a no-op for
// the reducer (it clamps to the same index), so the "load whatever became
// current" effect never runs to consume any navigation marker the call set
// — a plain boolean flag would go stale here and wrongly fire on the next,
// unrelated queue edit.
test("next() at the end of the queue leaves no stale navigation state for a later remove()", async () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2), tr(3)], 2)); // already on the last track
  const playsBefore = audio.play.mock.calls.length;
  act(() => player().next()); // clamped: nothing moves
  audio.currentTime = 90;
  act(() => player().remove(1)); // remove an earlier track
  await waitFor(() => expect(player().queue.tracks.map((t) => t.id)).toEqual([2, 3]));
  expect(player().current?.id).toBe(3);
  expect(audio.currentTime).toBe(90);
  expect(audio.play.mock.calls.length).toBe(playsBefore);
});

// jump() to the already-current index is
// also a no-op for cur/queue.index (same track, same slot), so the effect
// never runs afterward either — jump() must not leave anything for it to
// find stale later.
test("jump() to the current index leaves no stale navigation state for a later remove()", async () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2), tr(3)], 1));
  act(() => player().jump(1)); // already current: restarts it synchronously
  const playsAfterJump = audio.play.mock.calls.length;
  audio.currentTime = 90;
  act(() => player().remove(1)); // remove an earlier track (id 1)
  await waitFor(() => expect(player().queue.tracks.map((t) => t.id)).toEqual([2, 3]));
  expect(player().current?.id).toBe(2);
  expect(audio.currentTime).toBe(90);
  expect(audio.play.mock.calls.length).toBe(playsAfterJump);
});

// jump() must play synchronously, like
// playList, to satisfy the iOS gesture rule (play() has to happen inside
// the tap handler, not after an effect runs later).
test("jump plays synchronously like playList", () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2), tr(3)], 0));
  const playsBefore = audio.play.mock.calls.length;
  act(() => player().jump(2));
  expect(audio.play.mock.calls.length).toBe(playsBefore + 1);
  expect(audio.src).toContain("/tracks/3/");
  expect(player().current?.id).toBe(3);
});

// Restoring a queue with duplicate ids must
// use the server's reported position, not just the first matching id.
test("restore prefers the position at current_index when it matches, even with duplicate ids", async () => {
  const { player } = setup({
    "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [7, 7], current_index: 1, position_ms: 5000, version: 1, updated_by: "x", updated_at: 1 }, tracks: [tr(7), tr(7)] } }),
  });
  await waitFor(() => expect(player().queue.tracks.length).toBe(2));
  expect(player().queue.index).toBe(1);
});

// A real race — the restore fetch resolving
// after the user already started their own playList() — must not clobber
// what the user picked. (The existing StrictMode test only exercises two
// restore calls racing each other; this one is the genuine "restore vs.
// user action" race the guard is actually meant for.)
test("a slow restore fetch does not clobber a playList started first", async () => {
  let resolveQueue!: (r: Response) => void;
  const queuePromise = new Promise<Response>((r) => {
    resolveQueue = r;
  });
  const fn = vi.fn((input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = typeof input === "string" ? input : input.toString();
    const path = url.split("?")[0];
    const method = (init.method ?? "GET").toUpperCase();
    if (method === "GET" && path === "/api/v1/queue") return queuePromise;
    if (method === "PUT" && path === "/api/v1/queue") return Promise.resolve(new Response(JSON.stringify({}), { status: 200 }));
    if (method === "POST" && path === "/api/v1/events/play") {
      const events = JSON.parse(init.body as string).events;
      return Promise.resolve(new Response(JSON.stringify({ accepted: events.length }), { status: 200 }));
    }
    if (method === "GET" && path === "/api/v1/radio/next") return Promise.resolve(new Response(JSON.stringify([]), { status: 200 }));
    return Promise.resolve(new Response(JSON.stringify({ error: `no mock for ${method} ${path}` }), { status: 599 }));
  });
  vi.stubGlobal("fetch", fn);

  const audio = new FakeAudio();
  let p!: Player;
  function Probe() {
    p = usePlayer();
    return null;
  }
  render(<PlayerProvider audio={audio as unknown as HTMLAudioElement}><Probe /></PlayerProvider>);

  act(() => p.playList([tr(1), tr(2)], 0));
  expect(p.current?.id).toBe(1);

  await act(async () => {
    resolveQueue(
      new Response(
        JSON.stringify({ queue: { track_ids: [5, 6], current_index: 1, position_ms: 0, version: 1, updated_by: "", updated_at: 0 }, tracks: [tr(5), tr(6)] }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    await Promise.resolve();
    await Promise.resolve();
  });

  expect(p.queue.tracks.map((t) => t.id)).toEqual([1, 2]);
  expect(p.current?.id).toBe(1);
  expect(audio.src).toContain("/tracks/1/");
});

// `pointerdown` is not a user-activation event on iOS Safari — only
// `click`/`touchend` are — so the unlock has to listen on those instead.
test("the first tap unlocks the audio element once", async () => {
  const { audio } = setup();
  document.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  await Promise.resolve();
  document.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  await Promise.resolve();
  expect(audio.play).toHaveBeenCalledTimes(1);
  expect(audio.pause).toHaveBeenCalledTimes(1);
});

// A tap that iOS doesn't count as a user gesture (or any other rejection)
// must not spend the unlock: the next tap has to get another try.
test("a rejected unlock attempt is retried on the next tap", async () => {
  const { audio } = setup();
  let rejectNext = true;
  audio.play = vi.fn(() => {
    if (rejectNext) {
      rejectNext = false;
      return Promise.reject(new Error("not a user gesture"));
    }
    audio.paused = false;
    audio.fire("play");
    return Promise.resolve();
  });
  document.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  await Promise.resolve();
  await Promise.resolve();
  expect(audio.play).toHaveBeenCalledTimes(1);
  expect(audio.pause).not.toHaveBeenCalled();

  document.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  await Promise.resolve();
  await Promise.resolve();
  expect(audio.play).toHaveBeenCalledTimes(2);
  expect(audio.pause).toHaveBeenCalledTimes(1);
});

// ---- Restoring, saving and retrying ----

const restoreAt42 = {
  "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [5, 6], current_index: 1, position_ms: 42000, version: 3, updated_by: "x", updated_at: 1 }, tracks: [tr(5), tr(6)] } }),
};
const click = () => document.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));

// A restored track is only loaded, never played from a
// gesture — iOS still needs the first tap to prime the element.
test("a restored queue does not count as unlocked: the first tap primes the restored track, muted", async () => {
  const { audio, player } = setup(restoreAt42);
  await waitFor(() => expect(player().current?.id).toBe(6));
  let mutedDuringPlay: boolean | null = null;
  const realPlay = audio.play;
  audio.play = vi.fn(() => {
    mutedDuringPlay = audio.muted;
    return realPlay();
  });
  click();
  expect(audio.play).toHaveBeenCalledTimes(1);
  expect(mutedDuringPlay).toBe(true);
  expect(audio.pause).toHaveBeenCalledTimes(1);
  expect(audio.muted).toBe(false);
  expect(audio.src).toContain("/tracks/6/"); // the restored track itself, not the silent clip
  click(); // already primed: no second play
  expect(audio.play).toHaveBeenCalledTimes(1);
});

test("prime() plays synchronously, and is a no-op once the element has really played", () => {
  const { audio, player } = setup();
  player().prime();
  expect(audio.play).toHaveBeenCalledTimes(1); // no await in between: still inside the tap
  act(() => player().playList([tr(1), tr(2)], 0));
  act(() => audio.fire("playing"));
  const plays = audio.play.mock.calls.length;
  player().prime();
  click();
  expect(audio.play.mock.calls.length).toBe(plays);
});

// The save right after a restore must not overwrite the
// restored position with 0 (currentTime is 0 until metadata arrives).
test("a restored position is not overwritten with 0 by the next save", async () => {
  const save = vi.fn(() => ({ body: {} }));
  const { player } = setup({ ...restoreAt42, "PUT /api/v1/queue": save });
  await waitFor(() => expect(player().current?.id).toBe(6));
  await new Promise((r) => setTimeout(r, 1500));
  for (const c of save.mock.calls) {
    expect(JSON.parse(((c as unknown[])[0] as RequestInit).body as string).position_ms).toBe(42000);
  }
  // A queue edit before the metadata (and the seek) lands still saves 42 s.
  act(() => player().enqueueNext(tr(9)));
  await waitFor(() => expect(save).toHaveBeenCalled(), { timeout: 2500 });
  const body = JSON.parse(((save.mock.calls.at(-1) as unknown[])[0] as RequestInit).body as string);
  expect(body).toMatchObject({ track_ids: [5, 6, 9], current_index: 1, position_ms: 42000 });
});

// A network error (4G dead zone) retries the same track at
// the same position with backoff instead of skipping through the queue.
test("a network error retries the same track at its position with backoff, never skipping", async () => {
  vi.useFakeTimers();
  try {
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3), tr(4), tr(5)], 0));
    audio.currentTime = 80;
    act(() => audio.fire("timeupdate"));
    const plays = audio.play.mock.calls.length;
    for (let i = 0; i < 3; i++) {
      audio.error = { code: 2 };
      act(() => audio.fire("error"));
      expect(player().current?.id).toBe(1);
      expect(player().error).toBe("网络中断，正在重试…");
    }
    // Backoff: the third error scheduled a 10 s retry.
    act(() => vi.advanceTimersByTime(9000));
    expect(audio.errored).toBe(true);
    act(() => vi.advanceTimersByTime(1000));
    expect(audio.errored).toBe(false); // src reassigned
    expect(audio.src).toContain("/tracks/1/");
    expect(audio.play.mock.calls.length).toBe(plays + 1);
    act(() => audio.fire("loadedmetadata"));
    expect(audio.currentTime).toBe(80);
    act(() => audio.fire("playing"));
    expect(player().error).toBeNull();
    expect(player().current?.id).toBe(1);
  } finally {
    vi.useRealTimers();
  }
});

test("the first network retry comes after 2 s, and coming back online retries at once", async () => {
  vi.useFakeTimers();
  try {
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    audio.error = { code: 2 };
    act(() => audio.fire("error"));
    act(() => vi.advanceTimersByTime(1999));
    expect(audio.errored).toBe(true);
    act(() => vi.advanceTimersByTime(1));
    expect(audio.errored).toBe(false);

    audio.error = { code: 2 };
    act(() => audio.fire("error"));
    act(() => window.dispatchEvent(new Event("online")));
    expect(audio.errored).toBe(false);
    expect(player().current?.id).toBe(1);
  } finally {
    vi.useRealTimers();
  }
});

test("a decode error still skips to the next track", async () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2), tr(3)], 0));
  audio.error = { code: 3 };
  act(() => audio.fire("error"));
  await waitFor(() => expect(player().current?.id).toBe(2));
  expect(audio.src).toContain("/tracks/2/");
});

// One failed radio request must not disable radio forever.
test("a failed radio refill is retried on the next queue change", async () => {
  let calls = 0;
  const radio = vi.fn(() => (++calls === 1 ? { status: 503, body: { error: "offline" } } : { body: [tr(10), tr(11)] }));
  const { player } = setup({ "GET /api/v1/radio/next": radio });
  act(() => player().playList([tr(1), tr(2)], 0));
  await waitFor(() => expect(radio).toHaveBeenCalledTimes(1));
  await new Promise((r) => setTimeout(r, 0));
  act(() => player().enqueueNext(tr(3)));
  await waitFor(() => expect(player().queue.tracks.map((t) => t.id)).toEqual([1, 3, 2, 10, 11]));
  expect(radio).toHaveBeenCalledTimes(2);
});

// Removing the only / last track.
test("removing the only track stops, releases the element and saves the empty queue", async () => {
  const save = vi.fn(() => ({ body: {} }));
  const { audio, player } = setup({ "PUT /api/v1/queue": save });
  await waitFor(() => expect(player()).toBeTruthy());
  await new Promise((r) => setTimeout(r, 0)); // let the (empty) restore finish
  act(() => player().playList([tr(1)], 0));
  act(() => audio.fire("playing"));
  act(() => player().remove(1));
  expect(audio.pause).toHaveBeenCalled();
  expect(audio.removeAttribute).toHaveBeenCalledWith("src");
  expect(player().current).toBeNull();
  await waitFor(() => expect(save).toHaveBeenCalled(), { timeout: 2500 });
  const body = JSON.parse(((save.mock.calls.at(-1) as unknown[])[0] as RequestInit).body as string);
  expect(body).toMatchObject({ track_ids: [], current_index: 0, position_ms: 0 });
});

test("removing the current track when it is the last one stops instead of playing the previous", async () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2), tr(3)], 2));
  const plays = audio.play.mock.calls.length;
  act(() => player().remove(3));
  await waitFor(() => expect(player().current?.id).toBe(2));
  expect(audio.src).toContain("/tracks/2/");
  expect(audio.play.mock.calls.length).toBe(plays);
  expect(audio.paused).toBe(true);
});

// Pending events are stored per user, so they can
// never be replayed under another account.
test("pending play events are stored under the user's own key", async () => {
  mockFetch({
    "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] } }),
    "POST /api/v1/events/play": () => ({ status: 503, body: { error: "down" } }),
    "GET /api/v1/radio/next": () => ({ body: [] }),
    "PUT /api/v1/queue": () => ({ body: {} }),
  });
  const audio = new FakeAudio();
  let p!: Player;
  function Probe() {
    p = usePlayer();
    return null;
  }
  const { unmount } = render(<PlayerProvider audio={audio as unknown as HTMLAudioElement} userId={7}><Probe /></PlayerProvider>);
  act(() => p.playList([tr(1), tr(2), tr(3)], 0));
  for (const t of [1, 2, 3]) {
    audio.currentTime = t;
    act(() => audio.fire("timeupdate"));
  }
  await act(async () => {
    await p.flushEvents();
  });
  expect(JSON.parse(localStorage.getItem("lark.pendingEvents.7") ?? "[]")).toHaveLength(1);
  expect(localStorage.getItem("lark.pendingEvents")).toBeNull();
  act(() => unmount());

  // Another account on the same browser never sends user 7's events.
  const f = mockFetch({
    "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] } }),
    "POST /api/v1/events/play": (init) => ({ body: { accepted: JSON.parse(init.body as string).events.length } }),
  });
  render(<PlayerProvider audio={new FakeAudio() as unknown as HTMLAudioElement} userId={8}><Probe /></PlayerProvider>);
  await act(async () => {
    await p.flushEvents();
  });
  expect(f.mock.calls.some((c) => String(c[0]).includes("/events/play"))).toBe(false);
  expect(JSON.parse(localStorage.getItem("lark.pendingEvents.7") ?? "[]")).toHaveLength(1);
});

// Save on pagehide / hidden, with keepalive.
test("the queue is saved with keepalive when the page is hidden", async () => {
  const save = vi.fn(() => ({ body: {} }));
  const { audio, player } = setup({ "PUT /api/v1/queue": save });
  await new Promise((r) => setTimeout(r, 0));
  act(() => player().playList([tr(1), tr(2), tr(3)], 1));
  audio.currentTime = 33;
  act(() => window.dispatchEvent(new Event("pagehide")));
  expect(save).toHaveBeenCalledTimes(1);
  const init = (save.mock.calls[0] as unknown[])[0] as RequestInit;
  expect(init.keepalive).toBe(true);
  expect(JSON.parse(init.body as string)).toMatchObject({ track_ids: [1, 2, 3], current_index: 1, position_ms: 33000 });

  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
  try {
    act(() => document.dispatchEvent(new Event("visibilitychange")));
  } finally {
    delete (document as unknown as { visibilityState?: string }).visibilityState;
  }
  expect(save).toHaveBeenCalledTimes(2);
  expect(((save.mock.calls[1] as unknown[])[0] as RequestInit).keepalive).toBe(true);
});

// ---- Media Session lock-screen fixes ----

function stubMediaSession() {
  const setActionHandler = vi.fn();
  const setPositionState = vi.fn();
  const ms = { setActionHandler, setPositionState, metadata: null, playbackState: "none" };
  Object.defineProperty(navigator, "mediaSession", { value: ms, configurable: true, writable: true });
  return { setActionHandler, setPositionState, cleanup: () => { delete (navigator as unknown as { mediaSession?: unknown }).mediaSession; } };
}

test("media session metadata carries the 300 and 1000 px covers", () => {
  const { cleanup } = stubMediaSession();
  vi.stubGlobal("MediaMetadata", class { constructor(public init: MediaMetadataInit) {} });
  try {
    const { player } = setup();
    act(() => player().playList([tr(1), tr(2)], 0));
    const md = (navigator.mediaSession as unknown as { metadata: { init: MediaMetadataInit } }).metadata.init;
    expect(md.artwork).toEqual([
      { src: `${location.origin}/api/v1/tracks/1/cover?size=300`, sizes: "300x300", type: "image/jpeg" },
      { src: `${location.origin}/api/v1/tracks/1/cover?size=1000`, sizes: "1000x1000", type: "image/jpeg" },
    ]);
    act(() => player().next());
    expect((navigator.mediaSession as unknown as { metadata: { init: MediaMetadataInit } }).metadata.init.artwork?.[0].src).toContain("/tracks/2/cover");
  } finally {
    cleanup();
  }
});

test("updateTrack replaces the current track's metadata in place without restarting playback", () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1), tr(2)], 0));
  const srcBefore = audio.src;
  act(() => player().updateTrack({ ...tr(1), title: "新标题", artist: "新歌手" } as Track));
  expect(player().current).toEqual(expect.objectContaining({ id: 1, title: "新标题", artist: "新歌手" }));
  expect(audio.src).toBe(srcBefore); // no reload: still the same instance
  expect(audio.play).toHaveBeenCalledTimes(1); // only the original playList() play
});

test("updateTrack also updates a non-current entry in the queue", () => {
  const { player } = setup();
  act(() => player().playList([tr(1), tr(2), tr(3)], 0));
  act(() => player().updateTrack({ ...tr(3), title: "新标题" } as Track));
  expect(player().queue.tracks.find((t) => t.id === 3)?.title).toBe("新标题");
  expect(player().current?.id).toBe(1); // unaffected
});

test("media session nulls out seekbackward/seekforward and re-registers handlers on every play", () => {
  const { setActionHandler, cleanup } = stubMediaSession();
  try {
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2)], 0));
    const actionsOf = (calls: unknown[][]) => calls.map((c) => c[0]);
    expect(actionsOf(setActionHandler.mock.calls)).toEqual(expect.arrayContaining(["play", "pause", "nexttrack", "previoustrack", "seekto", "seekbackward", "seekforward"]));
    expect(setActionHandler.mock.calls.find((c) => c[0] === "seekbackward")?.[1]).toBeNull();
    expect(setActionHandler.mock.calls.find((c) => c[0] === "seekforward")?.[1]).toBeNull();
    const callsBefore = setActionHandler.mock.calls.length;
    act(() => audio.fire("play"));
    expect(setActionHandler.mock.calls.length).toBeGreaterThan(callsBefore); // re-registered
  } finally {
    cleanup();
  }
});

test("media session reports position state on loadedmetadata and throttled timeupdate", () => {
  const { setPositionState, cleanup } = stubMediaSession();
  try {
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2)], 0));
    audio.duration = 120;
    audio.currentTime = 5;
    act(() => audio.fire("loadedmetadata"));
    expect(setPositionState).toHaveBeenCalledWith({ duration: 120, position: 5, playbackRate: 1 });
    const callsAfterMeta = setPositionState.mock.calls.length;
    audio.currentTime = 6;
    act(() => audio.fire("timeupdate")); // within the same second: throttled away
    expect(setPositionState.mock.calls.length).toBe(callsAfterMeta);
  } finally {
    cleanup();
  }
});

test("media session skips setPositionState while duration is not finite", () => {
  const { setPositionState, cleanup } = stubMediaSession();
  try {
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2)], 0));
    audio.duration = NaN;
    act(() => audio.fire("loadedmetadata"));
    expect(setPositionState).not.toHaveBeenCalled();
  } finally {
    cleanup();
  }
});

// ---- Start with favorites ----

const notAllowed = () => Promise.reject(Object.assign(new Error("blocked"), { name: "NotAllowedError" }));
const favs = (...ids: number[]) => ({ "GET /api/v1/tracks/random": () => ({ body: { source: "favorites", tracks: ids.map((i) => tr(i)) } }) });

test("shuffle_favorites: refused autoplay → needsTap; the first tap anywhere starts that queue once", async () => {
  const { audio, f, player } = setup({ "GET /api/v1/tracks/random": () => ({ body: { source: "favorites", tracks: [tr(7), tr(8), tr(9)] } }) }, "shuffle_favorites", (a) => a.play.mockImplementationOnce(notAllowed));
  await waitFor(() => expect(player().needsTap).toBe(true));
  expect(audio.src).toContain("/tracks/7/");
  expect(player().queue.source).toBe("favorites");
  expect(f.mock.calls.some((c) => String(c[0]).startsWith("/api/v1/queue") && (c[1]?.method ?? "GET") === "GET")).toBe(false);
  expect(String(f.mock.calls.find((c) => String(c[0]).includes("/tracks/random"))![0])).toContain("source=favorites");
  const src = audio.src;
  const plays = audio.play.mock.calls.length;
  act(() => document.body.click());
  await waitFor(() => expect(player().needsTap).toBe(false));
  expect(audio.play.mock.calls.length).toBe(plays + 1);
  expect(audio.src).toBe(src); // same track, not reloaded, never the silent clip
  expect(audio.paused).toBe(false);
  act(() => document.body.click());
  expect(audio.play.mock.calls.length).toBe(plays + 1); // a second tap does nothing more
});

// A tap the browser still doesn't count as a gesture must not swallow the
// prompt: it comes back, and the next tap tries again.
test("shuffle_favorites: a refused tap keeps the prompt for the next tap", async () => {
  const { audio, player } = setup(favs(7, 8, 9), "shuffle_favorites", (a) => a.play.mockImplementationOnce(notAllowed).mockImplementationOnce(notAllowed));
  await waitFor(() => expect(player().needsTap).toBe(true));
  act(() => document.body.click());
  await waitFor(() => expect(player().needsTap).toBe(true));
  expect(audio.paused).toBe(true);
  act(() => document.body.click());
  await waitFor(() => expect(player().needsTap).toBe(false));
  expect(audio.paused).toBe(false);
  expect(audio.src).toContain("/tracks/7/");
});

test("shuffle_favorites: playback started elsewhere (lock screen) clears the prompt", async () => {
  const { audio, player } = setup(favs(7, 8, 9), "shuffle_favorites", (a) => a.play.mockImplementationOnce(notAllowed));
  await waitFor(() => expect(player().needsTap).toBe(true));
  act(() => audio.fire("playing"));
  await waitFor(() => expect(player().needsTap).toBe(false));
});

test("shuffle_favorites: allowed autoplay just plays", async () => {
  const { audio, player } = setup({ "GET /api/v1/tracks/random": () => ({ body: { source: "all", tracks: [tr(3)] } }) }, "shuffle_favorites");
  await waitFor(() => expect(audio.paused).toBe(false));
  expect(player().needsTap).toBe(false);
  expect(player().queue.source).toBe("shuffle");
});

test("nothing: no restore, no shuffle", async () => {
  const { f, player } = setup({}, "nothing");
  await new Promise((r) => setTimeout(r, 20));
  expect(f.mock.calls.filter((c) => /\/queue|\/tracks\/random/.test(String(c[0])))).toEqual([]);
  expect(player().current).toBeNull();
});

test("a favorites queue refills from favorites, excluding what it already holds", async () => {
  const { player, f } = setup({ "GET /api/v1/tracks/random": (_i, url) => ({ body: { source: "favorites", tracks: url.includes("exclude") ? [tr(20)] : [tr(7), tr(8)] } }) }, "shuffle_favorites");
  await waitFor(() => expect(player().queue.tracks.map((x) => x.id)).toContain(20));
  const refill = f.mock.calls.map((c) => String(c[0])).find((u) => u.includes("exclude="))!;
  expect(refill).toContain("source=favorites");
  expect(refill).toMatch(/exclude=7(%2C|,)8/);
});

// Favorites loop. Once every favorite is queued the server
// reshuffles them all, and that pass is appended (repeats allowed — only
// what is still upcoming is deduped), so 2 favorites never run dry.
test("once every favorite is queued, a refill appends a reshuffled pass", async () => {
  const random = vi.fn((_i: RequestInit, url: string) => ({ body: { source: "favorites", tracks: url.includes("exclude") ? [tr(8), tr(7)] : [tr(7), tr(8)] } }));
  const { player, audio } = setup({ "GET /api/v1/tracks/random": random }, "shuffle_favorites");
  // 8 is still upcoming, so only 7 joins: [7, 8, 7].
  await waitFor(() => expect(player().queue.tracks.map((x) => x.id)).toEqual([7, 8, 7]));
  act(() => player().next());
  await waitFor(() => expect(player().queue.tracks.map((x) => x.id)).toEqual([7, 8, 7, 8]));
  act(() => player().next()); // onto the repeated 7: a real (re)load of that position
  await waitFor(() => expect(player().queue.index).toBe(2));
  await waitFor(() => expect(player().queue.tracks.map((x) => x.id)).toEqual([7, 8, 7, 8, 7]));
  expect(audio.src).toContain("/tracks/7/");
  // No request storm: every refill either appended or waited for the next queue change.
  const n = random.mock.calls.length;
  await new Promise((r) => setTimeout(r, 50));
  expect(random.mock.calls.length).toBe(n);
});

// A refill that adds nothing at all (no tracks came back) still stops.
test("a favorites refill that returns nothing stops refilling", async () => {
  const random = vi.fn((_i: RequestInit, url: string) => ({ body: { source: "favorites", tracks: url.includes("exclude") ? [] : [tr(7), tr(8)] } }));
  const { player } = setup({ "GET /api/v1/tracks/random": random }, "shuffle_favorites");
  await waitFor(() => expect(random).toHaveBeenCalledTimes(2));
  act(() => player().next());
  await new Promise((r) => setTimeout(r, 50));
  expect(random).toHaveBeenCalledTimes(2);
  expect(player().queue.tracks.map((x) => x.id)).toEqual([7, 8]);
});

// A tap on an episode/preview control never starts the music queue.
test("prime ignores taps inside [data-no-music-prime]", async () => {
  const { audio, player } = setup({
    "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [1], current_index: 0, position_ms: 0, version: 1, updated_by: "", updated_at: 0 }, tracks: [tr(1)] } }),
  });
  await waitFor(() => expect(player().current?.id).toBe(1));
  audio.play.mockClear();
  const box = document.createElement("div");
  box.setAttribute("data-no-music-prime", "");
  const btn = document.createElement("button");
  box.appendChild(btn);
  document.body.appendChild(box);
  btn.click();
  expect(audio.play).not.toHaveBeenCalled();
  document.body.click();
  expect(audio.play).toHaveBeenCalled(); // any other tap still primes
  box.remove();
});

test("loudness normalization: each track's gain_db sets the volume, an unmeasured one plays at unity", async () => {
  const { audio, player } = setup();
  act(() => player().playList([tr(1, { gain_db: -6 }), tr(2, { gain_db: null }), tr(3)], 0));
  expect(audio.volume).toBeCloseTo(0.501, 3);
  act(() => audio.fire("ended"));
  await waitFor(() => expect(player().current?.id).toBe(2));
  expect(audio.volume).toBe(1);
  act(() => player().prev());
  await waitFor(() => expect(player().current?.id).toBe(1));
  expect(audio.volume).toBeCloseTo(0.501, 3);
  act(() => player().next());
  act(() => player().next());
  await waitFor(() => expect(player().current?.id).toBe(3));
  expect(audio.volume).toBe(1); // a track from an older server, without the field
});

test("loudness normalization off plays at unity, and the switch applies at once to the loaded track", () => {
  mockFetch({
    "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] } }),
    "PUT /api/v1/queue": () => ({ status: 200, body: {} }),
    "GET /api/v1/radio/next": () => ({ body: [] }),
  });
  const audio = new FakeAudio();
  let p!: Player;
  function Probe() {
    p = usePlayer();
    return null;
  }
  const ui = (loudness: boolean) => (
    <PlayerProvider audio={audio as unknown as HTMLAudioElement} loudness={loudness}><Probe /></PlayerProvider>
  );
  const { rerender } = render(ui(false));
  act(() => p.playList([tr(1, { gain_db: -6 })], 0));
  expect(audio.volume).toBe(1);
  rerender(ui(true));
  expect(audio.volume).toBeCloseTo(0.501, 3);
  rerender(ui(false));
  expect(audio.volume).toBe(1);
});

// Shuffle and repeat. Modes are stored per device ("lark.modes", or
// "lark.modes.<userId>") and read when the player mounts.
describe("shuffle and repeat", () => {
  const storeModes = (m: object, key = "lark.modes") => localStorage.setItem(key, JSON.stringify(m));
  const ids = (p: Player) => p.queue.tracks.map((t) => t.id);
  const listenFor = (audio: FakeAudio, seconds: number) => {
    for (let s = 1; s <= seconds; s++) {
      audio.currentTime = s;
      act(() => audio.fire("timeupdate"));
    }
  };
  afterEach(() => vi.restoreAllMocks());

  test("repeat one: a natural end replays the same track, and each loop is a new play", async () => {
    storeModes({ shuffle: false, repeat: "one", original: null });
    // The server is down: the events stay buffered, where they can be counted.
    const { audio, player } = setup({ "POST /api/v1/events/play": () => ({ status: 500, body: {} }) });
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    expect(player().modes).toEqual({ shuffle: false, repeat: "one" });
    listenFor(audio, 5);
    act(() => audio.fire("ended"));
    expect(audio.src).toContain("/tracks/1/");
    expect(player().current?.id).toBe(1);
    expect(player().queue.index).toBe(0);
    expect(audio.play).toHaveBeenCalledTimes(2);
    listenFor(audio, 5);
    act(() => audio.fire("ended"));
    expect(audio.src).toContain("/tracks/1/");
    expect(audio.play).toHaveBeenCalledTimes(3);
    await waitFor(() => {
      const pending = JSON.parse(localStorage.getItem("lark.pendingEvents") ?? "[]") as { track_id: number; skipped: boolean }[];
      expect(pending.map((e) => [e.track_id, e.skipped])).toEqual([[1, false], [1, false]]);
    });
  });

  test("repeat one restarts in place: no new src, so no network fetch", () => {
    storeModes({ shuffle: false, repeat: "one", original: null });
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2)], 0));
    const srcSet = vi.spyOn(audio, "src", "set");
    audio.currentTime = 199;
    act(() => audio.fire("ended"));
    expect(srcSet).not.toHaveBeenCalled();
    expect(audio.currentTime).toBe(0);
    expect(audio.play).toHaveBeenCalledTimes(2);
    expect(player().current?.id).toBe(1);
  });

  test("repeat one: next() still moves to the next track", () => {
    storeModes({ shuffle: false, repeat: "one", original: null });
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    act(() => player().next());
    expect(audio.src).toContain("/tracks/2/");
    expect(player().current?.id).toBe(2);
  });

  test("repeat all: the end of the queue plays its first track again, with no refill", async () => {
    storeModes({ shuffle: false, repeat: "all", original: null });
    const radio = vi.fn(() => ({ body: [tr(10)] }));
    // Both shuffle and favorites refills ask /tracks/random.
    const random = vi.fn(() => ({ body: [tr(20)] }));
    const { audio, player } = setup({ "GET /api/v1/radio/next": radio, "GET /api/v1/tracks/random": random });
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    act(() => audio.fire("ended"));
    act(() => audio.fire("ended"));
    expect(player().current?.id).toBe(3);
    act(() => audio.fire("ended"));
    expect(player().current?.id).toBe(1);
    expect(player().queue.index).toBe(0);
    expect(audio.src).toContain("/tracks/1/");
    expect(audio.play).toHaveBeenCalledTimes(4);
    expect(ids(player())).toEqual([1, 2, 3]);
    // A shuffle and a favorites queue don't refill either.
    act(() => player().playList([tr(4), tr(5)], 0, { source: "shuffle" }));
    act(() => player().playList([tr(6), tr(7)], 0, { source: "favorites" }));
    await new Promise((r) => setTimeout(r, 20));
    expect(radio).not.toHaveBeenCalled();
    expect(random).not.toHaveBeenCalled();
  });

  test("repeat all: next() at the last track wraps too", () => {
    storeModes({ shuffle: false, repeat: "all", original: null });
    const { player } = setup();
    act(() => player().playList([tr(1), tr(2)], 1));
    act(() => player().next());
    expect(player().current?.id).toBe(1);
    expect(player().queue.index).toBe(0);
  });

  test("repeat all with shuffle: each new pass is a fresh permutation from index 0", () => {
    storeModes({ shuffle: true, repeat: "all", original: null });
    vi.spyOn(Math, "random").mockReturnValue(0);
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3), tr(4)], 3));
    act(() => audio.fire("ended"));
    expect(player().queue.index).toBe(0);
    expect([...ids(player())].sort()).toEqual([1, 2, 3, 4]);
    expect(ids(player())).not.toEqual([1, 2, 3, 4]);
    expect(audio.src).toContain(`/tracks/${ids(player())[0]}/`);
    expect(player().queue.source).toBe("list");
  });

  test("shuffle on mid-queue keeps the current track playing and permutes the rest; off restores it", () => {
    vi.spyOn(Math, "random").mockReturnValue(0);
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3), tr(4), tr(5), tr(6)], 1));
    audio.currentTime = 42;
    const src = audio.src;
    act(() => player().setShuffle(true));
    expect(player().modes.shuffle).toBe(true);
    expect(audio.src).toBe(src);
    expect(audio.currentTime).toBe(42);
    expect(audio.load).not.toHaveBeenCalled();
    expect(audio.play).toHaveBeenCalledTimes(1);
    expect(player().queue.index).toBe(1);
    expect(ids(player()).slice(0, 2)).toEqual([1, 2]);
    expect([...ids(player()).slice(2)].sort()).toEqual([3, 4, 5, 6]);
    expect(ids(player())).not.toEqual([1, 2, 3, 4, 5, 6]);
    act(() => player().setShuffle(false));
    expect(player().modes.shuffle).toBe(false);
    expect(ids(player())).toEqual([1, 2, 3, 4, 5, 6]);
    expect(audio.src).toBe(src);
    expect(audio.currentTime).toBe(42);
  });

  test("with shuffle on, a newly played list is shuffled after the chosen track", () => {
    storeModes({ shuffle: true, repeat: "off", original: null });
    vi.spyOn(Math, "random").mockReturnValue(0);
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3), tr(4), tr(5)], 1));
    expect(audio.src).toContain("/tracks/2/");
    expect(ids(player()).slice(0, 2)).toEqual([1, 2]);
    expect(ids(player())).not.toEqual([1, 2, 3, 4, 5]);
    act(() => player().setShuffle(false));
    expect(ids(player())).toEqual([1, 2, 3, 4, 5]);
  });

  test("cycleRepeat goes off → all → one → off", () => {
    const { player } = setup();
    expect(player().modes).toEqual({ shuffle: false, repeat: "off" });
    expect(player().modesAvailable).toBe(true);
    act(() => player().cycleRepeat());
    expect(player().modes.repeat).toBe("all");
    act(() => player().cycleRepeat());
    expect(player().modes.repeat).toBe("one");
    act(() => player().cycleRepeat());
    expect(player().modes.repeat).toBe("off");
  });

  test("modes persist per user and are read back on mount", () => {
    mockFetch({
      "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] } }),
      "PUT /api/v1/queue": () => ({ status: 200, body: {} }),
      "GET /api/v1/radio/next": () => ({ body: [] }),
    });
    let p!: Player;
    function Probe() {
      p = usePlayer();
      return null;
    }
    const mount = (userId: number) =>
      render(<PlayerProvider audio={new FakeAudio() as unknown as HTMLAudioElement} userId={userId}><Probe /></PlayerProvider>);
    const first = mount(1);
    act(() => p.setShuffle(true));
    act(() => p.cycleRepeat());
    expect(JSON.parse(localStorage.getItem("lark.modes.1")!)).toMatchObject({ shuffle: true, repeat: "all" });
    first.unmount();
    const again = mount(1);
    expect(p.modes).toEqual({ shuffle: true, repeat: "all" });
    again.unmount();
    mount(2);
    expect(p.modes).toEqual({ shuffle: false, repeat: "off" });
  });
});

// Queue editing from the queue sheet and the track menu: the playing track is
// never reloaded or restarted.
describe("queue editing", () => {
  const ids = (p: Player) => p.queue.tracks.map((t) => t.id);
  afterEach(() => vi.restoreAllMocks());

  test("addToQueue, move and removeAt leave the playing track alone", async () => {
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3), tr(4)], 1));
    const srcSet = vi.spyOn(audio, "src", "set");
    act(() => player().enqueueNext(tr(9)));
    act(() => player().addToQueue(tr(7)));
    expect(ids(player())).toEqual([1, 2, 9, 7, 3, 4]);
    act(() => player().move(5, 0));
    expect(ids(player())).toEqual([4, 1, 2, 9, 7, 3]);
    act(() => player().removeAt(0));
    act(() => player().removeAt(1)); // the current one: ignored
    expect(ids(player())).toEqual([1, 2, 9, 7, 3]);
    expect(player().queue.index).toBe(1);
    await act(async () => {});
    expect(srcSet).not.toHaveBeenCalled();
    expect(audio.play).toHaveBeenCalledTimes(1);
    expect(player().current?.id).toBe(2);
  });

  test("moving the current track keeps it playing, then plays what follows it", async () => {
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3), tr(4)], 0));
    const srcSet = vi.spyOn(audio, "src", "set");
    act(() => player().move(0, 2));
    expect(ids(player())).toEqual([2, 3, 1, 4]);
    expect(player().queue.index).toBe(2);
    await act(async () => {});
    expect(srcSet).not.toHaveBeenCalled();
    act(() => audio.fire("ended"));
    await waitFor(() => expect(player().current?.id).toBe(4));
  });

  test("shuffle on: a moved track keeps where it was dropped after shuffle off", () => {
    const { player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3), tr(4), tr(5)], 0));
    vi.spyOn(Math, "random").mockReturnValue(0);
    act(() => player().setShuffle(true));
    expect(ids(player())).toEqual([1, 3, 4, 5, 2]);
    act(() => player().move(3, 1)); // 5 dragged up, in front of 3
    expect(ids(player())).toEqual([1, 5, 3, 4, 2]);
    act(() => player().setShuffle(false));
    expect(ids(player())).toEqual([1, 2, 5, 3, 4]); // still just before 3
  });

  const serverQueue = (trackIds: number[], index: number) => ({
    "GET /api/v1/queue": () => ({
      body: { queue: { track_ids: trackIds, current_index: index, position_ms: 0, version: 1, updated_by: "", updated_at: 0 }, tracks: trackIds.map((id) => tr(id)) },
    }),
  });

  test("a reload keeps the tracks queued before it ahead of a new add", async () => {
    localStorage.setItem("lark.upNext", JSON.stringify({ ids: [1, 7, 2, 3], index: 0, upNext: 1 }));
    const { player } = setup(serverQueue([1, 7, 2, 3], 0));
    await waitFor(() => expect(player().current?.id).toBe(1));
    act(() => player().addToQueue(tr(9)));
    expect(ids(player())).toEqual([1, 7, 9, 2, 3]);
  });

  test("a stored block for another queue (or index) is not applied", async () => {
    localStorage.setItem("lark.upNext", JSON.stringify({ ids: [1, 7, 2, 3], index: 1, upNext: 1 }));
    const { player } = setup(serverQueue([1, 7, 2, 3], 0));
    await waitFor(() => expect(player().current?.id).toBe(1));
    act(() => player().addToQueue(tr(9)));
    expect(ids(player())).toEqual([1, 9, 7, 2, 3]);
  });

  test("the queued block is saved per user as the queue changes", async () => {
    const { player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    act(() => player().addToQueue(tr(7)));
    await waitFor(() => expect(JSON.parse(localStorage.getItem("lark.upNext") ?? "null")).toEqual({ ids: [1, 7, 2, 3], index: 0, upNext: 1 }));
    act(() => player().next());
    await waitFor(() => expect(localStorage.getItem("lark.upNext")).toBeNull());
  });

  test("shuffle on: a track added or removed keeps where the user put it after shuffle off", () => {
    const { player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3), tr(4), tr(5)], 0));
    act(() => player().setShuffle(true));
    // 3 was among the shuffled ones; queued now, it is the user's, not the shuffle's.
    act(() => player().addToQueue(tr(3)));
    const at = player().queue.tracks.findIndex((t) => t.id === 4);
    act(() => player().removeAt(at));
    act(() => player().addToQueue(tr(4))); // removed, then queued again
    expect(ids(player()).slice(0, 3)).toEqual([1, 3, 4]);
    act(() => player().setShuffle(false));
    expect(ids(player())).toEqual([1, 3, 4, 2, 5]);
  });
});
