// The sleep timer: a 10-second fade before it pauses, on whichever player is
// sounding (music or an episode), and "end of this track".
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { MemoryRouter } from "react-router";
import type { Episode, Track } from "../api/types";
import { EpisodeMini } from "../channels/EpisodeMini";
import { EpisodesProvider, useEpisodes, type EpisodesPlayer } from "../channels/EpisodesProvider";
import type { NativeState } from "../native/bridge";
import { installFakeNative, stateEvent, type FakeNative } from "../native/fakeNative";
import { episodeItem, trackItem } from "../native/items";
import { FakeAudio, mockFetch } from "../test/setup";
import { renderWithApp } from "../test/render";
import { MiniPlayer } from "./MiniPlayer";
import { PlayerProvider, usePlayer, type Player } from "./PlayerProvider";
import { resetSessionOwner } from "./sessionOwner";
import { SleepTimerMenu } from "./SleepTimerMenu";
import { SleepTimerProvider } from "./SleepTimerProvider";

const tr = (id: number, extra: Partial<Track> = {}) => ({ id, title: `歌${id}`, artist: "a", album: "b", duration_ms: 200_000, codec: "flac", bitrate: 900, lossless: true, ...extra }) as Track;
const ep = (n: number): Episode => ({
  video_id: `episode000${n}`, channel_id: "UC0e5c4U67Vm6sAVK0vxN3Uw", channel_title: "频道",
  title: `第${n}集`, published_at: 1_790_000_000 + n, duration_s: 1800, kind: "video",
  thumbnail: `/api/v1/episodes/episode000${n}/thumbnail`,
  audio: { status: "done", progress: 100, bytes: 1, error: "" }, video: null, position_s: 0, played: false, kept: false,
});

const MIN = 60_000;

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  resetSessionOwner();
  localStorage.clear();
});

async function setup() {
  mockFetch({
    "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] } }),
    "PUT /api/v1/queue": () => ({ body: {} }),
    "GET /api/v1/radio/next": () => ({ body: [] }),
    "POST /api/v1/events/play": () => ({ body: { accepted: 0 } }),
    "PUT /api/v1/episodes/episode0001/progress": () => ({ status: 204 }),
    "PUT /api/v1/episodes/episode0002/progress": () => ({ status: 204 }),
  });
  const music = new FakeAudio();
  const episode = new FakeAudio();
  let p!: Player;
  let e!: EpisodesPlayer;
  function Probe() {
    p = usePlayer();
    e = useEpisodes();
    return null;
  }
  render(
    <MemoryRouter>
      <PlayerProvider audio={music as unknown as HTMLAudioElement}>
        <EpisodesProvider audio={episode as unknown as HTMLAudioElement}>
          <SleepTimerProvider>
            <Probe />
            <SleepTimerMenu />
          </SleepTimerProvider>
        </EpisodesProvider>
      </PlayerProvider>
    </MemoryRouter>,
  );
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });
  return { music, episode, player: () => p, eps: () => e };
}

function choose(name: string) {
  fireEvent.click(screen.getByRole("button", { name: "更多" }));
  fireEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name }));
}
const chip = () => screen.queryByRole("button", { name: /^睡眠定时，剩余/ });
const advance = (ms: number) => act(() => vi.advanceTimersByTime(ms));
function at(a: FakeAudio, seconds: number) {
  a.currentTime = seconds;
  act(() => a.fire("timeupdate"));
}
// The element's volume each time pause() is called on it.
function volumeAtPause(a: FakeAudio) {
  const seen: number[] = [];
  a.pause.mockImplementation(() => {
    seen.push(a.volume);
    a.paused = true;
    a.fire("pause");
  });
  return seen;
}

test("15 minutes: the chip counts down, the last 10 s fade out, then both players pause at full volume again", async () => {
  const { music, episode, player } = await setup();
  act(() => player().playList([tr(1)], 0));
  expect(player().playing).toBe(true);
  const musicAtPause = volumeAtPause(music);
  volumeAtPause(episode);

  choose("15 分钟");
  expect(chip()).toHaveTextContent("🌙 15:00");
  expect(chip()).toHaveAccessibleName("睡眠定时，剩余 15:00");

  advance(14 * MIN);
  expect(chip()).toHaveTextContent("1:00");
  advance(50_000); // 14:50: the fade begins
  expect(music.volume).toBe(1);
  const levels: number[] = [];
  for (let i = 0; i < 39; i++) {
    advance(250);
    levels.push(music.volume);
  }
  expect(levels[0]).toBeLessThan(1);
  for (let i = 1; i < levels.length; i++) expect(levels[i]).toBeLessThan(levels[i - 1]);
  expect(levels[19]).toBeCloseTo(0.5, 5);
  expect(music.paused).toBe(false);
  expect(player().playing).toBe(true);

  advance(250); // 15:00
  expect(music.paused).toBe(true);
  expect(player().playing).toBe(false);
  expect(musicAtPause).toEqual([0]); // paused silent…
  expect(music.volume).toBe(1); // …and the volume is back for the next play
  expect(episode.volume).toBe(1);
  expect(episode.pause).toHaveBeenCalled();
  expect(chip()).toBeNull();
});

test("the fade multiplies the track's loudness gain", async () => {
  const { music, player } = await setup();
  act(() => player().playList([tr(1, { gain_db: -6 })], 0));
  expect(music.volume).toBeCloseTo(0.501, 3);
  choose("15 分钟");
  advance(15 * MIN - 5000); // halfway through the fade
  expect(music.volume).toBeCloseTo(0.501 * 0.5, 3);
  advance(5000);
  expect(music.volume).toBeCloseTo(0.501, 3);
});

test("turning the timer off during the fade restores the volume at once and keeps playing", async () => {
  const { music, player } = await setup();
  act(() => player().playList([tr(1)], 0));
  choose("15 分钟");
  advance(15 * MIN - 5000);
  expect(music.volume).toBeLessThan(1);
  fireEvent.click(chip()!); // the chip opens the same menu
  fireEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: "关闭定时" }));
  expect(music.volume).toBe(1);
  expect(chip()).toBeNull();
  advance(MIN);
  expect(music.paused).toBe(false);
  expect(player().playing).toBe(true);
  expect(music.volume).toBe(1);
});

test("changing 30 → 15 minutes keeps one timer, with the new deadline", async () => {
  const { music, player } = await setup();
  act(() => player().playList([tr(1)], 0));
  choose("30 分钟");
  expect(chip()).toHaveTextContent("30:00");
  advance(MIN);
  choose("15 分钟");
  expect(chip()).toHaveTextContent("15:00");
  expect(screen.getAllByRole("button", { name: /^睡眠定时/ })).toHaveLength(1);
  advance(15 * MIN);
  expect(music.paused).toBe(true);
  expect(music.pause).toHaveBeenCalledTimes(1);
  expect(chip()).toBeNull();
  // The old 30-minute deadline does nothing.
  act(() => player().play());
  advance(20 * MIN);
  expect(music.pause).toHaveBeenCalledTimes(1);
  expect(music.paused).toBe(false);
});

test("off is only offered while a timer is set", async () => {
  await setup();
  fireEvent.click(screen.getByRole("button", { name: "更多" }));
  const menu = screen.getByRole("menu");
  expect(within(menu).getAllByRole("menuitem").map((b) => b.textContent)).toEqual(["15 分钟", "30 分钟", "45 分钟", "60 分钟", "播完这首"]);
  fireEvent.click(within(menu).getByRole("menuitem", { name: "45 分钟" }));
  expect(screen.queryByRole("menu")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "更多" }));
  expect(within(screen.getByRole("menu")).getByRole("menuitem", { name: "关闭定时" })).toBeInTheDocument();
});

test("end of this track with 25 s left: no fade until 15 s on, then the next track is loaded paused", async () => {
  const { music, player } = await setup();
  act(() => player().playList([tr(1), tr(2)], 0));
  at(music, 175);
  choose("播完这首");
  expect(chip()).toHaveTextContent("0:25");
  at(music, 185);
  expect(music.volume).toBe(1);
  at(music, 190); // 15 s on: 10 s left
  expect(music.volume).toBe(1);
  at(music, 195);
  expect(music.volume).toBeCloseTo(0.5, 5);
  at(music, 199.75);
  expect(music.volume).toBeCloseTo(0.025, 5);
  const plays = music.play.mock.calls.length;
  at(music, 200);
  act(() => music.fire("ended"));
  expect(player().current?.id).toBe(2);
  expect(player().queue.index).toBe(1);
  expect(music.src).toContain("/tracks/2/");
  expect(music.paused).toBe(true);
  expect(player().playing).toBe(false);
  expect(music.play.mock.calls.length).toBe(plays);
  expect(music.volume).toBe(1);
  expect(chip()).toBeNull();
  // Disarmed: the track after that plays on as usual.
  act(() => player().play());
  act(() => music.fire("ended"));
  expect(music.paused).toBe(false);
});

test("end of this track: a browser that pauses the element just before \"ended\" still stops there", async () => {
  const { music, player } = await setup();
  act(() => player().playList([tr(1), tr(2)], 0));
  choose("播完这首");
  at(music, 199.8);
  at(music, 200);
  music.paused = true;
  act(() => music.fire("pause")); // a separate render before "ended"
  const plays = music.play.mock.calls.length;
  act(() => music.fire("ended"));
  expect(player().current?.id).toBe(2);
  expect(music.play.mock.calls.length).toBe(plays);
  expect(player().playing).toBe(false);
  expect(chip()).toBeNull();
  expect(music.volume).toBe(1);
});

test("end of this track wins over repeat one: the track is not looped", async () => {
  const { music, player } = await setup();
  act(() => player().playList([tr(1), tr(2)], 0));
  act(() => player().cycleRepeat());
  act(() => player().cycleRepeat());
  expect(player().modes.repeat).toBe("one");
  choose("播完这首");
  const plays = music.play.mock.calls.length;
  at(music, 200);
  act(() => music.fire("ended"));
  expect(music.play.mock.calls.length).toBe(plays);
  expect(music.paused).toBe(true);
  expect(chip()).toBeNull();
});

test("turning off end of this track lets the next track play", async () => {
  const { music, player } = await setup();
  act(() => player().playList([tr(1), tr(2)], 0));
  choose("播完这首");
  at(music, 195);
  expect(music.volume).toBeCloseTo(0.5, 5);
  fireEvent.click(chip()!);
  fireEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: "关闭定时" }));
  expect(music.volume).toBe(1);
  at(music, 200);
  act(() => music.fire("ended"));
  expect(player().current?.id).toBe(2);
  expect(music.paused).toBe(false);
});

test("skipping to another track keeps end of this track armed, for the new one", async () => {
  const { music, player } = await setup();
  act(() => player().playList([tr(1), tr(2), tr(3)], 0));
  choose("播完这首");
  at(music, 50);
  act(() => player().next());
  expect(music.paused).toBe(false);
  expect(chip()).not.toBeNull();
  at(music, 195);
  expect(music.volume).toBeCloseTo(0.5, 5);
  at(music, 200);
  act(() => music.fire("ended"));
  expect(player().current?.id).toBe(3);
  expect(music.paused).toBe(true);
  expect(chip()).toBeNull();
});

test("while an episode plays, the fade and the pause apply to the episode player", async () => {
  const { music, episode, eps } = await setup();
  act(() => eps().play([ep(1), ep(2)], 0));
  expect(eps().playing).toBe(true);
  const epAtPause = volumeAtPause(episode);
  choose("15 分钟");
  advance(15 * MIN - 5000);
  expect(episode.volume).toBeCloseTo(0.5, 5);
  advance(5000);
  expect(episode.paused).toBe(true);
  expect(eps().playing).toBe(false);
  expect(epAtPause).toEqual([0]);
  expect(episode.volume).toBe(1);
  expect(music.volume).toBe(1);
  expect(chip()).toBeNull();
});

test("end of this episode: it fades over its last 10 s and the next episode does not start", async () => {
  const { episode, eps } = await setup();
  act(() => eps().play([ep(1), ep(2)], 0));
  at(episode, 1700);
  choose("播完这首");
  expect(chip()).toHaveTextContent("1:40");
  at(episode, 1795);
  expect(episode.volume).toBeCloseTo(0.5, 5);
  const plays = episode.play.mock.calls.length;
  at(episode, 1800); // a real element reports its end position…
  episode.paused = true; // …and is paused there
  act(() => episode.fire("pause"));
  act(() => episode.fire("ended"));
  expect(episode.play.mock.calls.length).toBe(plays);
  expect(eps().index).toBe(0);
  expect(eps().playing).toBe(false);
  expect(episode.volume).toBe(1);
  expect(chip()).toBeNull();
});

test.each([
  ["music", "歌1"],
  ["episodes", "第1集"],
])("%s Now Playing has the ⋯ menu and the chip", async (kind) => {
  vi.useRealTimers();
  const user = userEvent.setup();
  let p!: Player;
  let e!: EpisodesPlayer;
  function Grab() {
    p = usePlayer();
    e = useEpisodes();
    return null;
  }
  renderWithApp(<><Grab /><MiniPlayer /><EpisodeMini /></>);
  if (kind === "music") {
    act(() => p.playList([tr(1)], 0));
    await user.click(await screen.findByText("歌1"));
  } else {
    act(() => e.play([ep(1)], 0));
    await user.click(await screen.findByRole("button", { name: /第1集/ }));
  }
  const dialog = await screen.findByRole("dialog");
  await user.click(within(dialog).getByRole("button", { name: "更多" }));
  await user.click(within(dialog).getByRole("menuitem", { name: "30 分钟" }));
  expect(within(dialog).getByRole("button", { name: "睡眠定时，剩余 30:00" })).toBeInTheDocument();
});

test("in an iPhone app without the native timer (its state has no sleepRemainingMs) there is no sleep timer", async () => {
  vi.useRealTimers();
  const n = installFakeNative();
  try {
    renderWithApp(<MiniPlayer />);
    n.emit({ type: "queue", kind: "track", items: [trackItem(tr(1))], index: 0, source: "list" });
    n.emit(stateEvent({ kind: "track", itemId: "1", sleepRemainingMs: undefined }));
    await userEvent.click(await screen.findByText("歌1"));
    const dialog = await screen.findByRole("dialog", { name: "正在播放" });
    expect(within(dialog).getByRole("button", { name: "下一首" })).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "更多" })).toBeNull();
    expect(n.sent("sleepTimer")).toEqual([]);
  } finally {
    n.uninstall();
  }
});

// In the app the engine owns the timer (it keeps time with the phone locked):
// the menu only asks, and the chip shows what native's state says is left.
describe("in the iPhone app", () => {
  let n!: FakeNative;
  beforeEach(() => {
    n = installFakeNative();
  });
  afterEach(() => n.uninstall());

  function renderNative() {
    render(
      <MemoryRouter>
        <PlayerProvider userId={1}>
          <EpisodesProvider userId={1}>
            <SleepTimerProvider>
              <SleepTimerMenu />
            </SleepTimerProvider>
          </EpisodesProvider>
        </PlayerProvider>
      </MemoryRouter>,
    );
    n.emit({ type: "queue", kind: "track", items: [trackItem(tr(1))], index: 0, source: "list" });
  }
  const state = (sleepRemainingMs: number | null, extra: Partial<NativeState> = {}) =>
    n.emit(stateEvent({ kind: "track", itemId: "1", playing: true, positionMs: 10_000, durationMs: 200_000, sleepRemainingMs, ...extra }));

  test("the menu appears once native's state carries sleepRemainingMs", () => {
    renderNative();
    expect(screen.queryByRole("button", { name: "更多" })).toBeNull();
    state(null);
    expect(screen.getByRole("button", { name: "更多" })).toBeInTheDocument();
    expect(chip()).toBeNull();
  });

  test("minutes: posts sleepTimer, the chip follows native's time left and counts down between states", () => {
    renderNative();
    state(null);
    choose("15 分钟");
    expect(n.sent("sleepTimer")).toEqual([{ type: "sleepTimer", minutes: 15 }]);
    expect(chip()).toHaveAccessibleName("睡眠定时，剩余 15:00");
    advance(3000);
    state(899_000);
    expect(chip()).toHaveAccessibleName("睡眠定时，剩余 14:59");
    // Paused, native sends nothing: the chip counts down by itself.
    state(898_000, { playing: false });
    advance(60_000);
    expect(chip()).toHaveAccessibleName("睡眠定时，剩余 13:58");
    // The web's own timer never runs in the app: no fade, no pause from here.
    advance(15 * MIN);
    expect(n.sent("pause")).toEqual([]);
    expect(chip()).toHaveAccessibleName("睡眠定时，剩余 0:00");
    // Native paused at the deadline and says so.
    state(null, { playing: false });
    expect(chip()).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "更多" }));
    expect(within(screen.getByRole("menu")).queryByRole("menuitem", { name: "关闭定时" })).toBeNull();
  });

  test("end of track: the chip shows what native says is left of the item", () => {
    renderNative();
    state(null);
    choose("播完这首");
    expect(n.sent("sleepTimer")).toEqual([{ type: "sleepTimer", endOfTrack: true }]);
    advance(500);
    state(42_000, { positionMs: 158_000 });
    expect(chip()).toHaveAccessibleName("睡眠定时，剩余 0:42");
    // Paused: the time left of a track stands still.
    state(40_000, { positionMs: 160_000, playing: false });
    advance(30_000);
    expect(chip()).toHaveAccessibleName("睡眠定时，剩余 0:40");
  });

  test("off posts cancel and the chip goes at once", () => {
    renderNative();
    state(null);
    choose("30 分钟");
    state(1_799_000);
    choose("关闭定时");
    expect(n.sent("sleepTimer")).toEqual([{ type: "sleepTimer", minutes: 30 }, { type: "sleepTimer", cancel: true }]);
    expect(chip()).toBeNull();
    state(null);
    expect(chip()).toBeNull();
  });

  test("a state sent before native took the choice does not undo it", () => {
    renderNative();
    state(null);
    choose("45 分钟");
    state(null);                       // already on its way when the tap was posted
    expect(chip()).toHaveAccessibleName("睡眠定时，剩余 45:00");
    state(2_699_000);
    expect(chip()).toHaveAccessibleName("睡眠定时，剩余 44:59");
    // Native's own end later on is believed.
    advance(3000);
    state(null);
    expect(chip()).toBeNull();
  });

  // A reload's first states: the inactive kind's comes first, and its time left
  // says nothing about the timer. Only the playing kind's state decides.
  test("a reload under end of this episode reads the episode's state, not music's", () => {
    renderNative();
    n.emit({ type: "queue", kind: "episode", items: [episodeItem(ep(1))], index: 0, source: "list" });
    state(100_000, { playing: false, positionMs: 10_000, durationMs: 200_000 });           // music, inactive
    n.emit(stateEvent({ kind: "episode", itemId: "episode0001", playing: true, positionMs: 1_700_000, durationMs: 1_800_000, sleepRemainingMs: 100_000 }));
    n.emit(stateEvent({ kind: "episode", itemId: "episode0001", playing: false, positionMs: 1_700_000, durationMs: 1_800_000, sleepRemainingMs: 100_000 }));
    expect(chip()).toHaveAccessibleName("睡眠定时，剩余 1:40");
    advance(30_000);
    expect(chip()).toHaveAccessibleName("睡眠定时，剩余 1:40");
  });

  test("a page reloaded while native's timer runs shows it", () => {
    renderNative();
    // What is left equals what is left of the item: end of track, which stands still while paused.
    state(190_000, { positionMs: 10_000, durationMs: 200_000, playing: false });
    expect(chip()).toHaveAccessibleName("睡眠定时，剩余 3:10");
    advance(30_000);
    expect(chip()).toHaveAccessibleName("睡眠定时，剩余 3:10");
    state(null, { playing: false });
    expect(chip()).toBeNull();
    // Anything else is a deadline, which runs on.
    state(600_000, { playing: false });
    expect(chip()).toHaveAccessibleName("睡眠定时，剩余 10:00");
    advance(30_000);
    expect(chip()).toHaveAccessibleName("睡眠定时，剩余 9:30");
  });
});
