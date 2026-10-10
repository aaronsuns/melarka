import { useEffect, useRef, useState, type CSSProperties } from "react";
import { coverStyle } from "../format";

// The backdrop is painted once per artwork into a canvas this many pixels
// square, then stretched over the screen: the browser's smooth scaling does
// the blurring. A CSS blur over a full-screen layer instead costs a large
// offscreen buffer and a wide filter pass on every repaint (WebKit on Linux:
// about 1.1 GB for the page, against about 0.5 GB without it).
const BACKDROP_PX = 48;
// The artwork is first averaged down to this, so only its broad colours remain.
const SAMPLE_PX = 12;
// As CSS saturate(1.5): the averaged artwork is a little greyer than the original.
const SATURATE = 1.5;

/**
 * Now Playing's backdrop: the current artwork, blurred and darkened,
 * full-bleed behind everything — a tiny canvas, painted once per track and
 * scaled up (no CSS filter, no per-frame work). Under it the generated tile
 * colours of the cover, which are all that shows when there is no artwork.
 * A scrim on top keeps the text readable (WCAG AA) over any artwork.
 */
export function NowBackground({ seed, src, noReferrer }: { seed: string; src?: string; noReferrer?: boolean }) {
  const canvas = useRef<HTMLCanvasElement>(null);
  // The artwork the canvas holds; it fades in once painted.
  const [painted, setPainted] = useState<string | null>(null);
  useEffect(() => {
    if (!src) return;
    let live = true;
    const img = new Image();
    img.decoding = "async";
    if (noReferrer) img.referrerPolicy = "no-referrer";
    img.onload = () => {
      if (live && canvas.current && paintBackdrop(canvas.current, img)) setPainted(src);
    };
    img.src = src;
    return () => {
      live = false;
      img.onload = null;
    };
  }, [src, noReferrer]);
  return (
    <div className="now-bg" aria-hidden="true">
      <div className="now-bg-tile" style={coverStyle(seed)} />
      {src && (
        // A new track gets a new (blank) canvas: the old artwork never shows under the new title.
        <canvas
          key={src}
          ref={canvas}
          width={BACKDROP_PX}
          height={BACKDROP_PX}
          className={painted === src ? "now-bg-img loaded" : "now-bg-img"}
        />
      )}
      <div className="now-bg-scrim" />
    </div>
  );
}

/**
 * Paints the artwork's blurred, darkened backdrop into `c`: averaged down to
 * SAMPLE_PX and its colours boosted, smoothed back up to the canvas size
 * (with a small blur where the canvas supports filters), then halved in
 * brightness — exactly brightness(.5), last, so no channel ends up above half:
 * the scrim's AA sums rely on it. Cross-origin artwork (episode thumbnails)
 * cannot be read back, so it goes without the colour boost. False when there
 * is no 2D context.
 */
export function paintBackdrop(c: HTMLCanvasElement, img: CanvasImageSource): boolean {
  const ctx = c.getContext("2d");
  const small = document.createElement("canvas");
  small.width = SAMPLE_PX;
  small.height = SAMPLE_PX;
  const sctx = small.getContext("2d");
  if (!ctx || !sctx) return false;
  sctx.imageSmoothingEnabled = true;
  sctx.imageSmoothingQuality = "high";
  sctx.drawImage(img, 0, 0, SAMPLE_PX, SAMPLE_PX);
  saturate(sctx, SATURATE);
  ctx.clearRect(0, 0, c.width, c.height);
  ctx.imageSmoothingEnabled = true;
  ctx.imageSmoothingQuality = "high";
  const filters = "filter" in ctx;
  if (filters) ctx.filter = "blur(2px)";
  // Drawn a little larger than the canvas, so the blur's soft edges fall outside it.
  const pad = c.width / 8;
  ctx.drawImage(small, -pad, -pad, c.width + 2 * pad, c.height + 2 * pad);
  if (filters) ctx.filter = "none";
  ctx.fillStyle = "rgba(0, 0, 0, 0.5)";
  ctx.fillRect(0, 0, c.width, c.height);
  return true;
}

// CSS saturate(s) on the sampled pixels (the filter spec's matrix). Skipped
// for artwork the page may not read (cross-origin, a tainted canvas).
function saturate(ctx: CanvasRenderingContext2D, s: number) {
  let data: ImageData;
  try {
    data = ctx.getImageData(0, 0, ctx.canvas.width, ctx.canvas.height);
  } catch {
    return;
  }
  const d = data.data;
  for (let i = 0; i < d.length; i += 4) {
    const [r, g, b] = [d[i], d[i + 1], d[i + 2]];
    d[i] = (0.213 + 0.787 * s) * r + (0.715 - 0.715 * s) * g + (0.072 - 0.072 * s) * b;
    d[i + 1] = (0.213 - 0.213 * s) * r + (0.715 + 0.285 * s) * g + (0.072 - 0.072 * s) * b;
    d[i + 2] = (0.213 - 0.213 * s) * r + (0.715 - 0.715 * s) * g + (0.072 + 0.928 * s) * b;
  }
  ctx.putImageData(data, 0, 0); // Uint8ClampedArray: rounded and clamped to 0..255
}

/** The seek bar's played share, for its track's fill (--pct). */
export function rangeFill(position: number, duration: number): CSSProperties {
  const max = Math.max(1, Math.round(duration));
  const pct = Math.max(0, Math.min(1, Math.round(position) / max)) * 100;
  return { "--pct": `${pct}%` } as CSSProperties;
}
