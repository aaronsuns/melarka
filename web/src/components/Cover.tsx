import { useEffect, useState } from "react";
import { coverStyle, initials } from "../format";

export type CoverSize = 300 | 1000;

/** A track's or album's cover, from Lark's artwork cache (404 when it has none). */
export function coverUrl(kind: "track" | "album", id: number, size: CoverSize = 300): string {
  return `/api/v1/${kind === "track" ? "tracks" : "albums"}/${id}/cover?size=${size}`;
}

// Covers that failed recently are not asked for again, so a re-rendered list
// doesn't refetch every missing cover; after FAIL_TTL they are retried (a
// phone left open picks up covers found since, or a server hiccup).
const FAIL_TTL = 10 * 60_000;
const failed = new Map<string, number>(); // src → retry after (ms)
export function forgetFailedCovers(): void {
  failed.clear();
}

/** ms until src may be retried; 0 when it hasn't failed recently. */
function failedFor(src: string): number {
  const until = failed.get(src);
  if (until === undefined) return 0;
  const left = until - Date.now();
  if (left <= 0) {
    failed.delete(src);
    return 0;
  }
  return left;
}

function CoverImg({ src, onFail, noReferrer }: { src: string; onFail: () => void; noReferrer?: boolean }) {
  const [loaded, setLoaded] = useState(false);
  return (
    <img
      className={loaded ? "cover-img loaded" : "cover-img"}
      src={src}
      alt=""
      loading="lazy"
      decoding="async"
      draggable={false}
      referrerPolicy={noReferrer ? "no-referrer" : undefined}
      onLoad={() => setLoaded(true)}
      onError={() => {
        failed.set(src, Date.now() + FAIL_TTL);
        onFail();
      }}
    />
  );
}

/**
 * The initials tile, always at its fixed size; when src is given the cover
 * image loads lazily and fades in over it, so nothing shifts.
 */
/**
 * noReferrer: fetch src without a Referer (third-party images such as YouTube thumbnails).
 */
export function Cover({ seed, label, size = 48, round = false, src, noReferrer }: { seed: string; label?: string; size?: number; round?: boolean; src?: string; noReferrer?: boolean }) {
  const [, setFailures] = useState(0);
  const wait = src ? failedFor(src) : 0;
  useEffect(() => {
    if (wait <= 0) return;
    const t = setTimeout(() => setFailures((n) => n + 1), wait); // retry while still on screen
    return () => clearTimeout(t);
  }, [src, wait]);
  return (
    <div
      className="cover"
      aria-hidden="true"
      style={{ ...coverStyle(seed), width: size, height: size, borderRadius: round ? "50%" : size > 100 ? 16 : 8, fontSize: size * 0.42 }}
    >
      {initials(label ?? seed)}
      {src && wait === 0 && <CoverImg key={src} src={src} noReferrer={noReferrer} onFail={() => setFailures((n) => n + 1)} />}
    </div>
  );
}
