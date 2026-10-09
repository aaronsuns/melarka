import { Link } from "react-router";
import type { Album, Artist } from "../api/types";
import { useT } from "../i18n/i18n";
import { Cover, coverUrl } from "./Cover";

export function AlbumCards({ albums, grid = false }: { albums: Album[]; grid?: boolean }) {
  const t = useT();
  return (
    <div className={grid ? "grid" : "cards"}>
      {albums.map((a) => (
        <Link key={a.id} to={`/albums/${a.id}`} className="card">
          <Cover seed={a.name} size={grid ? 150 : 120} src={coverUrl("album", a.id)} />
          <span className="ellipsis card-title">{a.name}</span>
          <span className="ellipsis muted card-sub">{a.artist || t("common.songCount", { count: a.track_count })}</span>
        </Link>
      ))}
    </div>
  );
}

export function ArtistChips({ artists }: { artists: Artist[] }) {
  return (
    <div className="chips">
      {artists.map((a) => (
        <Link key={a.id} to={`/artists/${a.id}`} className="chip">
          <Cover seed={a.name} size={28} round />
          <span className="ellipsis">{a.name}</span>
        </Link>
      ))}
    </div>
  );
}
