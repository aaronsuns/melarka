import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import type { Track } from "../api/types";
import { t } from "../i18n/i18n";
import { PlayerCtx, type Player } from "./PlayerProvider";
import { QueueSheet } from "./QueueSheet";

const tr = (id: number) => ({ id, title: `song ${id}`, artist: "a", album: "b", duration_ms: 200_000 }) as Track;
const ROW_H = 50;

function renderSheet(index = 0, tracks = [1, 2, 3, 4, 5].map(tr)) {
  const p = {
    queue: { tracks, index, source: "list" },
    current: tracks[index],
    modes: { shuffle: false, repeat: "off" },
    jump: vi.fn(),
    move: vi.fn(),
    removeAt: vi.fn(),
  } as unknown as Player;
  // Rows measure as 50 px tall and 300 px wide (jsdom lays nothing out).
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
    x: 0, y: 0, top: 0, left: 0, bottom: ROW_H, right: 300, width: 300, height: ROW_H, toJSON: () => ({}),
  } as DOMRect);
  render(<PlayerCtx.Provider value={p}><QueueSheet /></PlayerCtx.Provider>);
  return p;
}
const handle = (n: number) => screen.getByRole("button", { name: t("queue.drag", { title: `song ${n}` }) });
const row = (n: number) => screen.getByRole("button", { name: new RegExp(`^song ${n}`) }).closest("li")!;

afterEach(() => vi.restoreAllMocks());

test("the current track comes first, marked, then what is up next", () => {
  renderSheet(1);
  const titles = screen.getAllByRole("listitem").map((li) => li.querySelector(".queue-title")?.textContent);
  expect(titles).toEqual(["song 2", "song 3", "song 4", "song 5"]);
  expect(screen.getByRole("button", { name: /^song 2/ })).toHaveAttribute("aria-current", "true");
  expect(screen.getByText(t("queue.nowPlaying"))).toBeInTheDocument();
});

test("a tap on a row plays it", () => {
  const p = renderSheet(1);
  fireEvent.click(screen.getByRole("button", { name: /^song 4/ }));
  expect(p.jump).toHaveBeenCalledWith(3);
});

test("dragging a row's handle by two rows up moves it there", () => {
  const p = renderSheet(0);
  const h = handle(4); // queue entry 3
  fireEvent.pointerDown(h, { pointerId: 1, clientY: 400, button: 0 });
  fireEvent.pointerMove(h, { pointerId: 1, clientY: 360 });
  fireEvent.pointerMove(h, { pointerId: 1, clientY: 300 });
  expect(row(4).style.transform).toBe("translateY(-100px)"); // follows the finger
  expect(row(2).style.transform).toBe(`translateY(${ROW_H}px)`); // makes room
  expect(row(3).style.transform).toBe(`translateY(${ROW_H}px)`);
  fireEvent.pointerUp(h, { pointerId: 1, clientY: 300 });
  expect(p.move).toHaveBeenCalledWith(3, 1);
  expect(row(4).style.transform).toBe("");
});

test("the current track can be dragged too, with indexes in queue terms", () => {
  const p = renderSheet(2);
  const h = handle(3);
  fireEvent.pointerDown(h, { pointerId: 1, clientY: 100, button: 0 });
  fireEvent.pointerMove(h, { pointerId: 1, clientY: 200 });
  fireEvent.pointerUp(h, { pointerId: 1, clientY: 200 });
  expect(p.move).toHaveBeenCalledWith(2, 4);
});

test("a drag back to where it began, or one the browser cancels, moves nothing", () => {
  const p = renderSheet(0);
  const h = handle(3);
  fireEvent.pointerDown(h, { pointerId: 1, clientY: 100, button: 0 });
  fireEvent.pointerMove(h, { pointerId: 1, clientY: 110 });
  fireEvent.pointerUp(h, { pointerId: 1, clientY: 110 });
  fireEvent.pointerDown(h, { pointerId: 2, clientY: 100, button: 0 });
  fireEvent.pointerMove(h, { pointerId: 2, clientY: 250 });
  fireEvent.pointerCancel(h, { pointerId: 2 });
  expect(row(3).style.transform).toBe("");
  expect(p.move).not.toHaveBeenCalled();
});

test("✕ removes a row; the current row has no ✕", () => {
  const p = renderSheet(1);
  fireEvent.click(screen.getByRole("button", { name: t("queue.remove", { title: "song 4" }) }));
  expect(p.removeAt).toHaveBeenCalledWith(3);
  expect(screen.queryByRole("button", { name: t("queue.remove", { title: "song 2" }) })).toBeNull();
  expect(handle(2)).toBeInTheDocument(); // but it has a handle
});

test("a left swipe past 35% of the row removes it; a shorter one springs back and plays nothing", () => {
  const p = renderSheet(0);
  const r = row(3);
  fireEvent.pointerDown(r, { pointerId: 1, clientX: 250, clientY: 20, button: 0 });
  fireEvent.pointerMove(r, { pointerId: 1, clientX: 200, clientY: 22 });
  expect(r.style.transform).toBe("translateX(-50px)");
  fireEvent.pointerUp(r, { pointerId: 1, clientX: 200, clientY: 22 });
  fireEvent.click(screen.getByRole("button", { name: /^song 3/ })); // the click that ends the swipe
  expect(p.removeAt).not.toHaveBeenCalled();
  expect(p.jump).not.toHaveBeenCalled();
  expect(r.style.transform).toBe("");
  fireEvent.pointerDown(r, { pointerId: 2, clientX: 250, clientY: 20, button: 0 });
  fireEvent.pointerMove(r, { pointerId: 2, clientX: 140, clientY: 25 });
  fireEvent.pointerUp(r, { pointerId: 2, clientX: 140, clientY: 25 });
  expect(p.removeAt).toHaveBeenCalledWith(2);
});

test("the current row doesn't swipe away", () => {
  const p = renderSheet(0);
  const r = row(1);
  fireEvent.pointerDown(r, { pointerId: 1, clientX: 250, clientY: 20, button: 0 });
  fireEvent.pointerMove(r, { pointerId: 1, clientX: 50, clientY: 20 });
  fireEvent.pointerUp(r, { pointerId: 1, clientX: 50, clientY: 20 });
  expect(p.removeAt).not.toHaveBeenCalled();
  expect(r.style.transform).toBe("");
});

test("a vertical move on a row is a scroll, not a swipe", () => {
  const p = renderSheet(0);
  const r = row(3);
  fireEvent.pointerDown(r, { pointerId: 1, clientX: 250, clientY: 20, button: 0 });
  fireEvent.pointerMove(r, { pointerId: 1, clientX: 140, clientY: 200 });
  fireEvent.pointerUp(r, { pointerId: 1, clientX: 140, clientY: 200 });
  expect(p.removeAt).not.toHaveBeenCalled();
});

test("ArrowUp / ArrowDown on a handle move the row by one", () => {
  const p = renderSheet(0);
  fireEvent.keyDown(handle(3), { key: "ArrowUp" });
  expect(p.move).toHaveBeenLastCalledWith(2, 1);
  fireEvent.keyDown(handle(3), { key: "ArrowDown" });
  expect(p.move).toHaveBeenLastCalledWith(2, 3);
  (p.move as ReturnType<typeof vi.fn>).mockClear();
  fireEvent.keyDown(handle(5), { key: "ArrowDown" }); // already last
  fireEvent.keyDown(handle(1), { key: "ArrowUp" }); // already first
  expect(p.move).not.toHaveBeenCalled();
});

test("with nothing up next it says what happens at the end", () => {
  renderSheet(0, [tr(1)]);
  expect(screen.getByText(t("now.radioEnd"))).toBeInTheDocument();
});
