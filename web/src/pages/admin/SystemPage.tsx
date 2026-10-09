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
    </>
  );
}
