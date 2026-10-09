import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { LyricsCandidate, LyricsRejected, Track } from "../api/types";
import { formatDateTime } from "../format";
import { errorMessage } from "../i18n/errors";
import { has, t as translate, useT } from "../i18n/i18n";
import { forgetLyrics } from "../player/lyricsCache";

export function sourceLabel(source: string): string {
  const key = `lyrics.source.${source}`;
  return has(key) ? translate(key) : source;
}

// Admin: pick which of a track's stored lyrics is shown, or search the
// providers again — with an edited title/artist when the track's own (say a
// YouTube video title) finds nothing, or broadly (any singer, looser title
// and duration: may be wrong, the admin checks while it plays). A wrong
// candidate can be deleted (confirmed in its row), or the song marked as
// having no lyrics (also confirmed in place). Rejected lyrics (reported wrong
// by a listener, or deleted here) are listed with who and when; 恢复 lifts a
// rejection, so the words come back as a candidate. onChanged runs after every change, so an open lyrics
// view reloads.
export function LyricsPicker({ track, onClose, onChanged }: { track: Track; onClose: () => void; onChanged?: () => void }) {
  const t = useT();
  const [list, setList] = useState<LyricsCandidate[] | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [title, setTitle] = useState(track.title);
  const [artist, setArtist] = useState(track.artist ?? "");
  const [confirming, setConfirming] = useState<number | null>(null);
  const [confirmingNone, setConfirmingNone] = useState(false);
  const [rejected, setRejected] = useState<LyricsRejected[]>([]);

  // An older server (or a failed read) simply shows no rejected section.
  const loadRejected = () =>
    api.lyricsRejected(track.id).then(
      (r) => setRejected(Array.isArray(r) ? r : []),
      () => setRejected([]),
    );
  useEffect(() => {
    api.lyricsCandidates(track.id).then(setList).catch((e) => setError(errorMessage(e, "common.actionFailed")));
    void loadRejected();
  }, [track.id]);

  async function run(fn: () => Promise<void>) {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await fn();
    } catch (e) {
      setError(errorMessage(e, "common.actionFailed"));
    } finally {
      setBusy(false);
    }
  }

  const changed = () => {
    forgetLyrics(track.id);
    onChanged?.();
  };

  const pick = (c: LyricsCandidate) =>
    run(async () => {
      await api.selectLyrics(track.id, c.id);
      changed();
      onClose();
    });

  const none = () =>
    run(async () => {
      await api.noLyrics(track.id);
      changed();
      onClose();
    });

  const remove = (c: LyricsCandidate) =>
    run(async () => {
      await api.deleteLyricsCandidate(track.id, c.id);
      setConfirming(null);
      changed();
      setList(await api.lyricsCandidates(track.id));
      await loadRejected();
    });

  const restore = (r: LyricsRejected) =>
    run(async () => {
      await api.unrejectLyrics(track.id, r.id);
      changed();
      setList(await api.lyricsCandidates(track.id));
      await loadRejected();
    });

  // Only the fields the admin changed are sent; untouched ones are left to
  // the server, which cleans them.
  const search = (broad: boolean) =>
    run(async () => {
      const o: { title?: string; artist?: string; broad?: boolean } = broad ? { broad: true } : {};
      if (title.trim() !== track.title.trim()) o.title = title.trim();
      if (artist.trim() !== (track.artist ?? "").trim()) o.artist = artist.trim();
      await api.refreshLyrics(track.id, Object.keys(o).length ? o : undefined);
      changed();
      setList(await api.lyricsCandidates(track.id));
    });

  return (
    <div className="sheet-backdrop" onClick={onClose}>
      <div className="sheet" role="dialog" aria-label={t("lyrics.change")} onClick={(e) => e.stopPropagation()}>
        <h2 className="ellipsis">{t("lyrics.change")} · {track.title}</h2>
        <label className="field">{t("lyrics.titleField")}<input value={title} onChange={(e) => setTitle(e.target.value)} /></label>
        <label className="field">{t("lyrics.artistField")}<input value={artist} onChange={(e) => setArtist(e.target.value)} /></label>
        <div className="lyrics-search">
          <button className="secondary" disabled={busy || !title.trim()} onClick={() => void search(false)}>{t("lyrics.search")}</button>
          <button className="secondary" disabled={busy || !title.trim()} onClick={() => void search(true)}>{t("lyrics.broadSearch")}</button>
        </div>
        {error && <p className="error" role="alert">{error}</p>}
        {list === null && !error && <p className="muted">{t("common.loading")}</p>}
        {list?.length === 0 && <p className="muted">{t("lyrics.noCandidates")}</p>}
        {list?.map((c) => (
          <div key={c.id} className={`lyrics-cand${c.selected ? " selected" : ""}`}>
            <button className="lyrics-cand-pick" aria-pressed={c.selected} disabled={busy} onClick={() => void pick(c)}>
              <span className="lyrics-cand-head">
                <span>{c.selected ? "✓ " : ""}{sourceLabel(c.source)}</span>
                {c.synced && <span className="badge">{t("lyrics.synced")}</span>}
              </span>
              <span className="muted small lyrics-cand-preview">{c.preview}</span>
            </button>
            {confirming === c.id ? (
              <span className="lyrics-cand-confirm">
                <span className="small">{t("lyrics.deleteConfirm")}</span>
                <button className="danger" disabled={busy} onClick={() => void remove(c)}>{t("lyrics.deleteYes")}</button>
                <button className="secondary" disabled={busy} onClick={() => setConfirming(null)}>{t("lyrics.keep")}</button>
              </span>
            ) : (
              <button className="lyrics-cand-delete secondary" disabled={busy} onClick={() => setConfirming(c.id)}>{t("lyrics.delete")}</button>
            )}
          </div>
        ))}
        {rejected.length > 0 && (
          <section className="lyrics-rejected-list">
            <h3>{t("lyrics.rejected")}</h3>
            {rejected.map((r) => (
              <div key={r.id} className="lyrics-cand lyrics-rejected">
                <span className="lyrics-cand-pick">
                  <span className="lyrics-cand-head">
                    <span>{sourceLabel(r.source)}</span>
                    <span className="muted small">
                      {r.reported_by
                        ? t("lyrics.rejectedBy", { name: r.reported_by, date: formatDateTime(r.rejected_at) })
                        : t("lyrics.rejectedByAdmin", { date: formatDateTime(r.rejected_at) })}
                    </span>
                  </span>
                  <span className="muted small lyrics-cand-preview">{r.preview || t("lyrics.noPreview")}</span>
                </span>
                <button className="secondary" disabled={busy} onClick={() => void restore(r)}>{t("lyrics.restore")}</button>
              </div>
            ))}
          </section>
        )}
        {confirmingNone ? (
          <div className="lyrics-none lyrics-cand-confirm">
            <span className="small">{t("lyrics.noneConfirm")}</span>
            <button className="danger" disabled={busy} onClick={() => void none()}>{t("lyrics.noneYes")}</button>
            <button className="secondary" disabled={busy} onClick={() => setConfirmingNone(false)}>{t("lyrics.noneNo")}</button>
          </div>
        ) : (
          <button className="secondary lyrics-none" disabled={busy} onClick={() => setConfirmingNone(true)}>{t("lyrics.noneForSong")}</button>
        )}
        <button className="sheet-cancel" disabled={busy} onClick={onClose}>{t("common.cancel")}</button>
      </div>
    </div>
  );
}
