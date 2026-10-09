import { act, fireEvent, render } from "@testing-library/react";
import { initials } from "../format";
import { Cover, coverUrl } from "./Cover";

test("coverUrl", () => {
  expect(coverUrl("track", 5)).toBe("/api/v1/tracks/5/cover?size=300");
  expect(coverUrl("album", 3, 1000)).toBe("/api/v1/albums/3/cover?size=1000");
});

test("the initials tile stays; the cover fades in over it once loaded, without changing size", () => {
  const { container } = render(<Cover seed="精选" label="甜蜜蜜" size={44} src={coverUrl("track", 5)} />);
  const tile = container.querySelector(".cover") as HTMLElement;
  const img = tile.querySelector("img")!;
  expect(img).toHaveAttribute("src", "/api/v1/tracks/5/cover?size=300");
  expect(img).toHaveAttribute("loading", "lazy");
  expect(img).toHaveAttribute("alt", "");
  expect(img).not.toHaveClass("loaded");
  expect(tile).toHaveTextContent(initials("甜蜜蜜"));
  fireEvent.load(img);
  expect(img).toHaveClass("loaded");
  expect(tile.style.width).toBe("44px");
  expect(tile.style.height).toBe("44px");
});

test("a missing cover leaves the initials and is not requested again", () => {
  const first = render(<Cover seed="x" label="甜蜜蜜" src={coverUrl("track", 6)} />);
  fireEvent.error(first.container.querySelector("img")!);
  expect(first.container.querySelector("img")).toBeNull();
  expect(first.container.textContent).toBe(initials("甜蜜蜜"));
  first.unmount();
  const again = render(<Cover seed="x" label="甜蜜蜜" src={coverUrl("track", 6)} />);
  expect(again.container.querySelector("img")).toBeNull();
});

test("no src, no image", () => {
  const { container } = render(<Cover seed="x" />);
  expect(container.querySelector("img")).toBeNull();
});

test("a failed cover is asked for again after 10 minutes, even by a cover left on screen", () => {
  vi.useFakeTimers();
  try {
    const src = coverUrl("track", 7);
    const shown = render(<Cover seed="x" label="甜蜜蜜" src={src} />);
    fireEvent.error(shown.container.querySelector("img")!);
    act(() => vi.advanceTimersByTime(9 * 60_000));
    expect(shown.container.querySelector("img")).toBeNull();
    expect(render(<Cover seed="x" src={src} />).container.querySelector("img")).toBeNull();
    act(() => vi.advanceTimersByTime(60_001));
    expect(shown.container.querySelector("img")).toHaveAttribute("src", src);
    expect(render(<Cover seed="x" src={src} />).container.querySelector("img")).toHaveAttribute("src", src);
  } finally {
    vi.useRealTimers();
  }
});
