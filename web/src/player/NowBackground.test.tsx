import { act, render } from "@testing-library/react";
import { NowBackground, paintBackdrop } from "./NowBackground";

// A 2D context that records what is drawn.
function recorder(pixels?: Uint8ClampedArray) {
  const ops: string[] = [];
  const ctx = {
    canvas: { width: 12, height: 12 },
    filter: "none",
    fillStyle: "",
    imageSmoothingEnabled: false,
    imageSmoothingQuality: "low",
    clearRect: () => ops.push("clear"),
    drawImage: (...a: unknown[]) => ops.push(`draw ${a.slice(1).join(",")} filter=${ctx.filter}`),
    fillRect: (...a: number[]) => ops.push(`fill ${ctx.fillStyle} ${a.join(",")}`),
    getImageData: () => {
      if (!pixels) throw new DOMException("tainted", "SecurityError"); // cross-origin artwork
      return { data: pixels };
    },
    putImageData: () => ops.push("put"),
  };
  return { ctx, ops };
}

afterEach(() => vi.restoreAllMocks());

test("the backdrop is the artwork averaged down, smoothed up with a small blur, then exactly half as bright", () => {
  const { ctx, ops } = recorder();
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(ctx as unknown as CanvasRenderingContext2D);
  const c = document.createElement("canvas");
  c.width = 48;
  c.height = 48;
  expect(paintBackdrop(c, new Image())).toBe(true);
  expect(ops[0]).toBe("draw 0,0,12,12 filter=none"); // averaged down
  expect(ops).not.toContain("put"); // cross-origin: drawn as is, without the colour boost
  expect(ops).toContain("draw -6,-6,60,60 filter=blur(2px)"); // soft edges outside the canvas
  expect(ops.at(-1)).toBe("fill rgba(0, 0, 0, 0.5) 0,0,48,48"); // brightness(.5): the scrim's AA sums rely on it
  expect(ctx.filter).toBe("none");
});

test("readable artwork gets the colour boost of CSS saturate(1.5) before it is darkened", () => {
  const px = new Uint8ClampedArray([200, 100, 50, 255, 128, 128, 128, 255]);
  const { ctx, ops } = recorder(px);
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(ctx as unknown as CanvasRenderingContext2D);
  paintBackdrop(document.createElement("canvas"), new Image());
  expect([...px]).toEqual([241, 91, 16, 255, 128, 128, 128, 255]); // grey stays grey
  expect(ops.indexOf("put")).toBeLessThan(ops.findIndex((o) => o.startsWith("fill")));
});

test("no 2D context: nothing is painted and the tile colours stay", () => {
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
  expect(paintBackdrop(document.createElement("canvas"), new Image())).toBe(false);
});

test("the canvas fades in once its artwork is painted, and a new track starts from a blank one", () => {
  const { ctx } = recorder();
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(ctx as unknown as CanvasRenderingContext2D);
  const loads: (() => void)[] = [];
  vi.spyOn(window, "Image").mockImplementation(function (this: HTMLImageElement) {
    const img = document.createElement("img");
    Object.defineProperty(img, "src", { set: () => loads.push(() => img.onload?.(new Event("load"))) });
    return img;
  } as unknown as () => HTMLImageElement);
  const { container, rerender } = render(<NowBackground seed="a" src="/c/1" />);
  const first = container.querySelector("canvas")!;
  expect(first.className).toBe("now-bg-img");
  expect(container.querySelector(".now-bg-tile")).not.toBeNull();
  act(() => loads[0]());
  expect(first.className).toBe("now-bg-img loaded");
  expect(first.width).toBe(48); // a tiny bitmap, stretched by CSS
  rerender(<NowBackground seed="a" src="/c/2" />);
  const second = container.querySelector("canvas")!;
  expect(second).not.toBe(first);
  expect(second.className).toBe("now-bg-img");
  act(() => loads[0]()); // the old artwork arriving late paints nothing
  expect(second.className).toBe("now-bg-img");
  act(() => loads[1]());
  expect(second.className).toBe("now-bg-img loaded");
});

test("no artwork: only the tile colours and the scrim", () => {
  const { container } = render(<NowBackground seed="a" />);
  expect(container.querySelector("canvas")).toBeNull();
  expect(container.querySelector(".now-bg-scrim")).not.toBeNull();
});
