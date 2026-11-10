import { useEffect, useRef, useState } from "react";
import { api } from "../../api/client";
import { errorMessage } from "../../i18n/errors";
import { t, useT } from "../../i18n/i18n";

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
      <UsagePing />
    </>
  );
}

// The opt-in anonymous install count (internal/usageping): off until an admin ticks it.
function UsagePing() {
  const t = useT();
  const [on, setOn] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    api
      .usagePing()
      .then((r) => {
        if (!cancelled) setOn(r.enabled);
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
              disabled={busy}
              onChange={(e) => void change(e.target.checked)}
            />
            {t("admin.system.pingLabel")}
          </label>
          <span id="usage-ping-hint" className="small">{t("admin.system.pingExplain")}</span>
        </div>
      )}
      {error && <p className="error">{error}</p>}
    </section>
  );
}
