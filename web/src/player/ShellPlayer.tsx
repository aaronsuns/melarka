import { useEffect, useState } from "react";
import { EpisodeMini } from "../channels/EpisodeMini";
import { useEpisodes } from "../channels/EpisodesProvider";
import { useT } from "../i18n/i18n";
import { MiniPlayer } from "./MiniPlayer";
import { usePlayer } from "./PlayerProvider";
import { onSessionClaim, ownsSession } from "./sessionOwner";

/**
 * 继续收听: an episode queue that isn't playing (restored after a reload, or
 * set aside when music took over) — offered only while music owns the
 * session and is silent, never in music's place.
 */
function EpisodeResume() {
  const t = useT();
  const ep = useEpisodes();
  const music = usePlayer();
  // Not over a preview or a video either: only while the session is music's.
  const [musicOwns, setMusicOwns] = useState(() => ownsSession("music"));
  useEffect(() => onSessionClaim((o) => setMusicOwns(o === "music")), []);
  if (ep.active || !ep.current || music.playing || !musicOwns) return null;
  return (
    <div className="episode-resume" data-no-music-prime="">
      <button type="button" onClick={ep.toggle}>{t("episodes.resumeQueue", { title: ep.current.title })}</button>
    </div>
  );
}

// The episode mini player while an episode owns playback, else the music one.
export function ShellPlayer() {
  const ep = useEpisodes();
  if (ep.active && ep.current) return <EpisodeMini />;
  // One grid row in .shell, whichever shows.
  return (
    <div className="shell-player">
      <EpisodeResume />
      <MiniPlayer />
    </div>
  );
}
