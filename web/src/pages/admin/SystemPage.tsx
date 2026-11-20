import { useEffect, useRef, useState } from "react";
import { api } from "../../api/client";
import { errorMessage } from "../../i18n/errors";
import { t, useT } from "../../i18n/i18n";
import { VideoSourcesCard } from "./VideoSourcesCard";

const UPDATE_TIMEOUT_MS = 60_000;

function timeout(ms: number): Promise<never> {
  return new Promise((_, reject) => setTimeout(() => reject(new Error(t("admin.system.updateTimeout"))), ms));
}

export default function SystemPage() {
  const t = useT();
  const [info, setInfo] = useState<{ version: string; path: string } | null>(null);
  const [loadError, setLoadError] = useState("");
  const [busy, setBusy] = useState(false);
  const [updateMsg, setUpdateMsg] = useState("");
  const [updateError, setUpdateError] = useState("");
  const busyRef = useRef(false);

  useEffect(() => {
    let cancelled = false;
    api
      .ytdlpInfo()
      .then((i) => {
        if (!cancelled) setInfo(i);
      })
      .catch((e: unknown) => {
        if (!cancelled) setLoadError(errorMessage(e, "common.loadFailed"));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  async function update() {
    if (busyRef.current) return;
    busyRef.current = true;
    setBusy(true);
    setUpdateMsg("");
    setUpdateError("");
    const real = api.ytdlpUpdate();
    // The button must stay disabled until the real request actually
    // settles, even if the 60s timeout below reports sooner — otherwise a
    // second update could be sent while the first is still running on the
    // server. The .catch here only prevents an unhandled-rejection warning
    // once the race (below) has already moved on to its own error path.
    real.catch(() => {}).finally(() => {
      busyRef.current = false;
      setBusy(false);
    });
    try {
      const i = await Promise.race([real, timeout(UPDATE_TIMEOUT_MS)]);
      setInfo(i);
      setUpdateMsg(t("admin.system.updated", { version: i.version }));
    } catch (e) {
      setUpdateError(errorMessage(e, "admin.system.updateFailed"));
    }
  }

  return (
    <>
      <UpdateBanner />
      {loadError && <p className="error">{loadError}</p>}
      {info && (
        <>
          <p>{t("admin.system.currentVersion", { version: info.version })}</p>
          <p className="muted small">{t("admin.system.path", { path: info.path })}</p>
        </>
      )}
      <div className="actions">
        <button className="primary" disabled={busy} onClick={() => void update()}>
          {busy ? t("admin.system.updating") : t("admin.system.update")}
        </button>
      </div>
      {updateMsg && <p className="muted">{updateMsg}</p>}
      {updateError && <p className="error">{updateError}</p>}
      <VideoSourcesCard />
      <UsagePing />
    </>
  );
}

const DISMISSED_KEY = "lark.updateDismissed";

// "A newer Melarka is out" (internal/updatecheck): admins only, dismissed per version in this browser.
// Silent when the check is off, failed or found nothing newer.
function UpdateBanner() {
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

// The anonymous install count (internal/usageping): on by default; LARK_USAGE_PING=off locks it off.
function UsagePing() {
  const t = useT();
  const [on, setOn] = useState<boolean | null>(null);
  const [envOff, setEnvOff] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    api
      .usagePing()
      .then((r) => {
        if (!cancelled) {
          setOn(r.enabled);
          setEnvOff(!!r.env_off);
        }
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(errorMessage(e, "common.loadFailed"));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  async function change(next: boolean) {
    setBusy(true);
    setError("");
    try {
      setOn((await api.setUsagePing(next)).enabled);
    } catch (e) {
      setError(errorMessage(e, "admin.system.pingFailed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="usage-ping">
      <h3>{t("admin.system.pingTitle")}</h3>
      {on !== null && (
        <div className="field field-check">
          <label className="field-check-row">
            <input
              type="checkbox"
              className="switch"
              aria-describedby="usage-ping-hint"
              checked={on}
              disabled={busy || envOff}
              onChange={(e) => void change(e.target.checked)}
            />
            {t("admin.system.pingLabel")}
          </label>
          <span id="usage-ping-hint" className="small">
            {t("admin.system.pingExplain")}
            {envOff && <> {t("admin.system.pingEnvOff")}</>}
          </span>
        </div>
      )}
      {error && <p className="error">{error}</p>}
    </section>
  );
}
