import { useState, type CSSProperties } from "react";
import { coverStyle } from "../format";

/**
 * Now Playing's backdrop: the current artwork, blurred and darkened,
 * full-bleed behind everything. One <img> with a CSS filter (no canvas, no
 * per-frame work); under it the generated tile colours of the cover, which
 * are all that shows when there is no artwork. A scrim on top keeps the
 * text readable (WCAG AA) over any artwork.
 */
export function NowBackground({ seed, src, noReferrer }: { seed: string; src?: string; noReferrer?: boolean }) {
  const [state, setState] = useState<{ src?: string; loaded: boolean; failed: boolean }>({ src, loaded: false, failed: false });
  // A new track: start over (render-time reset, so the old artwork never shows under the new title).
  if (state.src !== src) setState({ src, loaded: false, failed: false });
  return (
    <div className="now-bg" aria-hidden="true">
      <div className="now-bg-tile" style={coverStyle(seed)} />
      {src && !state.failed && (
        <img
          key={src}
          className={state.loaded ? "now-bg-img loaded" : "now-bg-img"}
          src={src}
          alt=""
          decoding="async"
          draggable={false}
          referrerPolicy={noReferrer ? "no-referrer" : undefined}
          onLoad={() => setState((s) => (s.src === src ? { ...s, loaded: true } : s))}
          onError={() => setState((s) => (s.src === src ? { ...s, failed: true } : s))}
        />
      )}
      <div className="now-bg-scrim" />
    </div>
  );
}

/** The seek bar's played share, for its track's fill (--pct). */
export function rangeFill(position: number, duration: number): CSSProperties {
  const max = Math.max(1, Math.round(duration));
  const pct = Math.max(0, Math.min(1, Math.round(position) / max)) * 100;
  return { "--pct": `${pct}%` } as CSSProperties;
}
