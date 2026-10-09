import { useEffect, useRef, useState } from "react";
import { useT } from "../i18n/i18n";
import { formatRemaining, SLEEP_MINUTES } from "./sleepTimer";
import { useSleepTimer, useTrackRemainingMs } from "./SleepTimerProvider";

function Chip({ time, onClick }: { time: string; onClick: () => void }) {
  const t = useT();
  return (
    <button className="chip sleep-chip" aria-label={t("sleep.chip", { time })} onClick={onClick}>
      🌙 {time}
    </button>
  );
}

// "End of this track": the time left in it (reads the progress, so it ticks).
function TrackChip({ onClick }: { onClick: () => void }) {
  const ms = useTrackRemainingMs();
  return <Chip time={formatRemaining(ms ?? 0)} onClick={onClick} />;
}

/**
 * Now Playing's ⋯ menu with the sleep timer, and the 🌙 chip while one is
 * set (tapping it opens the same menu). Rendered by music's and episodes'
 * Now Playing; hidden where the timer isn't available (the iPhone app).
 */
export function SleepTimerMenu() {
  const t = useT();
  const s = useSleepTimer();
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLSpanElement>(null);

  // A tap anywhere else closes the menu.
  useEffect(() => {
    if (!open) return;
    const away = (e: PointerEvent) => {
      if (!box.current?.contains(e.target as Node)) setOpen(false);
    };
    const esc = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", away);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("pointerdown", away);
      document.removeEventListener("keydown", esc);
    };
  }, [open]);

  if (!s.available) return null;
  const toggle = () => setOpen((o) => !o);
  return (
    <span className="sleep-menu" ref={box}>
      {s.choice &&
        ("endOfTrack" in s.choice ? <TrackChip onClick={toggle} /> : <Chip time={formatRemaining(s.remainingMs ?? 0)} onClick={toggle} />)}
      <button className="icon" aria-label={t("now.more")} aria-haspopup="menu" aria-expanded={open} onClick={toggle}>⋯</button>
      {open && (
        <div className="menu" role="menu" aria-label={t("sleep.title")}>
          <div className="menu-title muted" role="presentation">{t("sleep.title")}</div>
          {SLEEP_MINUTES.map((n) => (
            <button key={n} role="menuitem" onClick={() => { s.start({ minutes: n }); setOpen(false); }}>{t("sleep.minutes", { n })}</button>
          ))}
          <button role="menuitem" onClick={() => { s.start({ endOfTrack: true }); setOpen(false); }}>{t("sleep.endOfTrack")}</button>
          {s.choice && (
            <button role="menuitem" onClick={() => { s.cancel(); setOpen(false); }}>{t("sleep.off")}</button>
          )}
        </div>
      )}
    </span>
  );
}
