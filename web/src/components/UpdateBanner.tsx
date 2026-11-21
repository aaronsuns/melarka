import { useEffect, useState } from "react";
import { api } from "../api/client";
import { useT } from "../i18n/i18n";

const DISMISSED_KEY = "lark.updateDismissed";

// "A newer Melarka is out" (internal/updatecheck): shown to admins on Home and Admin → System, dismissed per version in this browser.
// Silent when the check is off, failed or found nothing newer.
export function UpdateBanner() {
  const t = useT();
  const [upd, setUpd] = useState<{ latest: string; current: string; url: string } | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .updateCheck()
      .then((r) => {
        if (cancelled || !r.newer) return;
        let dismissed = "";
        try {
          dismissed = localStorage.getItem(DISMISSED_KEY) ?? "";
        } catch {
          // storage unavailable: show it every time
        }
        if (dismissed !== r.latest) setUpd(r);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  if (!upd) return null;
  function dismiss() {
    try {
      localStorage.setItem(DISMISSED_KEY, upd!.latest);
    } catch {
      // remembered only until the page closes
    }
    setUpd(null);
  }
  return (
    <div className="notice-strip update-banner" role="status">
      <div>
        <p>
          {t("admin.system.updateAvailable", { latest: upd.latest, current: upd.current })}{" "}
          <a href={upd.url} target="_blank" rel="noreferrer">{t("admin.system.updateNotes")}</a>
        </p>
        <p className="muted small">{t("admin.system.updateHow")}</p>
      </div>
      <button className="secondary" onClick={dismiss}>{t("admin.system.updateDismiss")}</button>
    </div>
  );
}
