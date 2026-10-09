import { useEffect, useState } from "react";
import { Link, useParams } from "react-router";
import { api, ApiError } from "../api/client";
import type { Album, Track } from "../api/types";
import { Cover, coverUrl } from "../components/Cover";
import { TrackList } from "../components/TrackList";
import { useT } from "../i18n/i18n";
import { usePlayer } from "../player/PlayerProvider";

function shuffled<T>(xs: T[]): T[] {
  const a = [...xs];
  for (let i = a.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [a[i], a[j]] = [a[j], a[i]];
  }
  return a;
}

export default function AlbumPage() {
  const t = useT();
  const { id } = useParams();
  const player = usePlayer();
  const [album, setAlbum] = useState<Album | null>(null);
  const [tracks, setTracks] = useState<Track[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    setError("");
    setAlbum(null);
    setTracks([]);
    api.album(Number(id))
      .then((r) => {
        if (cancelled) return;
        setAlbum(r.album);
        setTracks(r.tracks);
      })
      .catch((e) => {
        if (cancelled) return;
        setError(e instanceof ApiError && e.status === 404 ? t("album.notFound") : e.message);
      });
    return () => {
      cancelled = true;
    };
  }, [id, t]);

  if (error) return <p className="muted">{error}</p>;
  if (!album) return <p className="muted">{t("common.loading")}</p>;
  const playable = tracks.filter((track) => !track.broken);
  return (
    <>
      <header className="hero">
        <Cover seed={album.name} size={160} src={coverUrl("album", album.id)} />
        <div className="hero-text">
          <h1 className="hero-title">{album.name}</h1>
          {album.artist_id ? <Link to={`/artists/${album.artist_id}`} className="muted">{album.artist}</Link> : <span className="muted">{album.artist}</span>}
          <span className="muted small">{[album.year, t("common.songCount", { count: tracks.length })].filter(Boolean).join(" · ")}</span>
        </div>
      </header>
      <div className="actions">
        <button className="primary" disabled={!playable.length} onClick={() => player.playList(playable, 0)}>{t("album.playAll")}</button>
        <button className="secondary" disabled={!playable.length} onClick={() => player.playList(shuffled(playable), 0)}>{t("album.shuffle")}</button>
      </div>
      <TrackList tracks={tracks} showAlbum={false} onRemoved={(tid) => setTracks((ts) => ts.filter((t) => t.id !== tid))}
        onChange={(t) => setTracks((ts) => ts.map((x) => (x.id === t.id ? t : x)))} />
    </>
  );
}
