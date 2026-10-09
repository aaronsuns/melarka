import { memo, useCallback, useEffect, useRef, useState } from "react";
import { api, ApiError } from "../api/client";
import type { Lyrics } from "../api/types";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { activeLine, clampOffset, forgetLyrics, formatOffset, getLyrics, onLyrics, setLyrics } from "./lyricsCache";
import { usePlayer, usePlayerProgress } from "./PlayerProvider";

type Line = { t_ms: number; text: string };

// The current track's lyrics, shared by the mini player and Now Playing (one
// fetch per track; found lyrics are cached for the session). null while the
// track's lyrics are still loading. reload() asks again — e.g. after a miss,
// when the server's background prefetch may have found them meanwhile, or
// after an admin picked other lyrics. The previous answer for the same track
// stays up until the new one lands, so a reload doesn't flicker.
export function useLyrics(trackId: number | undefined): { lyrics: Lyrics | null; reload: () => void } {
  const [state, setState] = useState<{ id: number; lyrics: Lyrics } | null>(null);
  const [nonce, setNonce] = useState(0);
  useEffect(() => {
    if (trackId === undefined) return;
    let live = true;
    getLyrics(trackId)
      .then((lyrics) => live && setState({ id: trackId, lyrics }))
      .catch(() => live && setState({ id: trackId, lyrics: { found: false, synced: false } }));
    // A changed offset or another version (setLyrics) shows at once.
    const off = onLyrics((id, lyrics) => live && id === trackId && setState({ id, lyrics }));
    return () => {
      live = false;
      off();
    };
  }, [trackId, nonce]);
  const reload = useCallback(() => setNonce((n) => n + 1), []);
  return { lyrics: trackId !== undefined && state?.id === trackId ? state.lyrics : null, reload };
}

// Synced lines worth following, or null.
export function syncedLines(l: Lyrics | null): Line[] | null {
  return l?.found && l.synced && l.lines?.length ? l.lines : null;
}

// Whether there is anything to read: synced lines or plain text.
export function hasLyrics(l: Lyrics): boolean {
  return !!(l.found && !l.instrumental && (syncedLines(l) || (l.text ?? "").trim()));
}

const STEP_MS = 500;
const SAVE_DELAY_MS = 800;
const LONG_PRESS_MS = 500;
const NOTICE_MS = 3000;
const UNDO_MS = 10000;

type Notice = { text: string; undo?: () => void };

// Someone replaced or emptied these lyrics meanwhile (409 lyrics_changed /
// no_lyrics): show what the server has now.
function isStale(e: unknown): boolean {
  return e instanceof ApiError && e.status === 409 && (e.code === "lyrics_changed" || e.code === "no_lyrics");
}

// The lyrics of the track playing now. For synced lyrics: −0.5s / +0.5s and
// "align to this line" (long-press, or right-click, a line) shift them for
// everyone; anyone may also report them wrong. A shift shows everywhere at
// once (the lyrics cache tells every holder) and is saved debounced; an
// alignment is saved at once. Every change names the lyrics it was made on
// (their id); an alignment, a reset and a report can be taken back for a
// while (撤销). onEmptied: "wrong lyrics" left none.
export function LyricsView({ lyrics: l, trackId, onEmptied }: { lyrics: Lyrics | null; trackId?: number; onEmptied?: () => void }) {
  const t = useT();
  const [notice, setNotice] = useState<Notice | null>(null);
  const [confirmWrong, setConfirmWrong] = useState(false);
  const [busy, setBusy] = useState(false);
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pending = useRef<{ id: number; ms: number; lyricsId: number } | null>(null);
  const noticeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const say = useCallback((text: string, undo?: () => void) => {
    setNotice({ text, undo });
    if (noticeTimer.current) clearTimeout(noticeTimer.current);
    noticeTimer.current = setTimeout(() => setNotice(null), undo ? UNDO_MS : NOTICE_MS);
  }, []);
  const refetch = useCallback(
    (id: number) => {
      forgetLyrics(id);
      getLyrics(id)
        .then((fresh) => setLyrics(id, fresh))
        .catch(() => {});
      say(t("lyrics.changed"));
    },
    [say, t],
  );
  const fail = useCallback(
    (id: number, e: unknown) => {
      if (isStale(e)) refetch(id);
      else say(errorMessage(e, "common.actionFailed"));
    },
    [refetch, say],
  );
  const flush = useCallback(() => {
    if (saveTimer.current) clearTimeout(saveTimer.current);
    saveTimer.current = null;
    const p = pending.current;
    pending.current = null;
    if (p) api.setLyricsOffset(p.id, p.ms, p.lyricsId).catch((e) => fail(p.id, e));
  }, [fail]);
  // Leaving (another track, closing Now Playing) still saves a pending shift.
  useEffect(
    () => () => {
      flush();
      if (noticeTimer.current) clearTimeout(noticeTimer.current);
    },
    [flush],
  );

  const offset = l?.offset_ms ?? 0;
  // Latest lyrics for callbacks that run later (undo, a timer).
  const latest = useRef(l);
  latest.current = l;
  const shift = useCallback(
    (ms: number, now: boolean) => {
      const cur = latest.current;
      if (trackId === undefined || !cur?.found) return;
      const next = clampOffset(Math.round(ms));
      setLyrics(trackId, { ...cur, offset_ms: next });
      pending.current = { id: trackId, ms: next, lyricsId: cur.id ?? 0 };
      if (saveTimer.current) clearTimeout(saveTimer.current);
      if (now) flush();
      else saveTimer.current = setTimeout(flush, SAVE_DELAY_MS);
    },
    [trackId, flush],
  );
  // An alignment or reset that can be taken back: the previous shift returns.
  const undoable = useCallback(
    (ms: number, now: boolean, text: string) => {
      const before = latest.current?.offset_ms ?? 0;
      const lyricsId = latest.current?.id;
      shift(ms, now);
      say(text, () => {
        if (latest.current?.id !== lyricsId) return;
        shift(before, true);
        setNotice(null);
      });
    },
    [shift, say],
  );
  const align = useCallback(
    (lineMs: number, positionS: number) => undoable(Math.round((positionS * 1000 - lineMs) / 100) * 100, true, t("lyrics.aligned")),
    [undoable, t],
  );

  async function reportWrong() {
    if (trackId === undefined || busy || !l?.found) return;
    setBusy(true);
    setConfirmWrong(false);
    pending.current = null; // a shift of the reported lyrics no longer applies
    if (saveTimer.current) clearTimeout(saveTimer.current);
    try {
      const { report_id: report, ...next } = await api.reportWrongLyrics(trackId, l.id ?? 0);
      setLyrics(trackId, next);
      if (!next.found) onEmptied?.();
      say(t(next.found ? "lyrics.wrongSwitched" : "lyrics.wrongFlagged"), () => {
        setNotice(null);
        api.undoWrongLyrics(trackId, report).then(
          (back) => {
            setLyrics(trackId, back);
            say(t("lyrics.undone"));
          },
          (e) => fail(trackId, e),
        );
      });
    } catch (e) {
      fail(trackId, e);
    } finally {
      setBusy(false);
    }
  }

  let body;
  const synced = !!(l?.found && l.synced && l.lines?.length);
  if (!l) body = <p className="muted lyrics-note">{t("lyrics.loading")}</p>;
  else if (l.instrumental) body = <p className="muted lyrics-note">{t("lyrics.instrumental")}</p>;
  else if (!l.found) body = <p className="muted lyrics-note">{t("lyrics.none")}</p>;
  else if (synced) body = <SyncedLyrics lines={l.lines!} offset={offset} onAlign={trackId === undefined ? undefined : align} />;
  else body = <div className="lyrics-plain">{(l.text ?? "").split("\n").map((line, i) => <p key={i}>{line || " "}</p>)}</div>;
  const tools = trackId !== undefined && l?.found && !l.instrumental;
  return (
    <>
      <div className="lyrics">{body}</div>
      {(tools || notice) && (
        <div className="lyrics-tools small">
          {tools && confirmWrong ? (
            <>
              <span>{t("lyrics.wrongConfirm")}</span>
              <button className="secondary danger" onClick={reportWrong}>{t("lyrics.wrongYes")}</button>
              <button className="secondary" onClick={() => setConfirmWrong(false)}>{t("lyrics.keep")}</button>
            </>
          ) : tools ? (
            <>
              {synced && (
                <span className="lyrics-offset-group">
                  <button className="secondary" aria-label={t("lyrics.earlier")} onClick={() => shift(offset - STEP_MS, false)}>{"−0.5s"}</button>
                  <span className="lyrics-offset" aria-label={t("lyrics.offset", { offset: formatOffset(offset) })}>{formatOffset(offset)}</span>
                  <button className="secondary" aria-label={t("lyrics.later")} onClick={() => shift(offset + STEP_MS, false)}>+0.5s</button>
                  {offset !== 0 && <button className="secondary" onClick={() => undoable(0, false, t("lyrics.offsetResetDone"))}>{t("lyrics.offsetReset")}</button>}
                </span>
              )}
              <button className="secondary" disabled={busy} onClick={() => setConfirmWrong(true)}>{t("lyrics.wrong")}</button>
            </>
          ) : null}
          {notice && (
            <span className="lyrics-notice" role="status">
              {notice.text}
              {notice.undo && <button className="secondary lyrics-undo" onClick={notice.undo}>{t("lyrics.undo")}</button>}
            </span>
          )}
        </div>
      )}
    </>
  );
}

// Only this part follows the playback position; the line list itself
// re-renders only when the active line changes.
function SyncedLyrics({ lines, offset, onAlign }: { lines: Line[]; offset: number; onAlign?: (lineMs: number, positionS: number) => void }) {
  const { seek } = usePlayer();
  const { position } = usePlayerProgress();
  const pos = useRef(position);
  pos.current = position;
  const getPosition = useCallback(() => pos.current, []);
  return <SyncedLines lines={lines} active={activeLine(lines, position, offset)} offset={offset} seek={seek} onAlign={onAlign} getPosition={getPosition} />;
}

const SyncedLines = memo(function SyncedLines({
  lines,
  active,
  offset,
  seek,
  onAlign,
  getPosition,
}: {
  lines: Line[];
  active: number;
  offset: number;
  seek: (s: number) => void;
  onAlign?: (lineMs: number, positionS: number) => void;
  getPosition: () => number;
}) {
  const t = useT();
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  // A press held LONG_PRESS_MS aligns the line (touch or mouse) to where the
  // song was when the press began; the click that ends it is then
  // swallowed, so only a short tap seeks.
  const press = useRef<{ timer: ReturnType<typeof setTimeout>; fired: boolean } | null>(null);
  const swallowClick = useRef(false);
  const [flash, setFlash] = useState(-1);
  const flashTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    if (active >= 0) refs.current[active]?.scrollIntoView?.({ block: "center", behavior: "smooth" });
  }, [active]);
  useEffect(
    () => () => {
      if (press.current) clearTimeout(press.current.timer);
      if (flashTimer.current) clearTimeout(flashTimer.current);
    },
    [],
  );
  function doAlign(i: number, positionS: number) {
    if (!onAlign) return;
    onAlign(lines[i].t_ms, positionS);
    setFlash(i);
    if (flashTimer.current) clearTimeout(flashTimer.current);
    flashTimer.current = setTimeout(() => setFlash(-1), 1200);
  }
  function down(i: number) {
    swallowClick.current = false; // a long press released elsewhere never got its click
    if (!onAlign) return;
    if (press.current) clearTimeout(press.current.timer);
    const at = getPosition();
    const p = {
      fired: false,
      timer: setTimeout(() => {
        p.fired = true;
        swallowClick.current = true;
        doAlign(i, at);
      }, LONG_PRESS_MS),
    };
    press.current = p;
  }
  function up() {
    if (press.current) clearTimeout(press.current.timer);
    press.current = null;
  }
  return (
    <ol className="lyrics-synced">
      {lines.map((line, i) => (
        <li key={i}>
          <button
            ref={(el) => {
              refs.current[i] = el;
            }}
            className={[i === active ? "active" : "", i === flash ? "aligned" : ""].filter(Boolean).join(" ") || undefined}
            aria-current={i === active ? "true" : undefined}
            title={onAlign ? t("lyrics.align") : undefined}
            onPointerDown={() => down(i)}
            onPointerUp={up}
            onPointerLeave={up}
            onPointerCancel={up}
            onContextMenu={(e) => {
              if (!onAlign) return;
              e.preventDefault();
              if (press.current?.fired) return; // a touch long-press already aligned
              up();
              doAlign(i, getPosition());
            }}
            onClick={() => {
              if (swallowClick.current) {
                swallowClick.current = false;
                return;
              }
              seek(Math.max(0, (line.t_ms + offset) / 1000));
            }}
          >
            {line.text || " "}
          </button>
        </li>
      ))}
    </ol>
  );
});

// Under the cover: the current line and the next one. Like SyncedLyrics, only
// this follows the position; the lines re-render when the active one changes.
export function LyricStrip({ lines, offset = 0 }: { lines: Line[]; offset?: number }) {
  const { position } = usePlayerProgress();
  return <StripLines lines={lines} active={activeLine(lines, position, offset)} />;
}

const StripLines = memo(function StripLines({ lines, active }: { lines: Line[]; active: number }) {
  const cur = active >= 0 ? lines[active].text : "";
  const next = lines[active + 1]?.text ?? "";
  return (
    <div className="now-lyric-strip">
      <p className={cur ? "active ellipsis" : "ellipsis"} aria-current={cur ? "true" : undefined}>{cur || " "}</p>
      <p className="muted ellipsis">{next || " "}</p>
    </div>
  );
});

// The mini player's second line: the current synced line, else the fallback
// (the artist).
export function CurrentLine({ lines, offset = 0, fallback }: { lines: Line[] | null; offset?: number; fallback: string }) {
  const { position } = usePlayerProgress();
  const text = lines ? (lines[activeLine(lines, position, offset)]?.text ?? "").trim() : "";
  return <>{text || fallback}</>;
}
