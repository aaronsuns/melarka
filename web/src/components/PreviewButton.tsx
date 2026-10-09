import { usePreviews, type PreviewTarget } from "../channels/PreviewProvider";
import { useT } from "../i18n/i18n";
import { PlayIcon } from "./icons";

/**
 * ▶ 试听 (or another label): opens the preview sheet; never starts the music
 * queue. `icon`: a round outlined ▶ for result rows, named 试听.
 */
export function PreviewButton({ target, label, icon }: { target: PreviewTarget; label?: string; icon?: boolean }) {
  const t = useT();
  const previews = usePreviews();
  if (icon) {
    return (
      <button className="icon-btn outline" data-no-music-prime="" aria-label={t("preview.listen")} title={t("preview.listen")} onClick={() => previews.open(target)}>
        <PlayIcon size={16} />
      </button>
    );
  }
  return (
    <button className="secondary" data-no-music-prime="" onClick={() => previews.open(target)}>
      {label ?? `▶ ${t("preview.listen")}`}
    </button>
  );
}
