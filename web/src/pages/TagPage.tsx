import { useState } from "react";
import { useParams } from "react-router";
import { api } from "../api/client";
import type { Track } from "../api/types";
import { More } from "../components/More";
import { tagLabel } from "../components/TagChips";
import { TrackList } from "../components/TrackList";
import { useInfinite } from "../components/useInfinite";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { usePlayer } from "../player/PlayerProvider";

export default function TagPage() {
  const t = useT();
  const player = usePlayer();
  const name = useParams().name ?? "";
  const [msg, setMsg] = useState("");
  const s = useInfinite<Track>((cursor) => api.tracks({ tag: name, sort: "title", limit: 100, cursor }), [name]);

  async function shuffle() {
    // Unlock the element inside the tap; playList runs after an await.
    player.prime();
    setMsg("");
    try {
      const ts = await api.randomTracks(200, [], { tag: name });
      if (ts.length > 0) player.playList(ts, 0);
    } catch (e) {
      setMsg(errorMessage(e, "library.shuffleUnavailable"));
    }
  }

  return (
    <>
      <h1 className="page-title">{tagLabel(name)}</h1>
      <div className="tab-actions">
        <button className="secondary" onClick={shuffle}>{t("tagPage.shuffle")}</button>
        {msg && <p className="error small">{msg}</p>}
      </div>
      <TrackList
        tracks={s.items}
        onRemoved={(id) => s.setItems((ts) => ts.filter((x) => x.id !== id))}
        onChange={(track) => s.setItems((ts) => ts.map((x) => (x.id === track.id ? track : x)))}
      />
      <More {...s} />
    </>
  );
}
