// Swipe to change track, on Now Playing's artwork and title and on the mini
// players: left = next, right = previous. Pointer events, so it works the
// same with a finger (iPhone Safari, the app's WKWebView) and a mouse. The
// swiped areas carry `touch-action: pan-y`: the browser keeps vertical pans
// (lyrics scrolling, the page) and hands horizontal ones to us.
import { useEffect, useRef, useState } from "react";

/** How far (px) a finger must travel sideways to change track. */
export const SWIPE_MIN_PX = 60;
/** …and how much more sideways than up or down. */
export const SWIPE_RATIO = 1.5;
/** Past this the gesture counts as a horizontal drag and the content follows the finger. */
export const SWIPE_FOLLOW_PX = 10;

export type SwipeAction = "next" | "prev";

/** The track change a finished gesture asks for, or null (too short, or more up/down than sideways). */
export function swipeAction(dx: number, dy: number): SwipeAction | null {
  if (Math.abs(dx) <= SWIPE_MIN_PX || Math.abs(dx) <= SWIPE_RATIO * Math.abs(dy)) return null;
  return dx < 0 ? "next" : "prev";
}

/** Whether a gesture in progress has become a sideways drag (the content starts following it). */
export function isHorizontalDrag(dx: number, dy: number): boolean {
  return Math.abs(dx) > SWIPE_FOLLOW_PX && Math.abs(dx) > SWIPE_RATIO * Math.abs(dy);
}

/** How far the content follows the finger: a damped share of dx, never more than 80 px. */
export function followOffset(dx: number): number {
  const d = dx * 0.4;
  return Math.max(-80, Math.min(80, d));
}

// A swipe never starts on a control: the progress slider, the transport and
// action buttons, the lyrics tools. The swiped area itself may be a button
// (Now Playing's cover, the mini player's title), marked data-swipe-surface.
const CONTROLS = "input, select, textarea, [data-no-swipe], button:not([data-swipe-surface])";

function reducedMotion(): boolean {
  try {
    return window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
  } catch {
    return false;
  }
}

/**
 * Swipe-to-change-track on the elements matching `surface` inside the
 * element the returned ref is given; they follow the finger and spring back.
 */
export function useTrackSwipe(surface: string, actions: { next: () => void; prev: () => void }): (el: HTMLElement | null) => void {
  const [el, setEl] = useState<HTMLElement | null>(null);
  // Read at the gesture's end, so a re-render never re-wires the listeners.
  const latest = useRef(actions);
  latest.current = actions;
  useEffect(() => {
    if (!el) return;
    let start: { id: number; x: number; y: number } | null = null;
    let dragging = false;
    let moved: HTMLElement[] = [];
    let swallowUntil = 0;

    const place = (dx: number, animate: boolean) => {
      const still = reducedMotion();
      for (const m of moved) {
        m.style.transition = animate && !still ? "transform .35s cubic-bezier(.2,.9,.3,1.15)" : "none";
        m.style.transform = dx && !still ? `translateX(${dx}px)` : "";
      }
    };
    const reset = () => {
      if (dragging) place(0, true);
      start = null;
      dragging = false;
    };
    const down = (e: PointerEvent) => {
      // A new press: the click that ended the last drag has come (or never will).
      swallowUntil = 0;
      if (!e.isPrimary || (e.pointerType === "mouse" && e.button !== 0)) return;
      const target = e.target as Element | null;
      if (!target?.closest(surface) || target.closest(CONTROLS)) return;
      start = { id: e.pointerId, x: e.clientX, y: e.clientY };
      dragging = false;
      moved = [...el.querySelectorAll<HTMLElement>(surface)];
    };
    const move = (e: PointerEvent) => {
      if (!start || e.pointerId !== start.id) return;
      const dx = e.clientX - start.x;
      const dy = e.clientY - start.y;
      if (!dragging && isHorizontalDrag(dx, dy)) dragging = true;
      if (dragging) place(followOffset(dx), false);
    };
    const up = (e: PointerEvent) => {
      if (!start || e.pointerId !== start.id) return;
      const action = swipeAction(e.clientX - start.x, e.clientY - start.y);
      // A drag that went sideways is never also a tap on the cover or the title. Only
      // one that ends on the area can be followed by a click there; ended
      // elsewhere, nothing is swallowed, so the next real tap goes through.
      const over = document.elementFromPoint?.(e.clientX, e.clientY);
      const onSurface = over ? el.contains(over) && !!over.closest(surface) : true;
      swallowUntil = (dragging || action) && onSurface ? Date.now() + 400 : 0;
      reset();
      if (action === "next") latest.current.next();
      else if (action === "prev") latest.current.prev();
    };
    const cancel = (e: PointerEvent) => {
      if (start && e.pointerId === start.id) reset();
    };
    const click = (e: MouseEvent) => {
      if (Date.now() < swallowUntil) {
        swallowUntil = 0;
        e.preventDefault();
        e.stopPropagation();
      }
    };
    el.addEventListener("pointerdown", down);
    // On the window: a finger that leaves the area still ends the gesture.
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", cancel);
    el.addEventListener("click", click, true);
    return () => {
      el.removeEventListener("pointerdown", down);
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", cancel);
      el.removeEventListener("click", click, true);
      for (const m of moved) {
        m.style.transform = "";
        m.style.transition = "";
      }
    };
  }, [el, surface]);
  return setEl;
}
