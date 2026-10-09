import { useState } from "react";
import { api } from "../api/client";
import type { Track } from "../api/types";
import { useAuth } from "../auth/AuthProvider";
import { duration, qualityLabel } from "../format";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { useOffline } from "../offline";
import { usePlayer } from "../player/PlayerProvider";
import { AddToPlaylist } from "./AddToPlaylist";
import { Cover, coverUrl } from "./Cover";
import { EditTags } from "./EditTags";
import { EditTrack } from "./EditTrack";
import { LyricsPicker } from "./LyricsPicker";

interface Props {
  tracks: Track[];
  showAlbum?: boolean;
  onChange?: (t: Track) => void;
  onRemoved?: (id: number) => void;
  extraActions?: (t: Track) => { label: string; run: () => void }[];
}

export function TrackList({ tracks, showAlbum = true, onChange, onRemoved, extraActions }: Props) {
  const t = useT();
  const player = usePlayer();
  const offline = useOffline();
  const { user } = useAuth();
  const [menuFor, setMenuFor] = useState<number | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<number | null>(null);
  const [adding, setAdding] = useState<Track | null>(null);
  const [editing, setEditing] = useState<Track | null>(null);
  const [editingTags, setEditingTags] = useState<Track | null>(null);
  const [pickingLyrics, setPickingLyrics] = useState<Track | null>(null);
  const [error, setError] = useState("");

  const closeMenu = () => {
    setMenuFor(null);
    setConfirmDeleteId(null);
  };

  const toggleMenu = (id: number) => {
    setMenuFor((prev) => (prev === id ? null : id));
    setConfirmDeleteId(null);
  };

  async function act(fn: () => Promise<void>) {
    setError("");
    try {
      await fn();
    } catch (e) {
      setError(errorMessage(e, "common.actionFailed"));
    }
    closeMenu();
  }

  const playable = tracks.filter((track) => !track.broken);

  return (
    <>
      {error && <p className="error" role="alert">{error}</p>}
      <ul className="tracks">
        {tracks.map((track) => {
          const isCurrent = player.current?.id === track.id;
          return (
            <li key={track.id} className={`track${isCurrent ? " current" : ""}${track.broken ? " broken" : ""}`}>
              <button
                className="track-main"
                disabled={track.broken}
                onClick={() => player.playList(playable, playable.findIndex((p) => p.id === track.id))}
              >
                <Cover seed={track.album || track.title} label={track.title} size={44} src={coverUrl("track", track.id)} />
                <span className="track-text">
                  <span className="ellipsis track-title">{track.title}</span>
                  <span className="ellipsis muted track-sub">
                    {track.favorite && <span className="heart" aria-label={t("track.favorited")}>♥ </span>}
                    {offline?.cachedIds.has(track.id) && <span className="cached-mark" role="img" aria-label={t("track.cached")} title={t("track.cached")}>⬇︎ </span>}
                    {[track.artist || t("common.unknownArtist"), showAlbum ? track.album : ""].filter(Boolean).join(" · ")}
                  </span>
                </span>
                <span className="muted track-meta">
                  <span className="badge">{qualityLabel(track)}</span>
                  {duration(track.duration_ms)}
                </span>
              </button>
              <button className="track-more" aria-label={t("track.more", { title: track.title })} onClick={() => toggleMenu(track.id)}>⋯</button>
              {menuFor === track.id && (
                <div className="menu" role="menu">
                  <button role="menuitem" onClick={() => { player.enqueueNext(track); closeMenu(); }}>{t("track.playNext")}</button>
                  <button role="menuitem" onClick={() => act(async () => { await api.setFavorite(track.id, !track.favorite); onChange?.({ ...track, favorite: !track.favorite }); })}>
                    {track.favorite ? t("common.unfavorite") : t("common.favorite")}
                  </button>
                  <button role="menuitem" onClick={() => act(async () => { await api.setDislike(track.id, true); player.remove(track.id); onRemoved?.(track.id); })}>{t("common.notForMe")}</button>
                  <button role="menuitem" onClick={() => { setAdding(track); closeMenu(); }}>{t("track.addToPlaylist")}</button>
                  {extraActions?.(track).map((a) => (
                    <button key={a.label} role="menuitem" onClick={() => { a.run(); closeMenu(); }}>{a.label}</button>
                  ))}
                  {user?.role === "admin" && (
                    <button role="menuitem" onClick={() => { setEditing(track); closeMenu(); }}>{t("track.editInfo")}</button>
                  )}
                  {user?.role === "admin" && (
                    <button role="menuitem" onClick={() => { setEditingTags(track); closeMenu(); }}>{t("track.editTags")}</button>
                  )}
                  {user?.role === "admin" && (
                    <button role="menuitem" onClick={() => { setPickingLyrics(track); closeMenu(); }}>{t("lyrics.change")}</button>
                  )}
                  {user?.role === "admin" &&
                    (confirmDeleteId === track.id ? (
                      <button role="menuitem" className="danger" onClick={() => act(async () => { await api.trashTrack(track.id); player.remove(track.id); onRemoved?.(track.id); })}>
                        {t("track.confirmDelete")}
                      </button>
                    ) : (
                      <button role="menuitem" className="danger" onClick={() => setConfirmDeleteId(track.id)}>{t("track.delete")}</button>
                    ))}
                </div>
              )}
            </li>
          );
        })}
      </ul>
      {adding && <AddToPlaylist track={adding} onClose={() => setAdding(null)} />}
      {editingTags && <EditTags track={editingTags} onClose={() => setEditingTags(null)} />}
      {pickingLyrics && <LyricsPicker track={pickingLyrics} onClose={() => setPickingLyrics(null)} />}
      {editing && (
        <EditTrack
          track={editing}
          onClose={() => setEditing(null)}
          onSaved={(updated) => {
            onChange?.(updated);
            player.updateTrack(updated);
            setEditing(null);
          }}
        />
      )}
    </>
  );
}
