// Small line icons for the round action buttons (▶ 试听, ⬇ 下载, ✕, ↻ …).
// SVG rather than text glyphs: ⬇ and 🔀 render as colour emoji on iOS, and
// the glyphs' sizes and baselines differ from font to font.
import type { ReactNode } from "react";

function Svg({ children, size = 18 }: { children: ReactNode; size?: number }) {
  return (
    <svg className="svg-icon" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.2} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
      {children}
    </svg>
  );
}

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
