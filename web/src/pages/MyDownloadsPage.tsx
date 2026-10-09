import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { Track } from "../api/types";
import { TrackList } from "../components/TrackList";
import { errorMessage } from "../i18n/errors";
import { t as translate, useT } from "../i18n/i18n";
import { usePlayer } from "../player/PlayerProvider";
import { shuffled } from "../player/queue";

// usePlayDownloads plays a user's "My downloads" (default: the caller's)
// straight from a tap: prime() runs inside the gesture, before the fetch.
export function usePlayDownloads() {
  const player = usePlayer();
  const [msg, setMsg] = useState("");
  async function play(opts: { shuffle?: boolean; user?: number | "all" } = {}) {
    player.prime();
    setMsg("");
    try {
      const ts = (await api.downloadTracks(opts.user)).filter((x) => !x.broken);
      if (ts.length === 0) return setMsg(translate("myDownloads.empty"));
      player.playList(opts.shuffle ? shuffled(ts) : ts, 0);
    } catch (e) {
      setMsg(errorMessage(e, "common.loadFailed"));
    }
  }
  return { play, msg };
}

export default function MyDownloadsPage() {
  const t = useT();
  const player = usePlayer();
  const [tracks, setTracks] = useState<Track[] | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    api.downloadTracks().then(setTracks).catch((e) => setError(errorMessage(e, "common.loadFailed")));
  }, []);
  const playable = (tracks ?? []).filter((x) => !x.broken);
  return (
    <>
      <h1 className="page-title">{t("myDownloads.title")}</h1>
      <p className="muted small">{t("myDownloads.hint")}</p>
      {error && <p className="error">{error}</p>}
      <div className="actions">
        <button className="primary" disabled={!playable.length} onClick={() => player.playList(playable, 0)}>{t("playlist.playAll")}</button>
        <button className="secondary" disabled={!playable.length} onClick={() => player.playList(shuffled(playable), 0)}>🔀 {t("library.shuffle")}</button>
      </div>
      {tracks?.length === 0 && <p className="muted">{t("myDownloads.empty")}</p>}
      <TrackList
        tracks={tracks ?? []}
        onRemoved={(id) => setTracks((ts) => ts?.filter((x) => x.id !== id) ?? ts)}
        onChange={(track) => setTracks((ts) => ts?.map((x) => (x.id === track.id ? track : x)) ?? ts)}
      />
    </>
  );
}
