import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { api } from "../api/client";
import type { Playlist } from "../api/types";
import { Cover } from "../components/Cover";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { usePlayDownloads } from "./MyDownloadsPage";

export default function PlaylistsPage() {
  const t = useT();
  const [lists, setLists] = useState<Playlist[] | null>(null);
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [creating, setCreating] = useState(false);
  const busy = useRef(false);
  const nav = useNavigate();
  const downloads = usePlayDownloads();

  useEffect(() => {
    api.playlists().then(setLists).catch((e) => setError(e.message));
  }, []);

  async function create(e: FormEvent) {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed || busy.current) return;
    busy.current = true;
    setCreating(true);
    try {
      const p = await api.createPlaylist(trimmed, []);
      nav(`/playlists/${p.id}`);
    } catch (err) {
      setError(errorMessage(err, "playlists.createFailed"));
      busy.current = false;
      setCreating(false);
    }
  }

  return (
    <>
      <h1 className="page-title">{t("playlists.title")}</h1>
      <form className="inline-form" onSubmit={create}>
        <input placeholder={t("playlists.newPlaceholder")} value={name} onChange={(e) => setName(e.target.value)} disabled={creating} />
        <button className="primary" disabled={!name.trim() || creating}>{t("playlists.create")}</button>
      </form>
      {error && <p className="error">{error}</p>}
      {lists?.length === 0 && <p className="muted">{t("playlists.empty")}</p>}
      <ul className="rows">
        <li className="row-with-actions">
          <Link to="/my-downloads" className="row">
            <Cover seed="my-downloads" label="⬇" size={48} />
            <span className="ellipsis">{t("myDownloads.title")}</span>
          </Link>
          <button className="icon" aria-label={t("myDownloads.playAria")} onClick={() => void downloads.play()}>▶</button>
          <button className="icon" aria-label={t("myDownloads.shuffleAria")} onClick={() => void downloads.play({ shuffle: true })}>🔀</button>
        </li>
        {downloads.msg && <li className="muted small">{downloads.msg}</li>}
        {lists?.map((p) => (
          <li key={p.id}>
            <Link to={`/playlists/${p.id}`} className="row">
              <Cover seed={p.name} size={48} />
              <span className="ellipsis">{p.name}</span>
              <span className="muted small">{t("common.songCount", { count: p.track_count })}</span>
            </Link>
          </li>
        ))}
      </ul>
    </>
  );
}
