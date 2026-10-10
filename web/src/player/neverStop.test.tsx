// Never stop: at a track change (and on stalls and failures) the player
// prefers tracks already on the phone, above all while the page is hidden
// (a locked iPhone suspends a silent page within ~10 s).
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { PlayerProvider, usePlayer, type Player } from "./PlayerProvider";
import { FakeAudio, mockFetch } from "../test/setup";
import type { Track } from "../api/types";
import { connectOffline, type LocalKind } from "../offline/bridge";

const tr = (id: number) => ({ id, title: `t${id}`, artist: "a", album: "b", duration_ms: 200000 }) as Track;

let hidden = false;
let online = true;
Object.defineProperty(document, "visibilityState", { configurable: true, get: () => (hidden ? "hidden" : "visible") });
Object.defineProperty(navigator, "onLine", { configurable: true, get: () => online });

const kinds = new Map<number, LocalKind>();
let favs: { track: Track; lastPlayedAt: number }[] = [];
const lookahead = vi.fn((_: Track[], _keep: number[]): number[] => []);
let disconnect: (() => void) | null = null;

beforeEach(() => {
  hidden = false;
  online = true;
  kinds.clear();
  favs = [];
  lookahead.mockReset();
  lookahead.mockImplementation(() => []);
  disconnect = connectOffline({
    played: () => {},
    playable: () => true,
    local: (id) => kinds.get(id) ?? null,
    favorites: () => favs,
    lookahead,
  });
});
afterEach(() => {
  disconnect?.();
  vi.useRealTimers();
});

function setup(routes: Parameters<typeof mockFetch>[0] = {}) {
  const f = mockFetch({
    "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] } }),
    "PUT /api/v1/queue": () => ({ body: {} }),
    "POST /api/v1/events/play": () => ({ body: { accepted: 1 } }),
    "GET /api/v1/radio/next": () => ({ body: [] }),
    "POST /api/v1/tracks/prepare": () => ({ status: 202, body: { queued: 0 } }),
    ...routes,
  });
  const audio = new FakeAudio();
  let p!: Player;
  function Probe() {
    p = usePlayer();
    return null;
  }
  render(<PlayerProvider audio={audio as unknown as HTMLAudioElement}><Probe /></PlayerProvider>);
  return { audio, f, player: () => p };
}

const ids = (p: Player) => p.queue.tracks.map((t) => t.id);

describe("at the end of a track", () => {
  test("a cached next track plays at once, from the cached (high) copy when it was a lookahead", () => {
    localStorage.setItem("lark.quality", "lossless");
    kinds.set(2, "lookahead");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    act(() => audio.fire("ended"));
    // Synchronously, inside the ended handler: no render round-trip.
    expect(audio.src).toContain("/tracks/2/stream?quality=high");
    expect(player().current?.id).toBe(2);
  });

  test("hidden, an uncached next track is passed over for the next cached one, and moved after it", () => {
    kinds.set(4, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3), tr(4), tr(5)], 0));
    hidden = true;
    let srcInHandler = "";
    audio.addEventListener("ended", () => (srcInHandler = audio.src));
    act(() => audio.fire("ended"));
    expect(srcInHandler).toContain("/tracks/4/");
    expect(ids(player())).toEqual([1, 4, 2, 3, 5]);
    expect(player().queue.index).toBe(1);
    expect(audio.play).toHaveBeenCalledTimes(2);
  });

  test("hidden with nothing cached in the queue, a cached favorite not played recently plays", () => {
    favs = [{ track: tr(10), lastPlayedAt: Date.now() - 60_000 }, { track: tr(11), lastPlayedAt: 0 }];
    kinds.set(10, "favorite");
    kinds.set(11, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    hidden = true;
    act(() => audio.fire("ended"));
    expect(audio.src).toContain("/tracks/11/");
    expect(ids(player())).toEqual([1, 11, 2, 3]);
  });

  test("visible, the next track plays normally even when it isn't cached", () => {
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    act(() => audio.fire("ended"));
    expect(audio.src).toContain("/tracks/2/");
    expect(ids(player())).toEqual([1, 2, 3]);
  });

  test("hidden at the very end of the queue, a cached favorite keeps it going", () => {
    favs = [{ track: tr(9), lastPlayedAt: 0 }];
    const { audio, player } = setup();
    act(() => player().playList([tr(1)], 0));
    hidden = true;
    act(() => audio.fire("ended"));
    expect(player().current?.id).toBe(9);
    expect(player().playing).toBe(true);
  });

  test("a next/previous-track press while hidden makes the same choice", () => {
    kinds.set(3, "recent");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    hidden = true;
    act(() => player().next());
    expect(audio.src).toContain("/tracks/3/");
    expect(ids(player())).toEqual([1, 3, 2]);
  });
});

describe("a stuck clock", () => {
  // "playing", the audio at the playhead downloaded, and currentTime frozen:
  // the media pipeline is stuck (WebKit's GStreamer backend), not the network.
  const stuck = (audio: FakeAudio, at = 0.0003) => {
    audio.currentTime = at;
    audio.bufferedRanges = [[0, 180]];
  };

  test("is reloaded where it stands after a few seconds, and plays on from there", () => {
    vi.useFakeTimers();
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2)], 0));
    act(() => audio.fire("playing"));
    stuck(audio, 1.5);
    const plays = audio.play.mock.calls.length;
    act(() => vi.advanceTimersByTime(2000));
    expect(audio.play.mock.calls.length).toBe(plays); // not yet
    act(() => vi.advanceTimersByTime(1500));
    expect(audio.play.mock.calls.length).toBe(plays + 1);
    expect(audio.src).toContain("/tracks/1/stream");
    expect(audio.currentTime).toBe(0); // a fresh load ...
    act(() => audio.fire("loadedmetadata"));
    expect(audio.currentTime).toBe(1.5); // ... back at the same place
    expect(player().current?.id).toBe(1);
  });

  test("is reloaded at most twice per load", () => {
    vi.useFakeTimers();
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2)], 0));
    act(() => audio.fire("playing"));
    const plays = audio.play.mock.calls.length;
    for (let i = 0; i < 4; i++) {
      stuck(audio);
      act(() => vi.advanceTimersByTime(3000));
    }
    expect(audio.play.mock.calls.length).toBe(plays + 2);
  });

  test("a moving clock, a pause, a seek, or a playhead with nothing downloaded is left alone", () => {
    vi.useFakeTimers();
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2)], 0));
    act(() => audio.fire("playing"));
    const plays = audio.play.mock.calls.length;
    stuck(audio, 1);
    for (let i = 0; i < 12; i++) {
      audio.currentTime += 0.5;
      act(() => vi.advanceTimersByTime(500));
    }
    audio.bufferedRanges = [[0, 3]]; // the network ran dry: the stall handlers' business
    audio.currentTime = 7;
    act(() => vi.advanceTimersByTime(5000));
    audio.bufferedRanges = [[0, 180]];
    audio.seeking = true;
    act(() => vi.advanceTimersByTime(5000));
    audio.seeking = false;
    act(() => player().pause());
    act(() => vi.advanceTimersByTime(5000));
    expect(audio.play.mock.calls.length).toBe(plays);
  });
});

describe("stalls", () => {
  // A playing network track that runs dry: no data ahead, still loading.
  const starve = (audio: FakeAudio) => {
    audio.readyState = 2;
    audio.networkState = 2;
  };

  test("hidden: a playing network track that runs dry switches at once to a cached one, and is requeued after it", () => {
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    act(() => audio.fire("playing"));
    hidden = true;
    starve(audio);
    act(() => audio.fire("waiting"));
    expect(audio.src).toContain("/tracks/3/");
    expect(ids(player())).toEqual([1, 3, 1, 2]);
    expect(player().queue.index).toBe(1);
  });

  test("hidden: the browser's own stalled event switches too", () => {
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    act(() => audio.fire("playing"));
    hidden = true;
    act(() => audio.fire("stalled"));
    expect(player().current?.id).toBe(3);
  });

  test("hidden: a waiting with data ahead, while seeking, or before the track ever started, is left alone", () => {
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    hidden = true;
    starve(audio);
    act(() => audio.fire("waiting")); // not started since its load
    expect(player().current?.id).toBe(1);
    act(() => audio.fire("playing"));
    audio.readyState = 3;
    act(() => audio.fire("waiting")); // data ahead
    expect(player().current?.id).toBe(1);
    starve(audio);
    audio.seeking = true;
    act(() => audio.fire("waiting"));
    expect(player().current?.id).toBe(1);
  });

  test("hidden: after a user action (car previous = restart, lock-screen seek, play) nothing switches until it plays again", () => {
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    act(() => audio.fire("playing"));
    hidden = true;
    audio.currentTime = 50;
    for (const action of [() => player().prev(), () => player().seek(20), () => (player().pause(), player().play())]) {
      act(action);
      starve(audio);
      act(() => audio.fire("stalled"));
      act(() => audio.fire("waiting"));
      expect(player().current?.id).toBe(1);
      act(() => audio.fire("playing"));
      audio.readyState = 4;
    }
    starve(audio);
    act(() => audio.fire("waiting"));
    expect(player().current?.id).toBe(3);
  });

  test("hidden: a play() on an already-playing element (a car connecting) is no user action", () => {
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    act(() => audio.fire("playing"));
    hidden = true;
    act(() => player().play());
    starve(audio);
    act(() => audio.fire("waiting"));
    expect(player().current?.id).toBe(3);
  });

  test("hidden: a seek into buffered audio stops counting once it has landed and plays on", () => {
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    act(() => audio.fire("playing"));
    hidden = true;
    act(() => player().seek(0)); // car "previous" mid-track: no "playing" follows
    act(() => audio.fire("seeked"));
    starve(audio);
    act(() => audio.fire("waiting"));
    expect(player().current?.id).toBe(3);
  });

  test("hidden: a seek stops counting once the position moves on (timeupdate)", () => {
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    act(() => audio.fire("playing"));
    hidden = true;
    act(() => player().seek(30));
    audio.currentTime = 31;
    act(() => audio.fire("timeupdate"));
    act(() => audio.fire("stalled"));
    expect(player().current?.id).toBe(3);
  });

  test("hidden: a user action is honored for 10 s at most — a pick that never starts falls back to a cached track", async () => {
    vi.useFakeTimers();
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 1));
    act(() => audio.fire("playing"));
    hidden = true;
    act(() => player().prev()); // onto track 1, uncached; it never starts
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(audio.src).toContain("/tracks/1/");
    act(() => audio.fire("stalled"));
    expect(player().current?.id).toBe(1);
    act(() => vi.advanceTimersByTime(10_500));
    act(() => audio.fire("stalled"));
    expect(player().current?.id).toBe(3);
  });

  test("a network retry the browser refuses to play asks for a tap", async () => {
    vi.useFakeTimers();
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2)], 0));
    audio.error = { code: 2 };
    act(() => audio.fire("error"));
    audio.play.mockImplementationOnce(() => Promise.reject(new DOMException("no gesture", "NotAllowedError")));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(player().needsTap).toBe(true);
    expect(player().playing).toBe(false);
  });

  test("hidden: previous onto an uncached track is honored", async () => {
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 1));
    act(() => audio.fire("playing"));
    hidden = true;
    act(() => player().prev());
    await waitFor(() => expect(audio.src).toContain("/tracks/1/"));
    act(() => audio.fire("stalled"));
    expect(player().current?.id).toBe(1);
  });

  test("hidden: a cached track that waits a moment is left alone", () => {
    kinds.set(1, "favorite");
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    hidden = true;
    act(() => audio.fire("waiting"));
    expect(player().current?.id).toBe(1);
  });

  test("visible: a seek on a slow link starts no stall timer", () => {
    vi.useFakeTimers();
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    act(() => audio.fire("playing"));
    act(() => player().seek(100));
    act(() => audio.fire("waiting"));
    act(() => vi.advanceTimersByTime(20_000));
    expect(player().current?.id).toBe(1);
  });

  test("visible: a stall of 6 s switches to a cached track, with a notice; playing again before that cancels it", () => {
    vi.useFakeTimers();
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    act(() => audio.fire("playing"));
    act(() => audio.fire("waiting"));
    act(() => vi.advanceTimersByTime(5000));
    act(() => audio.fire("playing"));
    act(() => vi.advanceTimersByTime(5000));
    expect(player().current?.id).toBe(1);
    act(() => audio.fire("stalled"));
    act(() => vi.advanceTimersByTime(5999));
    expect(player().current?.id).toBe(1);
    act(() => vi.advanceTimersByTime(1));
    expect(player().current?.id).toBe(3);
    expect(player().notice).toBe("网络不佳：改播已缓存的歌曲");
  });

  test("visible: a song the user just picked isn't switched away from while it starts", () => {
    vi.useFakeTimers();
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    act(() => audio.fire("waiting"));
    act(() => vi.advanceTimersByTime(20_000));
    expect(player().current?.id).toBe(1);
  });

  test("visible: pressing play on a paused track isn't switched away from while it resumes", () => {
    vi.useFakeTimers();
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    act(() => audio.fire("playing"));
    act(() => player().pause());
    act(() => player().play());
    act(() => audio.fire("waiting"));
    act(() => vi.advanceTimersByTime(20_000));
    expect(player().current?.id).toBe(1);
  });

  test("with nothing cached, a stall just waits (as before)", () => {
    vi.useFakeTimers();
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2)], 0));
    act(() => audio.fire("playing"));
    hidden = true;
    act(() => audio.fire("waiting"));
    act(() => vi.advanceTimersByTime(30_000));
    expect(player().current?.id).toBe(1);
  });
});

describe("failures", () => {
  test("a track that failed is remembered for the session and skipped when it comes round again", () => {
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(1), tr(3)], 0));
    audio.error = { code: 4 };
    act(() => audio.fire("error"));
    expect(player().current?.id).toBe(2);
    act(() => audio.fire("ended"));
    expect(player().current?.id).toBe(3);
  });

  test("several failures in a row fall back to a cached favorite instead of stopping", () => {
    favs = [{ track: tr(9), lastPlayedAt: 0 }];
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3), tr(4), tr(5)], 0));
    for (let i = 0; i < 3; i++) {
      audio.error = { code: 3 };
      act(() => audio.fire("error"));
    }
    expect(player().current?.id).toBe(9);
    expect(player().error).toBeNull();
  });

  test("the network retry is capped: three tries with backoff, then the next track", () => {
    vi.useFakeTimers();
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    for (const delay of [2000, 5000, 10_000]) {
      audio.error = { code: 2 };
      act(() => audio.fire("error"));
      expect(player().current?.id).toBe(1);
      act(() => vi.advanceTimersByTime(delay));
      expect(audio.errored).toBe(false); // retried
    }
    audio.error = { code: 2 };
    act(() => audio.fire("error"));
    expect(player().current?.id).toBe(2);
    expect(audio.src).toContain("/tracks/2/");
  });

  test("a network error while hidden switches at once to a cached track, requeueing the interrupted one", () => {
    kinds.set(3, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 0));
    hidden = true;
    audio.error = { code: 2 };
    act(() => audio.fire("error"));
    expect(player().current?.id).toBe(3);
    expect(ids(player())).toEqual([1, 3, 1, 2]);
  });

  test("a track that failed is tried again after 30 minutes", () => {
    vi.useFakeTimers();
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(1), tr(3)], 0));
    audio.error = { code: 4 };
    act(() => audio.fire("error"));
    act(() => audio.fire("playing"));
    vi.setSystemTime(Date.now() + 31 * 60_000);
    act(() => audio.fire("ended"));
    expect(player().current?.id).toBe(1);
  });

  test("failures are forgotten when the network comes back", () => {
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(1), tr(3)], 0));
    audio.error = { code: 4 };
    act(() => audio.fire("error"));
    act(() => window.dispatchEvent(new Event("online")));
    act(() => audio.fire("ended"));
    expect(player().current?.id).toBe(1);
  });

  test("failures are forgotten once something plays after a burst of them (a network-wide problem, not the files)", () => {
    kinds.set(4, "favorite");
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3), tr(4), tr(1)], 0));
    for (let i = 0; i < 3; i++) {
      audio.error = { code: 4 };
      act(() => audio.fire("error"));
    }
    expect(player().current?.id).toBe(4);
    act(() => audio.fire("playing"));
    act(() => audio.fire("ended"));
    expect(player().current?.id).toBe(1);
  });

  test("a play() the browser refuses (no gesture) asks for a tap instead of pretending to play", async () => {
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2)], 0));
    act(() => audio.fire("playing"));
    audio.play.mockImplementationOnce(() => Promise.reject(new DOMException("no gesture", "NotAllowedError")));
    act(() => audio.fire("ended"));
    await waitFor(() => expect(player().needsTap).toBe(true));
    expect(player().playing).toBe(false);
  });

  test("offline with nothing cached: retries, then waits for the network without spinning", () => {
    vi.useFakeTimers();
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2)], 0));
    online = false;
    for (const delay of [2000, 5000, 10_000]) {
      audio.error = { code: 2 };
      act(() => audio.fire("error"));
      act(() => vi.advanceTimersByTime(delay));
    }
    audio.error = { code: 2 };
    act(() => audio.fire("error"));
    act(() => vi.advanceTimersByTime(120_000));
    expect(audio.errored).toBe(true); // nothing retried meanwhile: no spinning
    expect(player().current?.id).toBe(1);
    online = true;
    act(() => window.dispatchEvent(new Event("online")));
    expect(audio.errored).toBe(false); // retried at once
  });

  test("online but every track's network fails: stops after a few tracks instead of skipping forever", () => {
    vi.useFakeTimers();
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3), tr(4), tr(5), tr(6)], 0));
    for (let n = 0; n < 20; n++) {
      audio.error = { code: 2 };
      act(() => audio.fire("error"));
      act(() => vi.advanceTimersByTime(10_000));
    }
    expect(player().playing).toBe(false);
    expect(player().error).toBeTruthy();
    expect(player().current!.id).toBeLessThan(6);
  });
});

describe("lookahead", () => {
  test("asks the offline cache for the next 3 tracks and the server to prepare them", async () => {
    localStorage.setItem("lark.quality", "lossless");
    const prepare = vi.fn(() => ({ status: 202, body: { queued: 3 } }));
    const { audio, player } = setup({ "POST /api/v1/tracks/prepare": prepare });
    act(() => player().playList([tr(1), tr(2), tr(3), tr(4), tr(5)], 0));
    act(() => audio.fire("playing"));
    await waitFor(() => expect(lookahead).toHaveBeenCalled());
    expect(lookahead.mock.calls.at(-1)![0].map((t) => t.id)).toEqual([2, 3, 4]);
    expect(lookahead.mock.calls.at(-1)![1]).toEqual([1]); // the playing track is kept (no previous yet)
    act(() => audio.fire("ended"));
    act(() => audio.fire("playing"));
    await waitFor(() => expect(lookahead.mock.calls.at(-1)![1]).toEqual([2, 1])); // playing, and the previous one
    await waitFor(() => expect(prepare).toHaveBeenCalled());
    // Prepared at the tier the lookahead fetches, whatever the chosen quality.
    expect(JSON.parse(((prepare.mock.calls[0] as unknown[])[0] as RequestInit).body as string)).toEqual({ ids: [2, 3, 4], quality: "high" });
  });

  test("a restored queue that isn't playing downloads nothing ahead", async () => {
    const restored = [tr(1), tr(2), tr(3)];
    const prepare = vi.fn(() => ({ status: 202, body: { queued: 2 } }));
    const { audio, player } = setup({
      "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [1, 2, 3], current_index: 0, position_ms: 0, version: 1, updated_by: "", updated_at: 0 }, tracks: restored } }),
      "POST /api/v1/tracks/prepare": prepare,
    });
    await waitFor(() => expect(player().current?.id).toBe(1));
    await waitFor(() => expect(prepare).toHaveBeenCalled()); // the server may get ready
    expect(lookahead).not.toHaveBeenCalled();
    // The first tap's muted play-and-pause (prime) is no playback either: a
    // browser reports its "play" and "pause" as separate tasks.
    act(() => audio.fire("play"));
    await new Promise((r) => setTimeout(r, 20));
    act(() => audio.fire("pause"));
    expect(lookahead).not.toHaveBeenCalled();
    act(() => player().play());
    act(() => audio.fire("playing"));
    await waitFor(() => expect(lookahead).toHaveBeenCalled());
  });

  test("without an offline cache, the next track is fetched whole once the current one has buffered, and played from memory", async () => {
    lookahead.mockImplementation(() => []);
    const create = vi.fn(() => "blob:next");
    const revoke = vi.fn();
    Object.assign(URL, { createObjectURL: create, revokeObjectURL: revoke });
    try {
      localStorage.setItem("lark.quality", "lossless");
      const stream = vi.fn(() => ({ body: "audio" }));
      const { audio, player } = setup({ "GET /api/v1/tracks/2/stream": stream });
      act(() => player().playList([tr(1), tr(2), tr(3)], 0));
      act(() => audio.fire("playing"));
      await new Promise((r) => setTimeout(r, 20));
      expect(stream).not.toHaveBeenCalled(); // still buffering the current track
      act(() => audio.fire("canplaythrough"));
      await waitFor(() => expect(create).toHaveBeenCalled());
      hidden = true;
      act(() => audio.fire("ended"));
      expect(audio.src).toBe("blob:next");
      expect(player().current?.id).toBe(2);
      expect(String((stream.mock.calls[0] as unknown[])[1])).toContain("quality=high");
      cleanup(); // the player releases its blob
      expect(revoke).toHaveBeenCalledWith("blob:next");
    } finally {
      Object.assign(URL, { createObjectURL: undefined, revokeObjectURL: undefined });
    }
  });

  test("without an offline cache, a next track over 30 MB isn't fetched into memory", async () => {
    Object.assign(URL, { createObjectURL: vi.fn(() => "blob:x"), revokeObjectURL: vi.fn() });
    try {
      const stream = vi.fn(() => ({ body: "audio" }));
      const long = { ...tr(2), duration_ms: 20 * 60_000, codec: "flac", bitrate: 900 } as Track; // ~40 MB at high
      const { audio, player } = setup({ "GET /api/v1/tracks/2/stream": stream });
      act(() => player().playList([tr(1), long, tr(3)], 0));
      act(() => audio.fire("playing"));
      act(() => audio.fire("canplaythrough"));
      await new Promise((r) => setTimeout(r, 30));
      expect(stream).not.toHaveBeenCalled();
      cleanup();
    } finally {
      Object.assign(URL, { createObjectURL: undefined, revokeObjectURL: undefined });
    }
  });
});

// Shuffle and repeat stored as off: the end of the queue is exactly as before
// (a cached favorite while hidden, and the radio refill).
describe("with play modes stored as off", () => {
  beforeEach(() => localStorage.setItem("lark.modes", JSON.stringify({ shuffle: false, repeat: "off", original: null })));

  test("hidden at the very end of the queue, a cached favorite still keeps it going", () => {
    favs = [{ track: tr(9), lastPlayedAt: 0 }];
    const { audio, player } = setup();
    expect(player().modes).toEqual({ shuffle: false, repeat: "off" });
    act(() => player().playList([tr(1)], 0));
    hidden = true;
    act(() => audio.fire("ended"));
    expect(player().current?.id).toBe(9);
    expect(ids(player())).toEqual([1, 9]);
    expect(player().playing).toBe(true);
  });

  test("near the end of the queue, the radio refill still runs", async () => {
    const radio = vi.fn(() => ({ body: [tr(10), tr(11)] }));
    const { player } = setup({ "GET /api/v1/radio/next": radio });
    act(() => player().playList([tr(1), tr(2)], 0));
    await waitFor(() => expect(ids(player())).toEqual([1, 2, 10, 11]));
    expect(radio).toHaveBeenCalledTimes(1);
  });

  test("visible at the end of an unrefillable queue, it stops as before", () => {
    const { audio, player } = setup();
    act(() => player().playList([tr(1)], 0));
    act(() => audio.fire("ended"));
    expect(player().current?.id).toBe(1);
    expect(player().playing).toBe(false);
  });
});

// Repeat all loops the queue as it is: a failing last track starts it over
// rather than adding a favorite to the loop.
describe("repeat all and failures", () => {
  beforeEach(() => {
    localStorage.setItem("lark.modes", JSON.stringify({ shuffle: false, repeat: "all", original: null }));
    favs = [{ track: tr(9), lastPlayedAt: 0 }];
    kinds.set(9, "favorite");
  });

  test("a last track that won't play wraps to the first", () => {
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2)], 1));
    act(() => audio.fire("error"));
    expect(player().current?.id).toBe(1);
    expect(ids(player())).toEqual([1, 2]);
  });

  // (With a cached favorite on the phone the capped retry plays it first, to
  // keep sound going, as before; here there is none.)
  test("a last track whose network keeps failing wraps to the first, rather than stopping", () => {
    favs = [];
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2)], 1));
    for (let i = 0; i < 4; i++) {
      audio.error = { code: 2 };
      act(() => audio.fire("error"));
    }
    expect(player().current?.id).toBe(1);
    expect(ids(player())).toEqual([1, 2]);
  });
});

// Repeat all keeps the cached substitute (offline it must still play
// something local) but only for that one play: the loop stays the queue's own.
describe("repeat all and a cached substitute", () => {
  beforeEach(() => {
    localStorage.setItem("lark.modes", JSON.stringify({ shuffle: false, repeat: "all", original: null }));
    favs = [{ track: tr(9), lastPlayedAt: 0 }];
    kinds.set(9, "favorite");
  });
  const failNetwork = (audio: FakeAudio) => {
    for (let i = 0; i < 4; i++) {
      audio.error = { code: 2 };
      act(() => audio.fire("error"));
    }
  };

  test("a network failure on the last track plays the favorite once, then the loop is the queue again", () => {
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2)], 1));
    failNetwork(audio);
    expect(player().current?.id).toBe(9);
    expect(ids(player())).toEqual([1, 2, 9, 2]);
    act(() => audio.fire("ended"));
    expect(ids(player())).toEqual([1, 2]);
    expect(player().current?.id).toBe(2); // the interrupted track, tried again
    act(() => audio.fire("ended"));
    expect(player().current?.id).toBe(1);
    expect(ids(player())).toEqual([1, 2]);
  });

  test("skipping the substitute drops it too", () => {
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 1));
    failNetwork(audio);
    expect(player().current?.id).toBe(9);
    expect(ids(player())).toEqual([1, 2, 9, 2, 3]);
    act(() => player().next());
    expect(ids(player())).toEqual([1, 2, 3]);
    expect(player().current?.id).toBe(2);
  });

  test("a substitute jumped past never joins the next pass", () => {
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 1));
    failNetwork(audio);
    act(() => player().jump(4)); // past it, to the last track
    expect(player().current?.id).toBe(3);
    act(() => audio.fire("ended"));
    expect(player().current?.id).toBe(1);
    expect(ids(player())).toEqual([1, 2, 3]);
  });
});

describe("repeat all, a cached substitute and a queue edit", () => {
  beforeEach(() => {
    localStorage.setItem("lark.modes", JSON.stringify({ shuffle: false, repeat: "all", original: null }));
    favs = [{ track: tr(9), lastPlayedAt: 0 }];
    kinds.set(9, "favorite");
  });

  test("removing the interrupted track's retry keeps it in the loop", () => {
    const { audio, player } = setup();
    act(() => player().playList([tr(1), tr(2), tr(3)], 1));
    for (let i = 0; i < 4; i++) {
      audio.error = { code: 2 };
      act(() => audio.fire("error"));
    }
    expect(ids(player())).toEqual([1, 2, 9, 2, 3]);
    act(() => player().removeAt(3)); // the retry, from the queue sheet
    expect(ids(player())).toEqual([1, 2, 9, 3]);
    act(() => audio.fire("ended"));
    expect(ids(player())).toEqual([1, 2, 3]); // the substitute leaves; 2 stays in the loop
    expect(player().current?.id).toBe(3);
  });
});
