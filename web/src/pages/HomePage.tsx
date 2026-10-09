import { useEffect, useState } from "react";
import { BRAND } from "../brand";
import { Link } from "react-router";
import { api } from "../api/client";
import type { TagCount, Track } from "../api/types";
import { RecommendationsSection } from "../components/RecommendationsSection";
import { useShuffleFavorites } from "../components/ShuffleFavorites";
import { TagChips } from "../components/TagChips";
import { TrackList } from "../components/TrackList";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { usePlayer } from "../player/PlayerProvider";

export default function HomePage() {
  const t = useT();
  const player = usePlayer();
  const [pending, setPending] = useState<Track[]>([]);
  const [recent, setRecent] = useState<Track[]>([]);
  const [radioMsg, setRadioMsg] = useState("");
  const [shuffleMsg, setShuffleMsg] = useState("");
  const [loadError, setLoadError] = useState("");
  const [topTags, setTopTags] = useState<TagCount[]>([]);
  const [activeDownloads, setActiveDownloads] = useState(0);
  const shuffleFavs = useShuffleFavorites();

  useEffect(() => {
    // Filter defensively by status: the mock in "home hides the pending
    // section when there are none" (and a real server ignoring an unknown
    // filter) can return non-pending items for this query, which must not
    // make an empty pending section appear.
    const fail = (e: unknown) => setLoadError(t("home.loadFailed", { msg: e instanceof Error ? e.message : t("home.networkError") }));
    api.tracks({ status: "pending", sort: "added", limit: 20 }).then((p) => setPending(p.items.filter((t) => t.status === "pending"))).catch(fail);
    api.tracks({ sort: "added", limit: 20 }).then((p) => setRecent(p.items)).catch(fail);
    api.tags().then((ts) => setTopTags([...ts].sort((a, b) => b.count - a.count).slice(0, 12))).catch(() => {});
    // Best-effort: a stalled/unreachable download service shouldn't block
    // or clutter the home screen, so failures here are silently ignored.
    api.downloads().then((jobs) => setActiveDownloads(jobs.filter((j) => j.status === "queued" || j.status === "downloading").length)).catch(() => {});
  }, []);

  async function startRadio() {
    // Unlock the element inside the tap: the playList() below runs after an
    // await, which iOS no longer counts as part of the gesture.
    player.prime();
    setRadioMsg("");
    try {
      const ts = await api.radio(30, []);
      if (ts.length === 0) setRadioMsg(t("home.radioEmpty"));
      else player.playList(ts, 0);
    } catch (e) {
      setRadioMsg(errorMessage(e, "home.radioUnavailable"));
    }
  }

  async function shuffleAll() {
    setShuffleMsg("");
    try {
      await player.shuffleAll();
    } catch (e) {
      setShuffleMsg(errorMessage(e, "home.shuffleUnavailable"));
    }
  }

  const drop = (setter: typeof setPending) => (id: number) => setter((ts) => ts.filter((track) => track.id !== id));
  const replace = (setter: typeof setPending) => (track: Track) => setter((ts) => ts.map((x) => (x.id === track.id ? track : x)));

  return (
    <>
      <h1 className="page-title">{BRAND}</h1>
      {/* One row of equal quick-play buttons. */}
      <div className="quick-actions">
        <button className="secondary quick" onClick={shuffleFavs.go}><span className="quick-icon">🔀</span>{" "}<span>{t("home.shuffleFavorites")}</span></button>
        <button className="secondary quick" onClick={shuffleAll} title={t("home.shuffleHint")}><span className="quick-icon">🔀</span>{" "}<span>{t("home.shuffle")}</span></button>
        <button className="secondary quick" onClick={startRadio} title={t("home.radioHint")}><span className="quick-icon">📻</span>{" "}<span>{t("home.radio")}</span></button>
      </div>
      {shuffleFavs.msg && <p className="error small">{shuffleFavs.msg}</p>}
      {shuffleMsg && <p className="error small">{shuffleMsg}</p>}
      {radioMsg && <p className="muted">{radioMsg}</p>}
      <RecommendationsSection />
      {player.current && (
        <section className="resume">
          <div className="ellipsis">
            <div className="muted small">{t("home.continuePlaying")}</div>
            <div className="ellipsis">{player.current.title}</div>
            <div className="ellipsis muted small">{player.current.artist}</div>
          </div>
          <button className="primary" onClick={player.toggle}>{player.playing ? t("common.pause") : t("common.play")}</button>
        </section>
      )}
      {activeDownloads > 0 && (
        <Link to="/downloads" className="chip">{t("home.downloadingCount", { count: activeDownloads })}</Link>
      )}
      {loadError && <p className="error small">{loadError}</p>}
      {pending.length > 0 && (
        <section>
          <h2 className="section-title">{t("home.pendingTitle")}</h2>
          <p className="muted small">{t("home.pendingHint")}</p>
          <TrackList tracks={pending} onRemoved={drop(setPending)} onChange={replace(setPending)} />
        </section>
      )}
      {topTags.length > 0 && (
        <section>
          <h2 className="section-title">{t("home.topTags")}</h2>
          <TagChips tags={topTags} />
        </section>
      )}
      <section>
        <h2 className="section-title">{t("home.recentTitle")}</h2>
        <TrackList tracks={recent} onRemoved={drop(setRecent)} onChange={replace(setRecent)} />
      </section>
    </>
  );
}
