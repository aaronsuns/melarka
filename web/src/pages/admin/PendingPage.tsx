import { useState } from "react";
import { api } from "../../api/client";
import type { Track } from "../../api/types";
import { useInfinite } from "../../components/useInfinite";
import { Cover, coverUrl } from "../../components/Cover";
import { errorMessage } from "../../i18n/errors";
import { useT } from "../../i18n/i18n";
import { usePlayer } from "../../player/PlayerProvider";

export default function PendingPage() {
  const t = useT();
  const player = usePlayer();
  const s = useInfinite<Track>((cursor) => api.tracks({ status: "pending", sort: "added", cursor }), []);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [confirmDeleteAll, setConfirmDeleteAll] = useState(false);
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<{ current: number; total: number } | null>(null);
  const [opError, setOpError] = useState("");

  function toggle(id: number) {
    setSelected((sel) => {
      const next = new Set(sel);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function selectAll() {
    setSelected(new Set(s.items.map((t) => t.id)));
  }

  function playFrom(track: Track) {
    const playable = s.items.filter((x) => !x.broken);
    const idx = playable.findIndex((x) => x.id === track.id);
    if (idx < 0) return;
    player.playList(playable, idx);
  }

  // Shared runner for both bulk actions: processes ids one at a time (in
  // order), stops on the first error and shows it, and removes each row as
  // soon as its own call succeeds.
  async function runBulk(ids: number[], fn: (id: number) => Promise<void>) {
    if (busy || ids.length === 0) return;
    setBusy(true);
    setOpError("");
    for (let i = 0; i < ids.length; i++) {
      setProgress({ current: i + 1, total: ids.length });
      try {
        await fn(ids[i]);
        s.setItems((ts) => ts.filter((t) => t.id !== ids[i]));
        setSelected((sel) => {
          if (!sel.has(ids[i])) return sel;
          const next = new Set(sel);
          next.delete(ids[i]);
          return next;
        });
      } catch (e) {
        setOpError(errorMessage(e, "common.actionFailed"));
        break;
      }
    }
    setProgress(null);
    setBusy(false);
  }

  function keepSelected() {
    void runBulk([...selected], (id) => api.setStatus(id, "kept"));
  }

  function askDeleteSelected() {
    if (selected.size === 0) return;
    setConfirmDeleteAll(true);
  }

  function cancelDeleteSelected() {
    setConfirmDeleteAll(false);
  }

  function confirmDeleteSelected() {
    setConfirmDeleteAll(false);
    void runBulk([...selected], async (id) => {
      await api.trashTrack(id);
      // Mirrors TrackList's delete: a trashed track still playing (or
      // queued) must drop out of the player too, not just the list.
      player.remove(id);
    });
  }

  const allSelected = s.items.length > 0 && selected.size === s.items.length;

  return (
    <>
      <div className="actions">
        <button className="secondary" disabled={busy || s.items.length === 0} onClick={selectAll}>
          {allSelected ? t("admin.pending.allSelected") : t("admin.pending.selectAll")}
        </button>
        <button className="secondary" disabled={busy || selected.size === 0} onClick={keepSelected}>{t("admin.pending.keepSelected")}</button>
        {confirmDeleteAll ? (
          <>
            <button className="danger" disabled={busy} onClick={confirmDeleteSelected}>{t("admin.pending.confirmDeleteSelected")}</button>
            <button className="secondary" disabled={busy} onClick={cancelDeleteSelected}>{t("admin.pending.cancelDeleteSelected")}</button>
          </>
        ) : (
          <button className="secondary" disabled={busy || selected.size === 0} onClick={askDeleteSelected}>{t("admin.pending.deleteSelected")}</button>
        )}
      </div>
      {progress && <p className="muted">{t("admin.pending.progress", { current: progress.current, total: progress.total })}</p>}
      {opError && <p className="error">{opError}</p>}
      {s.error && <p className="error">{s.error}</p>}
      {s.done && s.items.length === 0 && <p className="muted">{t("admin.pending.empty")}</p>}
      <ul className="rows">
        {s.items.map((track) => (
          <li key={track.id} className="check-row">
            <label className="check-tap">
              <input
                type="checkbox"
                aria-label={t("admin.pending.selectTrack", { title: track.title })}
                checked={selected.has(track.id)}
                disabled={busy}
                onChange={() => toggle(track.id)}
              />
            </label>
            <button className="track-main" disabled={track.broken} aria-label={t("admin.pending.playTrack", { title: track.title })} onClick={() => playFrom(track)}>
              <Cover seed={track.album || track.title} label={track.title} size={44} src={coverUrl("track", track.id)} />
              <span className="track-text">
                <span className="ellipsis track-title">{track.title}</span>
                <span className="ellipsis muted small">{track.artist || t("common.unknownArtist")}</span>
              </span>
            </button>
          </li>
        ))}
      </ul>
      {!s.done && (
        <div ref={s.sentinel} className="more">
          <button onClick={s.loadMore} disabled={s.loading}>{s.loading ? t("common.loading") : t("common.loadMore")}</button>
        </div>
      )}
    </>
  );
}
