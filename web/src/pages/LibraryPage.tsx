import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { api } from "../api/client";
import type { Album, Artist, TagCount, TagKind, Track } from "../api/types";
import { AlbumCards, ArtistChips } from "../components/Cards";
import { More } from "../components/More";
import { ShuffleFavoritesButton } from "../components/ShuffleFavorites";
import { TagChips } from "../components/TagChips";
import { TrackList } from "../components/TrackList";
import { useInfinite } from "../components/useInfinite";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { usePlayer } from "../player/PlayerProvider";

function tabs(t: ReturnType<typeof useT>) {
  return [
    ["favorites", t("library.tabFavorites")],
    ["songs", t("library.tabSongs")],
    ["albums", t("library.tabAlbums")],
    ["artists", t("library.tabArtists")],
    ["tags", t("library.tags")],
    ["playlists", t("library.tabPlaylists")],
  ] as const;
}

function Songs({ favorites }: { favorites: boolean }) {
  const t = useT();
  const s = useInfinite<Track>((cursor) => api.tracks({ sort: favorites ? "added" : "title", favorite: favorites ? 1 : undefined, limit: 100, cursor }), [favorites]);
  return (
    <>
      {s.done && s.items.length === 0 && <p className="muted">{favorites ? t("library.noFavorites") : t("library.empty")}</p>}
      <TrackList
        tracks={s.items}
        onRemoved={(id) => s.setItems((ts) => ts.filter((track) => track.id !== id))}
        onChange={(track) => s.setItems((ts) => (favorites && !track.favorite ? ts.filter((x) => x.id !== track.id) : ts.map((x) => (x.id === track.id ? track : x))))}
      />
      <More {...s} />
    </>
  );
}

function Albums() {
  const t = useT();
  const s = useInfinite<Album>((cursor) => api.albums(cursor), []);
  return (
    <>
      {s.done && s.items.length === 0 && <p className="muted">{t("library.noAlbums")}</p>}
      <AlbumCards albums={s.items} grid />
      <More {...s} />
    </>
  );
}

function Artists() {
  const t = useT();
  const s = useInfinite<Artist>((cursor) => api.artists(cursor), []);
  return (
    <>
      {s.done && s.items.length === 0 && <p className="muted">{t("library.noArtists")}</p>}
      <ArtistChips artists={s.items} />
      <More {...s} />
    </>
  );
}

const KIND_ORDER: TagKind[] = ["genre", "mood", "scene", "era", "language", "other"];

function Tags() {
  const t = useT();
  const [tags, setTags] = useState<TagCount[] | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    api.tags().then(setTags).catch((e) => setError(errorMessage(e, "common.actionFailed")));
  }, []);
  if (error) return <p className="error">{error}</p>;
  if (tags === null) return null;
  if (tags.length === 0) return <p className="muted">{t("library.noTags")}</p>;
  const kindOf = (k: string): TagKind => (KIND_ORDER.includes(k as TagKind) ? (k as TagKind) : "other");
  return (
    <>
      {KIND_ORDER.map((kind) => {
        const group = tags.filter((x) => kindOf(x.kind) === kind);
        if (group.length === 0) return null;
        return (
          <section key={kind}>
            <h2 className="section-title">{t(`tagKind.${kind}`)}</h2>
            <TagChips tags={group} />
          </section>
        );
      })}
    </>
  );
}

export default function LibraryPage() {
  const t = useT();
  const player = usePlayer();
  const [params, setParams] = useSearchParams();
  const [shuffleMsg, setShuffleMsg] = useState("");
  const tab = params.get("tab") ?? "favorites";

  async function shuffleAll() {
    setShuffleMsg("");
    try {
      await player.shuffleAll();
    } catch (e) {
      setShuffleMsg(errorMessage(e, "library.shuffleUnavailable"));
    }
  }

  return (
    <>
      <h1 className="page-title">{t("library.title")}</h1>
      <div className="segmented" role="tablist">
        {tabs(t).map(([key, label]) =>
          key === "playlists" ? (
            <Link key={key} to="/playlists" role="tab">{label}</Link>
          ) : (
            <button key={key} role="tab" aria-selected={tab === key} onClick={() => setParams({ tab: key })}>{label}</button>
          ),
        )}
      </div>
      {tab === "songs" && (
        <>
          <div className="tab-actions">
            <button className="secondary" onClick={shuffleAll}>🔀 {t("library.shuffle")}</button>
            {shuffleMsg && <p className="error small">{shuffleMsg}</p>}
          </div>
        </>
      )}
      {tab === "songs" && <Songs favorites={false} />}
      {tab === "favorites" && <ShuffleFavoritesButton />}
      {tab === "favorites" && <Songs favorites />}
      {tab === "albums" && <Albums />}
      {tab === "artists" && <Artists />}
      {tab === "tags" && <Tags />}
    </>
  );
}
