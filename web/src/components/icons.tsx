// The app's icons, all on one 24 × 24 grid: line icons for the round action
// buttons (▶ 试听, ⬇ 下载, ✕, ↻ …) and filled, SF-Symbols-like glyphs for the
// players' transport (▶ ⏸ ⏮ ⏭, ±15/30 s). SVG rather than text glyphs: ⬇ and
// 🔀 render as colour emoji on iOS, and the glyphs' sizes and baselines differ
// from font to font.
import type { ReactNode } from "react";

function Svg({ children, size = 18, stroke = 2.2 }: { children: ReactNode; size?: number; stroke?: number }) {
  return (
    <svg className="svg-icon" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={stroke} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
      {children}
    </svg>
  );
}

/** A filled glyph: no outline, so it reads as a solid shape at any size. */
function Glyph({ children, size = 24 }: { children: ReactNode; size?: number }) {
  return (
    <svg className="svg-icon glyph" width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" focusable="false">
      {children}
    </svg>
  );
}

// Transport: a rounded play triangle, two rounded pause bars, and the
// double-triangle ⏮ ⏭ (one shape, mirrored for ⏮).
const PLAY_D = "M7.2 4.4c0-1.06 1.16-1.7 2.06-1.14l11.16 7.1c.83.53.83 1.75 0 2.28L9.26 19.74c-.9.57-2.06-.08-2.06-1.14z";
const FORWARD_D =
  "M1.6 6.3c0-.95 1.07-1.5 1.84-.95l7.36 5.28c.62.44.62 1.37 0 1.81l-7.36 5.28c-.77.55-1.84 0-1.84-.95zM11.9 6.3c0-.95 1.07-1.5 1.84-.95l7.36 5.28c.62.44.62 1.37 0 1.81l-7.36 5.28c-.77.55-1.84 0-1.84-.95z";
export const PlayFillIcon = ({ size }: { size?: number }) => (
  <Glyph size={size}>
    <path d={PLAY_D} />
  </Glyph>
);
export const PauseFillIcon = ({ size }: { size?: number }) => (
  <Glyph size={size}>
    <rect x="5.8" y="4.2" width="4.4" height="15.6" rx="1.3" />
    <rect x="13.8" y="4.2" width="4.4" height="15.6" rx="1.3" />
  </Glyph>
);
export const ForwardFillIcon = ({ size }: { size?: number }) => (
  <Glyph size={size}>
    <path d={FORWARD_D} transform="translate(0.7 0)" />
  </Glyph>
);
export const BackwardFillIcon = ({ size }: { size?: number }) => (
  <Glyph size={size}>
    <path d={FORWARD_D} transform="matrix(-1 0 0 1 23.3 0)" />
  </Glyph>
);
/** ↺15 / 30↻: a clock arrow round the seconds it skips (gobackward.15 / goforward.30). */
function SkipGlyph({ seconds, forward, size }: { seconds: number; forward: boolean; size?: number }) {
  return (
    <svg className="svg-icon glyph" width={size ?? 24} height={size ?? 24} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <g transform={forward ? "matrix(-1 0 0 1 24 0)" : undefined}>
        <path d="M12 4.6a8.4 8.4 0 1 1-8.4 8.4" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" />
        <path d="M12.6 1.9v5.4c0 .4-.45.63-.77.4L8.4 5.1a.5.5 0 0 1 0-.8l3.43-2.6c.32-.24.77-.01.77.4z" fill="currentColor" />
      </g>
      <text x="12" y="16.4" textAnchor="middle" fontSize="7.6" fontWeight="700" fill="currentColor" style={{ fontFamily: "-apple-system, BlinkMacSystemFont, 'Helvetica Neue', sans-serif" }}>
        {seconds}
      </text>
    </svg>
  );
}
export const SkipBackIcon = ({ seconds, size }: { seconds: number; size?: number }) => <SkipGlyph seconds={seconds} forward={false} size={size} />;
export const SkipForwardIcon = ({ seconds, size }: { seconds: number; size?: number }) => <SkipGlyph seconds={seconds} forward size={size} />;

// Now Playing's header and actions.
export const ChevronDownIcon = ({ size = 24 }: { size?: number }) => (
  <Svg size={size} stroke={2.4}>
    <path d="M5.5 9.5 12 15.5l6.5-6" />
  </Svg>
);
export const ListIcon = ({ size = 22 }: { size?: number }) => (
  <Svg size={size} stroke={2}>
    <path d="M9 6.5h11M9 12h11M9 17.5h11" />
    <circle cx="4.6" cy="6.5" r="1.3" fill="currentColor" stroke="none" />
    <circle cx="4.6" cy="12" r="1.3" fill="currentColor" stroke="none" />
    <circle cx="4.6" cy="17.5" r="1.3" fill="currentColor" stroke="none" />
  </Svg>
);
export const MoreIcon = ({ size = 22 }: { size?: number }) => (
  <Glyph size={size}>
    <circle cx="5" cy="12" r="1.9" />
    <circle cx="12" cy="12" r="1.9" />
    <circle cx="19" cy="12" r="1.9" />
  </Glyph>
);
/** Back to the cover from the lyrics: a picture. */
export const PhotoIcon = ({ size = 22 }: { size?: number }) => (
  <Svg size={size} stroke={2}>
    <rect x="3.5" y="4.5" width="17" height="15" rx="3" />
    <circle cx="9" cy="10" r="1.6" fill="currentColor" stroke="none" />
    <path d="M4 17l4.6-4.3 3.4 3 3-2.6 5 4.4" />
  </Svg>
);
const HEART_D = "M12 20.2s-7.7-4.6-8.9-10C2.4 7 4.4 4.3 7.4 4.3c2 0 3.6 1.1 4.6 2.8 1-1.7 2.6-2.8 4.6-2.8 3 0 5 2.7 4.3 5.9-1.2 5.4-8.9 10-8.9 10z";
export const HeartIcon = ({ size = 22, filled = false }: { size?: number; filled?: boolean }) => (
  <Svg size={size} stroke={2}>
    <path d={HEART_D} fill={filled ? "currentColor" : "none"} />
  </Svg>
);
export const ThumbDownIcon = ({ size = 22 }: { size?: number }) => (
  <Svg size={size} stroke={2}>
    <path d="M7.5 14.5V3.5M7.5 3.5h9.3a2 2 0 0 1 2 1.6l1.2 6.6a2.2 2.2 0 0 1-2.2 2.6H13.4l.8 3.4a2.1 2.1 0 0 1-3.9 1.5L7.5 14.5H5a1.5 1.5 0 0 1-1.5-1.5V5A1.5 1.5 0 0 1 5 3.5z" />
  </Svg>
);
export const PencilIcon = ({ size = 22 }: { size?: number }) => (
  <Svg size={size} stroke={2}>
    <path d="M14.5 5.5l4 4M4.5 19.5l1-4.5L16 4.5a1.4 1.4 0 0 1 2 0l1.5 1.5a1.4 1.4 0 0 1 0 2L9 18.5z" />
  </Svg>
);
export const TagIcon = ({ size = 22 }: { size?: number }) => (
  <Svg size={size} stroke={2}>
    <path d="M3.5 12.2V4.8c0-.7.6-1.3 1.3-1.3h7.4c.4 0 .7.1.9.4l7.4 7.4c.5.5.5 1.3 0 1.8l-7.4 7.4c-.5.5-1.3.5-1.8 0l-7.4-7.4c-.3-.2-.4-.5-.4-.9z" />
    <circle cx="8" cy="8" r="1.5" fill="currentColor" stroke="none" />
  </Svg>
);
export const MicIcon = ({ size = 22 }: { size?: number }) => (
  <Svg size={size} stroke={2}>
    <rect x="9" y="3" width="6" height="11" rx="3" />
    <path d="M5.5 11.5a6.5 6.5 0 0 0 13 0M12 18v3" />
  </Svg>
);
export const TrashIcon = ({ size = 22 }: { size?: number }) => (
  <Svg size={size} stroke={2}>
    <path d="M4 6.5h16M9.5 6.5V4.8c0-.7.6-1.3 1.3-1.3h2.4c.7 0 1.3.6 1.3 1.3v1.7M6 6.5l.9 12.6c.1 1 .9 1.9 2 1.9h6.2c1 0 1.9-.8 2-1.9L18 6.5M10 10.5v6.5M14 10.5v6.5" />
  </Svg>
);

export const PlayIcon = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <path d="M8 5.5v13l10.5-6.5z" fill="currentColor" stroke="none" />
  </Svg>
);
export const DownloadIcon = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <path d="M12 4v11M7 10.5l5 5 5-5M5.5 19.5h13" />
  </Svg>
);
export const CloseIcon = ({ size = 16 }: { size?: number }) => (
  <Svg size={size}>
    <path d="M6.5 6.5l11 11M17.5 6.5l-11 11" />
  </Svg>
);
export const RetryIcon = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <path d="M19.5 12a7.5 7.5 0 1 1-2.2-5.3M19.5 4.5v4.2h-4.2" />
  </Svg>
);
export const CheckIcon = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <path d="M5.5 12.5l4.2 4.2 8.8-9.2" />
  </Svg>
);
export const ClockIcon = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <circle cx="12" cy="12" r="8" />
    <path d="M12 8v4.5l3 1.8" />
  </Svg>
);
export const ShuffleIcon = ({ size = 16 }: { size?: number }) => (
  <Svg size={size}>
    <path d="M4 7h3.5c4.5 0 4.5 10 9 10H20M4 17h3.5c1.6 0 2.6-1.2 3.4-2.8M13.1 9.8C13.9 8.2 14.9 7 16.5 7H20M17.5 4.5 20 7l-2.5 2.5M17.5 14.5 20 17l-2.5 2.5" />
  </Svg>
);
const REPEAT_PATH = "M17 3.5l3 3-3 3M4 11.5v-1a4 4 0 0 1 4-4h12M7 20.5l-3-3 3-3M20 12.5v1a4 4 0 0 1-4 4H4";
/** Repeat (all): two arrows chasing each other round a loop. */
export const RepeatIcon = ({ size = 16 }: { size?: number }) => (
  <Svg size={size}>
    <path d={REPEAT_PATH} />
  </Svg>
);
/** Repeat one: the repeat loop with a "1" in the middle. */
export const RepeatOneIcon = ({ size = 16 }: { size?: number }) => (
  <Svg size={size}>
    <path d={REPEAT_PATH} />
    <text className="repeat-one-badge" x="12" y="15" textAnchor="middle" fontSize="8.5" fontWeight="700" fill="currentColor" stroke="none">1</text>
  </Svg>
);
/** 关注频道: a person with a plus. */
export const FollowIcon = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <circle cx="9.5" cy="8.5" r="3.5" />
    <path d="M3.5 19.5c.6-3.2 3-5 6-5s5.4 1.8 6 5M18.5 8v6M15.5 11h6" />
  </Svg>
);
/** 已关注: a person with a tick. */
export const FollowingIcon = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <circle cx="9.5" cy="8.5" r="3.5" />
    <path d="M3.5 19.5c.6-3.2 3-5 6-5s5.4 1.8 6 5M15.5 11l2 2 4-4" />
  </Svg>
);
export const PlusIcon = ({ size = 16 }: { size?: number }) => (
  <Svg size={size}>
    <path d="M12 5v14M5 12h14" />
  </Svg>
);

/**
 * A download's progress as a ring: a spinning arc while queued (no
 * percentage yet), the share done once it downloads.
 */
export function ProgressRing({ pct }: { pct: number | null }) {
  const r = 15;
  const c = 2 * Math.PI * r;
  return (
    <svg className={pct === null ? "ring ring-spin" : "ring"} width="34" height="34" viewBox="0 0 34 34" aria-hidden="true" focusable="false">
      <circle cx="17" cy="17" r={r} fill="none" stroke="currentColor" strokeOpacity=".2" strokeWidth="2.5" />
      <circle
        cx="17" cy="17" r={r} fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round"
        strokeDasharray={c} strokeDashoffset={pct === null ? c * 0.72 : c * (1 - Math.max(0.03, Math.min(1, pct / 100)))}
        transform="rotate(-90 17 17)"
      />
    </svg>
  );
}
