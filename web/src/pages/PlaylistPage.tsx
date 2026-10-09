import { useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router";
import { api, ApiError } from "../api/client";
import type { Playlist, Track } from "../api/types";
import { TrackList } from "../components/TrackList";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { usePlayer } from "../player/PlayerProvider";

export default function PlaylistPage() {
  const t = useT();
  const id = Number(useParams().id);
  const nav = useNavigate();
  const player = usePlayer();
  const [pl, setPl] = useState<Playlist | null>(null);
  const [tracks, setTracks] = useState<Track[]>([]);
  const [error, setError] = useState("");
  const [renaming, setRenaming] = useState(false);
  const [name, setName] = useState("");
  const [savingName, setSavingName] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const tracksRef = useRef<Track[]>([]);
  const renameBusy = useRef(false);
  const deleteBusy = useRef(false);

  useEffect(() => {
    let cancelled = false;
    // Full reset: an armed delete-confirm, an in-progress rename, or a busy
    // flag from the previous playlist must never survive a same-instance
    // navigation to a different :id (PlaylistPage isn't remounted by the
    // router when only the param changes).
    setError("");
    setPl(null);
    setTracks([]);
    setRenaming(false);
    setName("");
    setSavingName(false);
    setConfirm(false);
    setDeleting(false);
    tracksRef.current = [];
    renameBusy.current = false;
    deleteBusy.current = false;
    api.playlist(id)
      .then((r) => {
        if (cancelled) return;
        setPl(r.playlist);
        setName(r.playlist.name);
        setTracks(r.tracks);
        tracksRef.current = r.tracks;
      })
      .catch((e) => {
        if (cancelled) return;
        setError(e instanceof ApiError && e.status === 404 ? t("playlist.notFound") : e.message);
      });
    return () => {
      cancelled = true;
    };
  }, [id, t]);

  async function removeTrack(trackId: number) {
    // tracksRef always holds the latest list (updated synchronously here),
    // so removing two tracks back-to-back can't use a stale snapshot even
    // if the `tracks` closure captured by TrackList's extraActions is old.
    const before = tracksRef.current;
    const next = before.filter((t) => t.id !== trackId);
    tracksRef.current = next;
    setTracks(next);
    try {
      await api.updatePlaylist(id, { track_ids: next.map((t) => t.id) });
    } catch (e) {
      tracksRef.current = before;
      setTracks(before);
      setError(errorMessage(e, "playlist.saveFailed"));
    }
  }

  async function rename() {
    const trimmed = name.trim();
    if (!trimmed || renameBusy.current) return;
    renameBusy.current = true;
    setSavingName(true);
    try {
      await api.updatePlaylist(id, { name: trimmed });
      setPl((p) => (p ? { ...p, name: trimmed } : p));
      setRenaming(false);
    } catch (e) {
      setError(errorMessage(e, "playlist.renameFailed"));
    } finally {
      renameBusy.current = false;
      setSavingName(false);
    }
  }

  async function confirmDelete() {
    if (deleteBusy.current) return;
    deleteBusy.current = true;
    setDeleting(true);
    try {
      await api.deletePlaylist(id);
      nav("/playlists", { replace: true });
    } catch (e) {
      setError(errorMessage(e, "playlist.deleteFailed"));
      deleteBusy.current = false;
      setDeleting(false);
    }
  }

  if (error && !pl) return <p className="muted">{error}</p>;
  if (!pl) return <p className="muted">{t("common.loading")}</p>;
  const playable = tracks.filter((track) => !track.broken);
  return (
    <>
      {renaming ? (
        <div className="inline-form">
          <input value={name} onChange={(e) => setName(e.target.value)} disabled={savingName} autoFocus />
          <button className="primary" disabled={!name.trim() || savingName} onClick={rename}>{t("common.save")}</button>
        </div>
      ) : (
        <h1 className="page-title">
          <button type="button" className="page-title-btn" onClick={() => { setName(pl.name); setRenaming(true); }}>
            {pl.name}
          </button>
        </h1>
      )}
      {error && <p className="error">{error}</p>}
      <div className="actions">
        <button className="primary" disabled={!playable.length} onClick={() => player.playList(playable, 0)}>{t("playlist.playAll")}</button>
        {confirm ? (
          <button className="secondary danger" disabled={deleting} onClick={confirmDelete}>{t("playlist.deleteConfirm")}</button>
        ) : (
          <button className="secondary" onClick={() => setConfirm(true)}>{t("playlist.delete")}</button>
        )}
      </div>
      {tracks.length === 0 && <p className="muted">{t("playlist.empty")}</p>}
      <TrackList
        tracks={tracks}
        onRemoved={(tid) => {
          tracksRef.current = tracksRef.current.filter((track) => track.id !== tid);
          setTracks((ts) => ts.filter((track) => track.id !== tid));
        }}
        onChange={(track) => {
          tracksRef.current = tracksRef.current.map((x) => (x.id === track.id ? track : x));
          setTracks((ts) => ts.map((x) => (x.id === track.id ? track : x)));
        }}
        extraActions={(track) => [{ label: t("playlist.removeFromPlaylist"), run: () => void removeTrack(track.id) }]}
      />
    </>
  );
}
