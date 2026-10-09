import { useEffect, useState } from "react";
import { useParams } from "react-router";
import { api, ApiError } from "../api/client";
import type { Album, Artist } from "../api/types";
import { AlbumCards } from "../components/Cards";
import { Cover } from "../components/Cover";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { usePlayer } from "../player/PlayerProvider";

export default function ArtistPage() {
  const t = useT();
  const { id } = useParams();
  const player = usePlayer();
  const [artist, setArtist] = useState<Artist | null>(null);
  const [albums, setAlbums] = useState<Album[]>([]);
  const [error, setError] = useState("");
  const [playError, setPlayError] = useState("");

  useEffect(() => {
    let cancelled = false;
    setError("");
    setArtist(null);
    setAlbums([]);
    api.artist(Number(id))
      .then((r) => {
        if (cancelled) return;
        setArtist(r.artist);
        setAlbums(r.albums);
      })
      .catch((e) => {
        if (cancelled) return;
        setError(e instanceof ApiError && e.status === 404 ? t("artist.notFound") : e.message);
      });
    return () => {
      cancelled = true;
    };
  }, [id, t]);

  async function playAll() {
    // The fetch below means playList() runs after an await, outside the
    // tap as far as iOS is concerned — prime the element inside it first.
    player.prime();
    setPlayError("");
    try {
      const p = await api.tracks({ artist: Number(id), sort: "album", limit: 500 });
      const ts = p.items.filter((track) => !track.broken);
      if (ts.length) player.playList(ts, 0);
      else setPlayError(t("artist.noPlayable"));
    } catch (e) {
      setPlayError(errorMessage(e, "artist.playFailed"));
    }
  }

  if (error) return <p className="muted">{error}</p>;
  if (!artist) return <p className="muted">{t("common.loading")}</p>;
  return (
    <>
      <header className="hero">
        <Cover seed={artist.name} size={120} round />
        <div className="hero-text">
          <h1 className="hero-title">{artist.name}</h1>
          <span className="muted small">{t("common.songCount", { count: artist.track_count })}</span>
        </div>
      </header>
      <div className="actions"><button className="primary" disabled={artist.track_count === 0} onClick={() => void playAll()}>{t("artist.playAll")}</button></div>
      {playError && <p className="error">{playError}</p>}
      <h2 className="section-title">{t("artist.albumsHeading")}</h2>
      {albums.length === 0 ? <p className="muted">{t("artist.noAlbums")}</p> : <AlbumCards albums={albums} grid />}
    </>
  );
}
