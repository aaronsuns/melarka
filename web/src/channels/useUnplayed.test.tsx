import { act, fireEvent, render, screen } from "@testing-library/react";
import { Link, MemoryRouter } from "react-router";
import { afterEach, expect, test, vi } from "vitest";
import { mockFetch } from "../test/setup";
import { useUnplayed } from "./useUnplayed";

function Badge() {
  const n = useUnplayed();
  return (
    <>
      <output>{n}</output>
      <Link to="/library">library</Link>
      <Link to="/search">search</Link>
      <Link to="/channels">channels</Link>
      <Link to="/episodes/episode0001">episode</Link>
    </>
  );
}

afterEach(() => {
  delete (document as unknown as { visibilityState?: string }).visibilityState;
  vi.useRealTimers();
});

test("the badge is read on channel pages and every 5 minutes while visible, not on every music page", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const f = mockFetch({ "GET /api/v1/episodes/latest": () => ({ body: { items: [], unplayed: 4 } }) });
  const calls = () => f.mock.calls.length;
  render(<MemoryRouter initialEntries={["/"]}><Badge /></MemoryRouter>);
  expect(await screen.findByText("4")).toBeInTheDocument();
  expect(calls()).toBe(1);
  fireEvent.click(screen.getByText("library"));
  fireEvent.click(screen.getByText("search"));
  expect(calls()).toBe(1);
  fireEvent.click(screen.getByText("channels"));
  expect(calls()).toBe(2);
  fireEvent.click(screen.getByText("episode"));
  expect(calls()).toBe(3);
  Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
  fireEvent(document, new Event("visibilitychange"));
  await act(() => vi.advanceTimersByTimeAsync(11 * 60_000));
  expect(calls()).toBe(3);
  Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
  fireEvent(document, new Event("visibilitychange"));
  expect(calls()).toBe(4);
  await act(() => vi.advanceTimersByTimeAsync(5 * 60_000));
  expect(calls()).toBe(5);
});
