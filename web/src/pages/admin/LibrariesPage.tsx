import { useEffect, useRef, useState } from "react";
import { api } from "../../api/client";
import type { Library, ScanStatus, Track } from "../../api/types";
import { formatDateTime } from "../../format";
import { errorMessage } from "../../i18n/errors";
import { useT } from "../../i18n/i18n";

const POLL_MS = 2000;

function resultLabels(t: ReturnType<typeof useT>): [keyof ScanStatus["last"], string][] {
  return [
    ["added", t("admin.libraries.resultAdded")],
    ["updated", t("admin.libraries.resultUpdated")],
    ["moved", t("admin.libraries.resultMoved")],
    ["missing", t("admin.libraries.resultMissing")],
    ["broken", t("admin.libraries.resultBroken")],
    ["unchanged", t("admin.libraries.resultUnchanged")],
  ];
}

function resultSummary(t: ReturnType<typeof useT>, last: ScanStatus["last"]): string {
  return resultLabels(t).map(([key, label]) => `${label} ${last[key] ?? 0}`).join(" · ");
}

export default function LibrariesPage() {
  const t = useT();
  const [libraries, setLibraries] = useState<Library[] | null>(null);
  const [statuses, setStatuses] = useState<Record<number, ScanStatus>>({});
  const [loadError, setLoadError] = useState("");
  const [scanBusy, setScanBusy] = useState<Set<number>>(new Set());
  const [scanError, setScanError] = useState<Record<number, string>>({});
  const [polling, setPolling] = useState(false);

  const [broken, setBroken] = useState<Track[] | null>(null);
  // Every broken file, also past the listed ones: what 全部删除 moves.
  const [brokenTotal, setBrokenTotal] = useState(0);
  const [brokenError, setBrokenError] = useState("");
  const [confirmBroken, setConfirmBroken] = useState(false);
  const [brokenBusy, setBrokenBusy] = useState(false);
  const [brokenMsg, setBrokenMsg] = useState("");
  const [brokenActionError, setBrokenActionError] = useState("");

  const scanBusyRef = useRef(scanBusy);
  scanBusyRef.current = scanBusy;

  function applyStatuses(sts: ScanStatus[]) {
    const map: Record<number, ScanStatus> = {};
    for (const st of sts) map[st.library_id] = st;
    setStatuses(map);
    return sts;
  }

  useEffect(() => {
    let cancelled = false;
    api
      .libraries()
      .then((libs) => {
        if (!cancelled) setLibraries(libs);
      })
      .catch((e: unknown) => {
        if (!cancelled) setLoadError(errorMessage(e, "common.loadFailed"));
      });
    api
      .scanStatus()
      .then((sts) => {
        if (cancelled) return;
        applyStatuses(sts);
        if (sts.some((s) => s.running)) setPolling(true);
      })
      .catch(() => {
        /* status is best-effort; the libraries list is the load-bearing fetch */
      });
    api
      .brokenTracks()
      .then((p) => {
        if (cancelled) return;
        setBroken(p.items);
        setBrokenTotal(p.total);
      })
      .catch((e: unknown) => {
        if (!cancelled) setBrokenError(errorMessage(e, "common.loadFailed"));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Polls /admin/scan/status every 2 s while any library is running, and
  // stops itself once none are — a fresh trigger (rescan) sets `polling`
  // back to true to resume it.
  useEffect(() => {
    if (!polling) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = () => {
      api
        .scanStatus()
        .then((sts) => {
          if (cancelled) return;
          applyStatuses(sts);
          if (sts.some((s) => s.running)) timer = setTimeout(tick, POLL_MS);
          else setPolling(false);
        })
        .catch(() => {
          if (!cancelled) timer = setTimeout(tick, POLL_MS);
        });
    };
    tick();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [polling]);

  async function rescan(lib: Library) {
    if (scanBusyRef.current.has(lib.id)) return;
    setScanBusy((s) => new Set(s).add(lib.id));
    setScanError((e) => ({ ...e, [lib.id]: "" }));
    try {
      await api.triggerScan(lib.id);
      setPolling(true);
    } catch (e) {
      setScanError((er) => ({ ...er, [lib.id]: errorMessage(e, "admin.libraries.rescanFailed") }));
    } finally {
      setScanBusy((s) => {
        const next = new Set(s);
        next.delete(lib.id);
        return next;
      });
    }
  }

  // 全部删除: every broken file to the trash (restorable for 30 days, as one
  // deleted by itself). The server moves nothing unless the confirmed total
  // still holds. Afterwards the list is fetched again: when some file could
  // not be moved, or the count had changed, it is all still there.
  async function trashAllBroken() {
    if (brokenBusy) return;
    setConfirmBroken(false);
    setBrokenBusy(true);
    setBrokenMsg("");
    setBrokenActionError("");
    try {
      const n = await api.trashBroken(brokenTotal);
      setBrokenMsg(t("admin.libraries.deletedAll", { count: n }));
    } catch (e) {
      setBrokenActionError(errorMessage(e, "admin.libraries.deleteAllFailed"));
    }
    try {
      const p = await api.brokenTracks();
      setBroken(p.items);
      setBrokenTotal(p.total);
    } catch (e) {
      setBrokenError(errorMessage(e, "common.loadFailed"));
    } finally {
      setBrokenBusy(false);
    }
  }

  return (
    <>
      {loadError && <p className="error">{loadError}</p>}
      {libraries?.length === 0 && <p className="muted">{t("admin.libraries.empty")}</p>}
      <ul className="rows">
        {libraries?.map((lib) => {
          const st = statuses[lib.id];
          return (
            <li key={lib.id} className="lib-row">
              <div className="lib-row-main">
                <span className="ellipsis">{lib.name}</span>
                {lib.download_target && <span className="badge">{t("admin.libraries.downloadTarget")}</span>}
                <button className="secondary" disabled={scanBusy.has(lib.id) || st?.running} onClick={() => void rescan(lib)}>
                  {t("admin.libraries.rescan", { name: lib.name })}
                </button>
              </div>
              <span className="muted small">{lib.root}</span>
              {st?.running && <span className="muted small">{t("admin.libraries.scanning")}</span>}
              {st && !st.running && st.finished_at > 0 && (
                <span className="muted small">{t("admin.libraries.lastScan", { time: formatDateTime(st.finished_at) })}</span>
              )}
              {st && !st.running && st.finished_at > 0 && <span className="muted small">{resultSummary(t, st.last)}</span>}
              {st?.last_error && <span className="error small">{st.last_error}</span>}
              {scanError[lib.id] && <span className="error small">{scanError[lib.id]}</span>}
            </li>
          );
        })}
      </ul>

      <h2 className="section-title">{t("admin.libraries.brokenHeading")}</h2>
      {!!broken?.length && (
        <div className="actions">
          {confirmBroken ? (
            <div role="alertdialog" aria-labelledby="broken-confirm-text">
              <p id="broken-confirm-text">{t("admin.libraries.deleteAllConfirmText", { count: brokenTotal })}</p>
              <button className="danger" disabled={brokenBusy} onClick={() => void trashAllBroken()}>{t("admin.libraries.deleteAllConfirm")}</button>
              <button className="secondary" disabled={brokenBusy} onClick={() => setConfirmBroken(false)}>{t("admin.libraries.deleteAllCancel")}</button>
            </div>
          ) : (
            <button className="secondary" disabled={brokenBusy} onClick={() => { setConfirmBroken(true); setBrokenMsg(""); setBrokenActionError(""); }}>
              {t("admin.libraries.deleteAll")}
            </button>
          )}
        </div>
      )}
      {brokenMsg && <p className="muted">{brokenMsg}</p>}
      {brokenActionError && <p className="error">{brokenActionError}</p>}
      {brokenError && <p className="error">{brokenError}</p>}
      {broken?.length === 0 && <p className="muted">{t("admin.libraries.noBroken")}</p>}
      <ul className="rows">
        {broken?.map((track) => (
          <li key={track.id} className="check-row">
            <span className="track-text">
              <span className="ellipsis track-title">{track.path}</span>
              <span className="muted small">{track.broken_reason || t("admin.libraries.unreadable")}</span>
            </span>
          </li>
        ))}
      </ul>
    </>
  );
}
