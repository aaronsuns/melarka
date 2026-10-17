import { useRef, useState } from "react";
import { api } from "../api/client";
import type { Lyrics } from "../api/types";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { usePlayer, usePlayerProgress } from "./PlayerProvider";

/** "[mm:ss.xx]" for a time in seconds (LRC). */
export function lrcTime(s: number): string {
  const cs = Math.round(Math.max(0, s) * 100);
  const m = Math.floor(cs / 6000);
  const rest = cs - m * 6000;
  return `[${String(m).padStart(2, "0")}:${String(Math.floor(rest / 100)).padStart(2, "0")}.${String(rest % 100).padStart(2, "0")}]`;
}

export function toLRC(lines: string[], times: number[]): string {
  return lines.map((l, i) => lrcTime(times[i]) + l).join("\n");
}

// Text already timed (an .lrc pasted as is): three lines with a time stamp, as the server counts it.
const TIMED = /^\s*\[\d{1,3}:\d{2}(?:[.:]\d{1,3})?\]/;
const isTimed = (text: string) => text.split("\n").filter((l) => TIMED.test(l)).length >= 3;

/**
 * 制作歌词: lyrics a person makes for the playing song. Paste the words (one
 * line each); then the song plays from the top and a tap marks when each line
 * starts (撤销 takes the last one back, 重来 starts over); 保存 stores them as
 * the song's lyrics for everyone. Timed text (an .lrc) can be saved as it is.
 */
export function LyricsMaker({ trackId, initialText, onClose, onSaved }: {
  trackId: number;
  initialText: string;
  onClose: () => void;
  onSaved: (l: Lyrics) => void;
}) {
  const t = useT();
  const p = usePlayer();
  const { position } = usePlayerProgress();
  const pos = useRef(position);
  pos.current = position;
  const [text, setText] = useState(initialText);
  const [lines, setLines] = useState<string[] | null>(null); // null: still pasting
  const [times, setTimes] = useState<number[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  function start() {
    const ls = text.split("\n").map((l) => l.trim()).filter(Boolean);
    if (!ls.length) return;
    setLines(ls);
    setTimes([]);
    p.seek(0);
    p.play();
  }
  function tap() {
    if (!lines || times.length >= lines.length) return;
    setTimes([...times, pos.current]);
  }
  function undo() {
    if (!times.length) return;
    const rest = times.slice(0, -1);
    setTimes(rest);
    p.seek(Math.max(0, (rest.length ? rest[rest.length - 1] : 0) - 1)); // just before the line to tap again
  }
  async function save(body: string) {
    setBusy(true);
    setError("");
    try {
      onSaved(await api.setOwnLyrics(trackId, body));
      onClose();
    } catch (e) {
      setError(errorMessage(e, "common.actionFailed"));
    } finally {
      setBusy(false);
    }
  }

  const done = lines !== null && times.length === lines.length;
  return (
    <div className="sheet-backdrop" onClick={onClose}>
      <div className="sheet lyrics-maker" role="dialog" aria-label={t("lyrics.make")} onClick={(e) => e.stopPropagation()}>
        <h2>{t("lyrics.make")}</h2>
        {error && <p className="error" role="alert">{error}</p>}
        {lines === null ? (
          <>
            <p className="muted small">{t("lyrics.makePasteHint")}</p>
            <textarea className="lyrics-maker-text" rows={10} value={text} onChange={(e) => setText(e.target.value)} />
            <div className="lyrics-maker-actions">
              <button className="secondary" onClick={onClose}>{t("common.cancel")}</button>
              {isTimed(text) && <button className="secondary" disabled={busy} onClick={() => void save(text)}>{t("lyrics.makeSaveAsIs")}</button>}
              <button className="primary" disabled={!text.trim()} onClick={start}>{t("lyrics.makeStart")}</button>
            </div>
          </>
        ) : (
          <>
            <p className="muted small">{done ? t("lyrics.makeDoneHint") : t("lyrics.makeTapHint")}</p>
            <ol className="lyrics-maker-lines">
              {lines.map((l, i) => (
                <li key={i} className={i === times.length ? "current" : i < times.length ? "timed" : ""}>
                  <span className="lyrics-maker-time">{i < times.length ? lrcTime(times[i]).slice(1, -1) : ""}</span>
                  <span>{l}</span>
                </li>
              ))}
            </ol>
            {!done && (
              <button className="primary lyrics-maker-tap" onClick={tap}>
                {t("lyrics.makeTap")} · {t("lyrics.makeProgress", { n: times.length + 1, total: lines.length })}
              </button>
            )}
            <div className="lyrics-maker-actions">
              <button className="secondary" disabled={!times.length} onClick={undo}>{t("lyrics.undo")}</button>
              <button className="secondary" onClick={start}>{t("lyrics.makeRestart")}</button>
              {done && <button className="primary" disabled={busy} onClick={() => void save(toLRC(lines, times))}>{t("common.save")}</button>}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
