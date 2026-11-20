import { useEffect, useState } from "react";
import { api } from "../../api/client";
import type { VideoSourcesSetting } from "../../api/types";
import { sourceLabelKey } from "../../components/SourceSwitch";
import { errorMessage } from "../../i18n/errors";
import { useT } from "../../i18n/i18n";
import type { VideoSource } from "../../videoLink";
import { setVideoSources } from "../../videoSources";

const ALL: VideoSource[] = ["youtube", "bilibili"];

/** Admin → System: which video sources the server searches and downloads from. */
export function VideoSourcesCard() {
  const t = useT();
  const [setting, setSetting] = useState<VideoSourcesSetting | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    api
      .adminVideoSources()
      .then((r) => !cancelled && setSetting(r))
      .catch((e: unknown) => !cancelled && setError(errorMessage(e, "common.loadFailed")));
    return () => {
      cancelled = true;
    };
  }, []);

  async function toggle(src: VideoSource, on: boolean) {
    if (!setting) return;
    const next = ALL.filter((s) => (s === src ? on : setting.sources.includes(s)));
    setBusy(true);
    setError("");
    try {
      const r = await api.setAdminVideoSources(next);
      setSetting(r);
      setVideoSources(r.sources); // this page's own nav follows at once
    } catch (e) {
      setError(errorMessage(e, "common.actionFailed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="video-sources">
      <h3>{t("admin.videoSources.title")}</h3>
      <p id="video-sources-hint" className="small">{t("admin.videoSources.help")}</p>
      {setting && (
        <>
          {ALL.map((src) => (
            <div key={src} className="field field-check">
              <label className="field-check-row">
                <input
                  type="checkbox"
                  className="switch"
                  aria-describedby="video-sources-hint"
                  checked={setting.sources.includes(src)}
                  disabled={busy}
                  onChange={(e) => void toggle(src, e.target.checked)}
                />
                {t(sourceLabelKey(src))}
              </label>
            </div>
          ))}
          <p className="muted small">{t(setting.set_by === "admin" ? "admin.videoSources.fromAdmin" : "admin.videoSources.fromEnv")}</p>
        </>
      )}
      {error && <p className="error">{error}</p>}
    </section>
  );
}
