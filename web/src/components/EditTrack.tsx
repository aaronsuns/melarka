import { useState } from "react";
import { api } from "../api/client";
import type { Track } from "../api/types";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";

type Patch = { title?: string; artist?: string; album?: string; year?: number };

export function EditTrack({ track, onClose, onSaved }: { track: Track; onClose: () => void; onSaved: (t: Track) => void }) {
  const t = useT();
  const [title, setTitle] = useState(track.title);
  const [artist, setArtist] = useState(track.artist);
  const [album, setAlbum] = useState(track.album);
  const [year, setYear] = useState(track.year != null ? String(track.year) : "");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  function changes(): Patch {
    const patch: Patch = {};
    if (title !== track.title) patch.title = title;
    if (artist !== track.artist) patch.artist = artist;
    if (album !== track.album) patch.album = album;
    const trimmedYear = year.trim();
    const yearNum = trimmedYear === "" ? 0 : Number(trimmedYear);
    const origYear = track.year ?? 0;
    if (Number.isFinite(yearNum) && yearNum !== origYear) patch.year = yearNum;
    return patch;
  }

  async function save() {
    if (busy) return;
    const patch = changes();
    if (Object.keys(patch).length === 0) {
      onClose();
      return;
    }
    setBusy(true);
    setError("");
    try {
      await api.editTrack(track.id, patch);
      // year:0 means "clear the override" — the track's year sentinel for
      // "no override" is null, not 0, so reflect that in the merged track
      // rather than a fake year-zero.
      const yearPatch = patch.year === undefined ? {} : { year: patch.year === 0 ? null : patch.year };
      onSaved({ ...track, ...patch, ...yearPatch });
    } catch (e) {
      setError(errorMessage(e, "editTrack.saveFailed"));
      setBusy(false);
    }
  }

  return (
    <div className="sheet-backdrop" onClick={onClose}>
      <div className="sheet" role="dialog" aria-label={t("editTrack.ariaLabel")} onClick={(e) => e.stopPropagation()}>
        <h2 className="ellipsis">{t("editTrack.ariaLabel")}</h2>
        {error && <p className="error" role="alert">{error}</p>}
        <label className="field">
          {t("editTrack.title")}
          <input value={title} disabled={busy} onChange={(e) => setTitle(e.target.value)} />
        </label>
        <label className="field">
          {t("editTrack.artist")}
          <input value={artist} disabled={busy} onChange={(e) => setArtist(e.target.value)} />
        </label>
        <label className="field">
          {t("editTrack.album")}
          <input value={album} disabled={busy} onChange={(e) => setAlbum(e.target.value)} />
        </label>
        <label className="field">
          {t("editTrack.year")}
          <input type="number" value={year} disabled={busy} onChange={(e) => setYear(e.target.value)} />
        </label>
        <button className="primary" disabled={busy} onClick={() => void save()}>{t("common.save")}</button>
        <button className="sheet-cancel" disabled={busy} onClick={onClose}>{t("common.cancel")}</button>
      </div>
    </div>
  );
}
