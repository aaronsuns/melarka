import { act, cleanup, fireEvent, render } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { claimSession, ownsSession, resetSessionOwner } from "./sessionOwner";
import { useSessionVideo, type SessionVideo, type SessionVideoOptions } from "./useSessionVideo";

// The Media Session stub: MediaMetadata keeps its fields, handlers are recorded.
let ms: { setActionHandler: ReturnType<typeof vi.fn>; metadata: unknown; playbackState: string };

beforeEach(() => {
  ms = { setActionHandler: vi.fn(), metadata: null, playbackState: "none" };
  Object.defineProperty(navigator, "mediaSession", { value: ms, configurable: true, writable: true });
  vi.stubGlobal("MediaMetadata", class { constructor(init: MediaMetadataInit) { Object.assign(this, init); } });
});

afterEach(() => {
  cleanup();
  resetSessionOwner();
  delete (navigator as unknown as { mediaSession?: unknown }).mediaSession;
  delete (document as unknown as { visibilityState?: string }).visibilityState;
});

const handler = (a: string) => [...ms.setActionHandler.mock.calls].reverse().find(([n]) => n === a)?.[1] as (() => void) | null | undefined;

function renderVideoHarness(opts: Partial<SessionVideoOptions> = {}) {
  const result: { current: SessionVideo } = { current: null as unknown as SessionVideo };
  function Harness() {
    const ref = useRef<HTMLVideoElement>(null);
    const sv = useSessionVideo(ref, "/v.mp4", { metadata: () => ({ title: "T", artist: "C" }), ...opts });
    result.current = sv;
    return <video ref={ref} src="/v.mp4" {...sv.handlers} />;
  }
  render(<Harness />);
  const video = document.querySelector("video")!;
  // jsdom: the element's paused flag follows our play/pause events.
  let paused = true;
  Object.defineProperty(video, "paused", { get: () => paused, configurable: true });
  vi.mocked(video.pause).mockClear();
  return {
    video,
    result,
    setPaused: (p: boolean) => (paused = p),
  };
}

function fire(video: HTMLVideoElement, ev: "play" | "pause" | "ended") {
  if (ev === "play") fireEvent.play(video);
  else if (ev === "pause") fireEvent.pause(video);
  else fireEvent.ended(video);
}

function setVisibility(v: "hidden" | "visible") {
  Object.defineProperty(document, "visibilityState", { value: v, configurable: true });
  fireEvent(document, new Event("visibilitychange"));
}

test("playing claims the video session and sets the lock screen; another owner pauses it", () => {
  const { video, result, setPaused } = renderVideoHarness({ metadata: () => ({ title: "T", artist: "C" }) });
  setPaused(false);
  act(() => fire(video, "play"));
  expect(ownsSession("video")).toBe(true);
  expect(result.current.playing()).toBe(true);
  expect((navigator.mediaSession.metadata as unknown as { title: string })?.title).toBe("T");
  expect(handler("pause")).toBeTypeOf("function");
  expect(handler("nexttrack")).toBeNull();
  vi.mocked(video.pause).mockImplementation(() => {
    setPaused(true);
    fireEvent.pause(video);
  });
  act(() => claimSession("music"));
  expect(video.pause).toHaveBeenCalled();
  expect(result.current.playing()).toBe(false);
});

test("no metadata yet: it claims the session but leaves the lock screen alone", () => {
  const { video } = renderVideoHarness({ metadata: () => null });
  act(() => fire(video, "play"));
  expect(ownsSession("video")).toBe(true);
  expect(ms.metadata).toBeNull();
  expect(ms.setActionHandler).not.toHaveBeenCalled();
});

test("a pause just before the page hides is a background stop at that second", () => {
  const onBackgroundStop = vi.fn();
  const { video } = renderVideoHarness({ onBackgroundStop });
  act(() => fire(video, "play"));
  video.currentTime = 42;
  act(() => fire(video, "pause"));
  expect(onBackgroundStop).not.toHaveBeenCalled();
  act(() => setVisibility("hidden"));
  expect(onBackgroundStop).toHaveBeenCalledWith(42);
});

test("a pause while hidden is a background stop at once; a pause long before the hide is not", () => {
  const onBackgroundStop = vi.fn();
  const { video } = renderVideoHarness({ onBackgroundStop });
  let now = 1_000_000;
  vi.spyOn(Date, "now").mockImplementation(() => now);
  try {
    act(() => fire(video, "play"));
    video.currentTime = 10;
    act(() => fire(video, "pause"));
    now += 5000;
    act(() => setVisibility("hidden"));
    expect(onBackgroundStop).not.toHaveBeenCalled();
    act(() => setVisibility("visible"));
    act(() => fire(video, "play"));
    act(() => setVisibility("hidden"));
    video.currentTime = 20;
    act(() => fire(video, "pause"));
    expect(onBackgroundStop).toHaveBeenCalledWith(20);
  } finally {
    vi.mocked(Date.now).mockRestore();
  }
});

test("pauseOwn and the lock screen's pause never offer a background stop", () => {
  const onBackgroundStop = vi.fn();
  const onPause = vi.fn();
  const { video, result, setPaused } = renderVideoHarness({ onBackgroundStop, onPause });
  vi.mocked(video.pause).mockImplementation(() => {
    setPaused(true);
    fireEvent.pause(video);
  });
  setPaused(false);
  act(() => fire(video, "play"));
  video.currentTime = 7;
  act(() => result.current.pauseOwn());
  expect(onPause).toHaveBeenLastCalledWith({ second: 7, hidden: false, own: true, wasPlaying: true, hideSaved: false });
  act(() => setVisibility("hidden"));
  act(() => setVisibility("visible"));
  setPaused(false);
  act(() => fire(video, "play"));
  act(() => handler("pause")!());
  expect(onPause).toHaveBeenLastCalledWith(expect.objectContaining({ own: true }));
  act(() => setVisibility("hidden"));
  expect(onBackgroundStop).not.toHaveBeenCalled();
});

test("hiding while playing asks for one save per hide (visibilitychange and pagehide)", () => {
  const onHideWhilePlaying = vi.fn();
  const { video } = renderVideoHarness({ onHideWhilePlaying });
  act(() => fire(video, "play"));
  act(() => setVisibility("hidden"));
  fireEvent(window, new Event("pagehide"));
  expect(onHideWhilePlaying).toHaveBeenCalledTimes(1);
  act(() => setVisibility("visible"));
  act(() => setVisibility("hidden"));
  expect(onHideWhilePlaying).toHaveBeenCalledTimes(2);
});

test("ended stops it playing and is not a pause", () => {
  const onPause = vi.fn();
  const { video, result } = renderVideoHarness({ onPause });
  act(() => fire(video, "play"));
  act(() => fire(video, "ended"));
  expect(result.current.playing()).toBe(false);
  expect(onPause).not.toHaveBeenCalled();
});

test("unmount releases the session to music and clears the lock screen", () => {
  const { video } = renderVideoHarness();
  act(() => fire(video, "play"));
  expect(ownsSession("video")).toBe(true);
  cleanup();
  expect(ownsSession("music")).toBe(true);
  expect(ms.metadata).toBeNull();
  expect(handler("pause")).toBeNull();
});

test("release leaves another owner alone", () => {
  const { video, result } = renderVideoHarness();
  act(() => fire(video, "play"));
  act(() => claimSession("preview"));
  act(() => result.current.release());
  expect(ownsSession("preview")).toBe(true);
});

// A <video> that leaves the page (the page goes, another video, ▶ 听 on an
// episode) stops downloading: its src is dropped, not only paused. A src
// change on the same element (the 高清 switch) is left alone.
function SwitchHarness({ show, src }: { show: boolean; src: string }) {
  const ref = useRef<HTMLVideoElement>(null);
  const sv = useSessionVideo(ref, show ? src : null, { metadata: () => null });
  return show ? <video ref={ref} src={src} {...sv.handlers} /> : <p>gone</p>;
}

test("a video taken off the page lets go of its stream; a src change on the same element does not", () => {
  const { rerender, unmount } = render(<SwitchHarness show src="/360.mp4" />);
  const video = document.querySelector("video")!;
  const load = vi.mocked(video.load);
  load.mockClear();
  rerender(<SwitchHarness show src="/720.mp4" />);
  expect(video.getAttribute("src")).toBe("/720.mp4");
  expect(load).not.toHaveBeenCalled();
  rerender(<SwitchHarness show={false} src="/720.mp4" />);
  expect(video.hasAttribute("src")).toBe(false);
  expect(load).toHaveBeenCalled();
  unmount();
});

test("unmounting the page with its video lets go of the stream", () => {
  const { unmount } = render(<SwitchHarness show src="/360.mp4" />);
  const video = document.querySelector("video")!;
  vi.mocked(video.load).mockClear();
  unmount();
  expect(video.hasAttribute("src")).toBe(false);
  expect(video.load).toHaveBeenCalled();
});
