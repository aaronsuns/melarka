import { useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { useT } from "../i18n/i18n";
import { swipeRemoves, targetIndex, type DragState } from "./dragReorder";
import { usePlayer } from "./PlayerProvider";

export interface QueueRowItem {
  key: string;
  title: string;
  sub: ReactNode;
  current: boolean;
}

// How far a finger must travel before a row decides between a swipe and a scroll.
const SLOP = 10;

// Stable React keys for queue entries: a moved row keeps its DOM node (and
// keyboard focus, and a drag in progress). By `keyOf`, else by object
// identity; an entry twice in a queue (a requeued track) gets a second key
// from its occurrence.
const ids = new WeakMap<object, number>();
let nextId = 0;
export function entryKeys<T extends object>(list: readonly T[], keyOf?: (x: T) => string): string[] {
  const seen = new Map<string, number>();
  return list.map((o) => {
    let id = keyOf?.(o);
    if (id === undefined) {
      let n = ids.get(o);
      if (n === undefined) ids.set(o, (n = ++nextId));
      id = String(n);
    }
    const k = seen.get(id) ?? 0;
    seen.set(id, k + 1);
    return `${id}.${k}`;
  });
}

interface Swipe {
  pointerId: number;
  x: number;
  y: number;
  width: number;
  swiping: boolean;
}

/**
 * Queue rows that reorder and remove, shared by the music and episode sheets.
 * The handle on the right drags (pointer events, so mouse, touch and pen) and
 * answers ArrowUp/ArrowDown; a row swipes left to go, or on a desktop shows ✕.
 * The current row never goes. Indexes are row numbers.
 *
 * `fixedCurrent` (music, whose sheet starts at the current track): the current
 * row is first and stays there — no handle, and nothing drops above it.
 * Without it (episodes, the whole list with the current one in place) the
 * current row moves like any other.
 *
 * The list doesn't scroll by itself during a drag (the handle takes the
 * touch): a long move is made one screenful at a time.
 */
export function QueueRows({ items, label, id, onJump, onMove, onRemove, footer, fixedCurrent = false }: {
  items: QueueRowItem[];
  fixedCurrent?: boolean;
  label: string;
  id?: string;
  onJump(row: number): void;
  onMove(from: number, to: number): void;
  onRemove(row: number): void;
  footer?: ReactNode;
}) {
  const t = useT();
  // A drag follows its entry by key, not by row: the queue may move on (the
  // current track ends) or grow (a refill) while a finger holds it.
  const [drag, setDrag] = useState<{ key: string; startY: number; rowH: number; dy: number } | null>(null);
  const [swipe, setSwipe] = useState<{ row: number; dx: number } | null>(null);
  const dragRef = useRef<{ key: string; startY: number; rowH: number } | null>(null);
  const minRow = fixedCurrent ? 1 : 0;
  // Where a drag of the entry with `key` is now, and the row it would land on at `y`.
  const resolve = (d: { key: string; startY: number; rowH: number }, y: number): { from: number; to: number } | null => {
    const from = items.findIndex((it) => it.key === d.key);
    if (from < minRow) return null; // gone, or now the fixed current row
    const s: DragState = { from, startY: d.startY, rowH: d.rowH, count: items.length };
    return { from, to: Math.max(minRow, targetIndex(s, y)) };
  };
  const swipeRef = useRef<Swipe | null>(null);
  // The click that ends a swipe must not also play the row.
  const swallowClick = useRef(false);

  const capture = (e: ReactPointerEvent<HTMLElement>) => {
    try {
      e.currentTarget.setPointerCapture?.(e.pointerId);
    } catch {
      // the pointer is already gone
    }
  };

  function handleDown(e: ReactPointerEvent<HTMLButtonElement>, row: number) {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation(); // the row's swipe never starts from its handle
    const li = e.currentTarget.closest("li");
    const d = { key: items[row].key, startY: e.clientY, rowH: li?.getBoundingClientRect().height ?? 0 };
    dragRef.current = d;
    capture(e);
    setDrag({ ...d, dy: 0 });
  }
  function handleMove(e: ReactPointerEvent) {
    const d = dragRef.current;
    if (!d) return;
    e.preventDefault();
    setDrag({ ...d, dy: e.clientY - d.startY });
  }
  function handleUp(e: ReactPointerEvent) {
    const d = dragRef.current;
    if (!d) return;
    dragRef.current = null;
    setDrag(null);
    const r = resolve(d, e.clientY);
    if (r && r.to !== r.from) onMove(r.from, r.to);
  }
  function handleCancel() {
    dragRef.current = null;
    setDrag(null);
  }
  function handleKey(e: KeyboardEvent, row: number) {
    const to = e.key === "ArrowUp" ? row - 1 : e.key === "ArrowDown" ? row + 1 : null;
    if (to === null) return;
    e.preventDefault();
    if (to >= minRow && to < items.length) onMove(row, to);
  }

  function rowDown(e: ReactPointerEvent<HTMLLIElement>, row: number) {
    swallowClick.current = false;
    if (e.button !== 0 || items[row].current || dragRef.current) return;
    swipeRef.current = { pointerId: e.pointerId, x: e.clientX, y: e.clientY, width: e.currentTarget.getBoundingClientRect().width, swiping: false };
  }
  function rowMove(e: ReactPointerEvent<HTMLLIElement>, row: number) {
    const sw = swipeRef.current;
    if (!sw || sw.pointerId !== e.pointerId) return;
    const dx = e.clientX - sw.x;
    const dy = e.clientY - sw.y;
    if (!sw.swiping) {
      if (Math.abs(dy) > SLOP && Math.abs(dy) >= Math.abs(dx)) {
        swipeRef.current = null; // a scroll: the browser has it
        return;
      }
      if (Math.abs(dx) <= SLOP) return;
      sw.swiping = true;
      capture(e);
    }
    setSwipe({ row, dx: Math.min(0, dx) });
  }
  function rowUp(e: ReactPointerEvent<HTMLLIElement>, row: number) {
    const sw = swipeRef.current;
    swipeRef.current = null;
    if (!sw || !sw.swiping) return;
    swallowClick.current = true;
    setSwipe(null);
    if (swipeRemoves(e.clientX - sw.x, sw.width)) onRemove(row);
  }
  function rowCancel() {
    swipeRef.current = null;
    setSwipe(null);
  }

  function rowStyle(row: number): CSSProperties | undefined {
    if (swipe && swipe.row === row) return { transform: `translateX(${swipe.dx}px)` };
    const r = drag && resolve(drag, drag.startY + drag.dy);
    if (!drag || !r) return undefined;
    const { from, to } = r;
    if (row === from) return { transform: `translateY(${drag.dy}px)`, zIndex: 1, position: "relative" };
    if (from < to && row > from && row <= to) return { transform: `translateY(${-drag.rowH}px)` };
    if (from > to && row >= to && row < from) return { transform: `translateY(${drag.rowH}px)` };
    return undefined;
  }

  return (
    <ul id={id} className={`now-queue queue-list${drag ? " dragging" : ""}`} aria-label={label}>
      {items.map((it, row) => (
        <li
          key={it.key}
          className={`queue-row${drag?.key === it.key ? " lifted" : ""}`}
          style={rowStyle(row)}
          onPointerDown={(e) => rowDown(e, row)}
          onPointerMove={(e) => rowMove(e, row)}
          onPointerUp={(e) => rowUp(e, row)}
          onPointerCancel={rowCancel}
        >
          <button
            className="row queue-main"
            aria-current={it.current ? true : undefined}
            onClick={() => {
              if (swallowClick.current) {
                swallowClick.current = false;
                return;
              }
              if (!it.current) onJump(row);
            }}
          >
            <span className="ellipsis queue-title">{it.title}</span>
            <span className="muted small ellipsis">
              {it.current && <><span className="queue-now">{t("queue.nowPlaying")}</span> · </>}
              {it.sub}
            </span>
          </button>
          {!it.current && (
            <button className="icon queue-remove" aria-label={t("queue.remove", { title: it.title })} onClick={() => onRemove(row)}>✕</button>
          )}
          {!(fixedCurrent && it.current) && <button
            className="icon queue-handle"
            aria-label={t("queue.drag", { title: it.title })}
            aria-keyshortcuts="ArrowUp ArrowDown"
            onPointerDown={(e) => handleDown(e, row)}
            onPointerMove={handleMove}
            onPointerUp={handleUp}
            onPointerCancel={handleCancel}
            onKeyDown={(e) => handleKey(e, row)}
          >
            <svg viewBox="0 0 24 24" width={20} height={20} aria-hidden="true" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round">
              <path d="M5 8h14M5 12h14M5 16h14" />
            </svg>
          </button>}
        </li>
      ))}
      {footer}
    </ul>
  );
}

/** Music's queue: the current track first, then what plays next. */
export function QueueSheet() {
  const t = useT();
  const p = usePlayer();
  const { tracks, index } = p.queue;
  const shown = tracks.length === 0 ? [] : tracks.slice(index);
  const keys = entryKeys(shown);
  const items = shown.map((q, i) => ({ key: keys[i], title: q.title, sub: q.artist || t("common.unknownArtist"), current: i === 0 }));
  return (
    <QueueRows
      items={items}
      label={t("now.queue")}
      fixedCurrent
      onJump={(row) => p.jump(index + row)}
      onMove={(from, to) => p.move(index + from, index + to)}
      onRemove={(row) => p.removeAt(index + row)}
      footer={shown.length <= 1 && <li className="muted">{p.modes.repeat === "all" ? t("now.repeatAllEnd") : t("now.radioEnd")}</li>}
    />
  );
}
