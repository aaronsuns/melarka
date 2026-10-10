import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { followOffset, isHorizontalDrag, SWIPE_MIN_PX, swipeAction, useTrackSwipe } from "./swipe";

test("swipeAction: left past 60 px is next, right is previous", () => {
  expect(swipeAction(-61, 0)).toBe("next");
  expect(swipeAction(61, 0)).toBe("prev");
  expect(swipeAction(-200, 40)).toBe("next");
  expect(swipeAction(150, -30)).toBe("prev");
});

test("swipeAction: 60 px or less is no swipe", () => {
  expect(SWIPE_MIN_PX).toBe(60);
  expect(swipeAction(-60, 0)).toBeNull();
  expect(swipeAction(60, 0)).toBeNull();
  expect(swipeAction(0, 0)).toBeNull();
  expect(swipeAction(-30, 2)).toBeNull();
});

test("swipeAction: sideways must beat 1.5 × the vertical travel (a lyrics scroll or a pull down stays one)", () => {
  expect(swipeAction(-90, 60)).toBeNull(); // exactly 1.5 ×: not enough
  expect(swipeAction(-91, 60)).toBe("next");
  expect(swipeAction(100, 80)).toBeNull();
  expect(swipeAction(10, 300)).toBeNull(); // a vertical scroll
  expect(swipeAction(-80, -200)).toBeNull();
});

test("isHorizontalDrag: the content starts following only a sideways drag", () => {
  expect(isHorizontalDrag(5, 0)).toBe(false); // a tap's jitter
  expect(isHorizontalDrag(20, 5)).toBe(true);
  expect(isHorizontalDrag(-20, 5)).toBe(true);
  expect(isHorizontalDrag(20, 20)).toBe(false);
});

test("followOffset: damped and capped both ways", () => {
  expect(followOffset(0)).toBe(0);
  expect(followOffset(100)).toBe(40);
  expect(followOffset(-100)).toBe(-40);
  expect(followOffset(1000)).toBe(80);
  expect(followOffset(-1000)).toBe(-80);
});

function Swipeable({ next, prev, tap }: { next: () => void; prev: () => void; tap: () => void }) {
  const ref = useTrackSwipe(".area", { next, prev });
  return (
    <div ref={ref}>
      <button className="area" data-swipe-surface="" onClick={tap}>cover</button>
      <button className="control" onClick={() => {}}>play</button>
      <input type="range" aria-label="seek" />
    </div>
  );
}

function swipe(el: Element, dx: number, dy = 0) {
  const at = { clientX: 200, clientY: 300, pointerId: 1, isPrimary: true, pointerType: "touch", button: 0 };
  fireEvent.pointerDown(el, at);
  fireEvent.pointerMove(window, { ...at, clientX: 200 + dx / 2, clientY: 300 + dy / 2 });
  fireEvent.pointerMove(window, { ...at, clientX: 200 + dx, clientY: 300 + dy });
  fireEvent.pointerUp(window, { ...at, clientX: 200 + dx, clientY: 300 + dy });
}

function setup() {
  const next = vi.fn();
  const prev = vi.fn();
  const tap = vi.fn();
  render(<Swipeable next={next} prev={prev} tap={tap} />);
  return { next, prev, tap };
}

test("a left swipe on the area plays the next track, a right one the previous", () => {
  const { next, prev } = setup();
  swipe(screen.getByText("cover"), -120);
  expect(next).toHaveBeenCalledTimes(1);
  expect(prev).not.toHaveBeenCalled();
  swipe(screen.getByText("cover"), 120);
  expect(prev).toHaveBeenCalledTimes(1);
});

test("a short or vertical drag changes nothing", () => {
  const { next, prev } = setup();
  swipe(screen.getByText("cover"), -40);
  swipe(screen.getByText("cover"), -70, 200);
  expect(next).not.toHaveBeenCalled();
  expect(prev).not.toHaveBeenCalled();
});

test("a swipe that starts on a button or the slider is not a track change", () => {
  const { next, prev } = setup();
  swipe(screen.getByText("play"), -150);
  swipe(screen.getByRole("slider", { name: "seek" }), 150);
  expect(next).not.toHaveBeenCalled();
  expect(prev).not.toHaveBeenCalled();
});

test("the click that ends a swipe is swallowed; a plain tap still goes through", () => {
  const { next, tap } = setup();
  const cover = screen.getByText("cover");
  swipe(cover, -120);
  fireEvent.click(cover);
  expect(next).toHaveBeenCalledTimes(1);
  expect(tap).not.toHaveBeenCalled();
  fireEvent.click(cover);
  expect(tap).toHaveBeenCalledTimes(1);
});

test("the area follows the finger during the drag and springs back", () => {
  setup();
  const cover = screen.getByText("cover");
  const at = { clientX: 200, clientY: 300, pointerId: 1, isPrimary: true, pointerType: "touch", button: 0 };
  fireEvent.pointerDown(cover, at);
  fireEvent.pointerMove(window, { ...at, clientX: 150 });
  expect(cover.style.transform).toBe("translateX(-20px)");
  fireEvent.pointerUp(window, { ...at, clientX: 150 });
  expect(cover.style.transform).toBe("");
});

test("with reduced motion the area does not move, the swipe still works", () => {
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: q.includes("reduce"), media: q }));
  try {
    const { next } = setup();
    const cover = screen.getByText("cover");
    const at = { clientX: 200, clientY: 300, pointerId: 1, isPrimary: true, pointerType: "touch", button: 0 };
    fireEvent.pointerDown(cover, at);
    fireEvent.pointerMove(window, { ...at, clientX: 100 });
    expect(cover.style.transform).toBe("");
    fireEvent.pointerUp(window, { ...at, clientX: 100 });
    expect(next).toHaveBeenCalledTimes(1);
  } finally {
    vi.unstubAllGlobals();
  }
});
