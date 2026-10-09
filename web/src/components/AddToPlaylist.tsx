import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { Playlist, Track } from "../api/types";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";

export function AddToPlaylist({ track, onClose }: { track: Track; onClose: () => void }) {
  const t = useT();
  const [lists, setLists] = useState<Playlist[] | null>(null);
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.playlists().then(setLists).catch((e) => setError(e.message));
  }, []);

  async function addTo(p: Playlist) {
    if (busy) return;
    setBusy(true);
    try {
      const { tracks } = await api.playlist(p.id);
      if (!tracks.some((x) => x.id === track.id)) await api.updatePlaylist(p.id, { track_ids: [...tracks.map((x) => x.id), track.id] });
      onClose();
    } catch (e) {
      setError(errorMessage(e, "addToPlaylist.addFailed"));
      setBusy(false);
    }
  }

  async function create() {
    if (busy) return;
    setBusy(true);
    try {
      await api.createPlaylist(name.trim(), [track.id]);
      onClose();
    } catch (e) {
      setError(errorMessage(e, "addToPlaylist.createFailed"));
      setBusy(false);
    }
  }

  return (
    <div className="sheet-backdrop" onClick={onClose}>
      <div className="sheet" role="dialog" aria-label={t("addToPlaylist.ariaLabel")} onClick={(e) => e.stopPropagation()}>
        <h2 className="ellipsis">{t("addToPlaylist.heading", { title: track.title })}</h2>
        {error && <p className="error" role="alert">{error}</p>}
        {lists?.map((p) => (
          <button key={p.id} className="sheet-row" disabled={busy} onClick={() => addTo(p)}>
            <span className="ellipsis">{p.name}</span><span className="muted">{t("common.songCount", { count: p.track_count })}</span>
          </button>
        ))}
        <div className="sheet-new">
          <input placeholder={t("addToPlaylist.newPlaceholder")} value={name} onChange={(e) => setName(e.target.value)} />
          <button className="primary" disabled={busy || !name.trim()} onClick={create}>{t("addToPlaylist.create")}</button>
        </div>
        <button className="sheet-cancel" onClick={onClose}>{t("common.cancel")}</button>
      </div>
    </div>
  );
}
