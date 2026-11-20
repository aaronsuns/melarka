import { useT } from "../i18n/i18n";
import type { VideoSource } from "../videoLink";
import { rememberSource } from "../videoSources";

export const sourceLabelKey = (s: VideoSource) => (s === "bilibili" ? "search.sourceBilibili" : "search.sourceYouTube");

/** YouTube | 哔哩哔哩 above the video results, when the server has both on. */
export function SourceSwitch({ sources, value, onChange }: { sources: VideoSource[]; value: VideoSource; onChange: (s: VideoSource) => void }) {
  const t = useT();
  if (sources.length < 2) return null;
  return (
    <div className="source-switch" role="radiogroup" aria-label={t("search.sourceLabel")}>
      {sources.map((s) => (
        <label key={s} className={s === value ? "chip tag-chip active" : "chip tag-chip"}>
          <input
            type="radio"
            name="video-source"
            value={s}
            checked={s === value}
            onChange={() => {
              rememberSource(s);
              onChange(s);
            }}
          />
          {t(sourceLabelKey(s))}
        </label>
      ))}
    </div>
  );
}
