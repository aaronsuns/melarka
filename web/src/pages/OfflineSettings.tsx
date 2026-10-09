import { useId, useState } from "react";
import { formatSize } from "../format";
import { useT } from "../i18n/i18n";
import { CAP_CHOICES, getOffline, useOffline } from "../offline";

/** Settings → Offline cache: favorites (and finished songs) kept on this device for tunnels and dead zones. */
export function OfflineSettings() {
  const t = useT();
  const snap = useOffline();
  const m = getOffline();
  const [confirmClear, setConfirmClear] = useState(false);
  const mobileHint = useId();

  if (!snap || !m) {
    return (
      <section>
        <h2 className="section-title">{t("settings.offline")}</h2>
        <p className="muted small">{t("settings.offline.unsupported")}</p>
      </section>
    );
  }
  const pct = snap.cap > 0 ? Math.min(100, (snap.usedBytes / snap.cap) * 100) : 0;
  const p = snap.progress;
  return (
    <section className="offline">
      <h2 className="section-title">{t("settings.offline")}</h2>
      <div className="offline-usage">
        <progress max={100} value={pct} aria-label={t("settings.offline")} />
        <span className="small">{t("settings.offline.used", { used: formatSize(snap.usedBytes), cap: formatSize(snap.cap) })}</span>
        <span className="muted small">{t("settings.offline.songs", { count: snap.count })}</span>
        {snap.estimate && snap.estimate.quota > 0 && (
          <span className="muted small">
            {t("settings.offline.storage", { usage: formatSize(snap.estimate.usage), quota: formatSize(snap.estimate.quota) })}
          </span>
        )}
        {snap.capReached && <span className="muted small">{t("settings.offline.capReached")}</span>}
        {snap.storageFull && <span className="error small">{t("settings.offline.storageFull")}</span>}
      </div>
      <label className="field">
        {t("settings.offline.cap")}
        <select aria-label={t("settings.offline.cap")} value={String(snap.cap)} onChange={(e) => void m.setCap(Number(e.target.value))}>
          {CAP_CHOICES.map((c) => (
            <option key={c} value={String(c)}>{formatSize(c)}</option>
          ))}
        </select>
      </label>
      <div className="offline-actions">
        <button className="secondary" onClick={() => void m.cacheFavoritesNow().catch(() => {})}>{t("settings.offline.cacheNow")}</button>
        {p && (
          <span className="muted small" role="status">
            {p.title
              ? t("settings.offline.progress", { done: p.done, total: p.total, title: p.title })
              : t("settings.offline.progressNoTitle", { done: p.done, total: p.total })}
          </span>
        )}
      </div>
      <div className="field field-check">
        <label className="field-check-row">
          <input type="checkbox" aria-describedby={mobileHint} checked={snap.allowMobile} onChange={(e) => m.setAllowMobile(e.target.checked)} />
          {t("settings.offline.mobileData")}
        </label>
        <span id={mobileHint} className="small">{t("settings.offline.mobileHint")}</span>
      </div>
      <div className="offline-actions">
        {confirmClear ? (
          <>
            <button
              className="danger"
              onClick={() => {
                setConfirmClear(false);
                void m.clear();
              }}
            >
              {t("settings.offline.clearConfirm")}
            </button>
            <button className="secondary" onClick={() => setConfirmClear(false)}>{t("common.cancel")}</button>
          </>
        ) : (
          <button className="secondary" disabled={snap.count === 0 && !p} onClick={() => setConfirmClear(true)}>{t("settings.offline.clear")}</button>
        )}
      </div>
      <p className="muted small">{t("settings.offline.hint")}</p>
    </section>
  );
}
