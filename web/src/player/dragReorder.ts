// Queue sheet gestures as plain arithmetic (no DOM), shared by the music and
// episode sheets: where a dragged row lands, whether a swipe removes, and the
// list after a move.

export interface DragState {
  from: number;
  startY: number;
  rowH: number;
  count: number;
}

/** The row a drag that started on row `from` at `startY` is over at `y`. */
export function targetIndex(s: DragState, y: number): number {
  if (s.count <= 0) return 0;
  const steps = s.rowH > 0 ? Math.round((y - s.startY) / s.rowH) : 0;
  return Math.min(Math.max(s.from + steps, 0), s.count - 1);
}

/** A left swipe past 35% of the row's width removes it. */
export function swipeRemoves(dx: number, width: number): boolean {
  return width > 0 && dx <= -0.35 * width;
}

/**
 * Moves entry `from` to slot `to`; `index` follows the current entry (which may be the one moved).
 * Null when either position is outside the list.
 */
export function moveEntry<T>(list: readonly T[], index: number, from: number, to: number): { list: T[]; index: number } | null {
  if (from < 0 || from >= list.length || to < 0 || to >= list.length) return null;
  const out = [...list];
  const [x] = out.splice(from, 1);
  out.splice(to, 0, x);
  let i = index;
  if (from === index) i = to;
  else if (from < index && to >= index) i = index - 1;
  else if (from > index && to <= index) i = index + 1;
  return { list: out, index: i };
}

/** Removes entry `at`; `index` follows the current entry. Null for the current entry or one outside the list. */
export function removeEntry<T>(list: readonly T[], index: number, at: number): { list: T[]; index: number } | null {
  if (at === index || at < 0 || at >= list.length) return null;
  return { list: list.filter((_, i) => i !== at), index: at < index ? index - 1 : index };
}
