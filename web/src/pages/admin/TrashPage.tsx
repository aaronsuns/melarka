import { useEffect, useRef, useState } from "react";
import { api, ApiError } from "../../api/client";
import type { TrashItem } from "../../api/types";
import { daysUntil } from "../../format";
import { errorMessage } from "../../i18n/errors";
import { useT } from "../../i18n/i18n";

interface RowState {
  busy: boolean;
  error: string;
}

const emptyRow: RowState = { busy: false, error: "" };

export default function TrashPage() {
  const t = useT();
  const [items, setItems] = useState<TrashItem[] | null>(null);
  const [loadError, setLoadError] = useState("");
  const [rows, setRows] = useState<Record<number, RowState>>({});
  const restoreBusy = useRef<Set<number>>(new Set());

  const [confirmEmpty, setConfirmEmpty] = useState(false);
  const [emptyBusy, setEmptyBusy] = useState(false);
  const [emptyMsg, setEmptyMsg] = useState("");
  const [emptyError, setEmptyError] = useState("");

  useEffect(() => {
    let cancelled = false;
    api
      .trash()
      .then((ts) => {
        if (!cancelled) setItems(ts);
      })
      .catch((e: unknown) => {
        if (!cancelled) setLoadError(errorMessage(e, "common.loadFailed"));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  function row(id: number): RowState {
    return rows[id] ?? emptyRow;
  }
  function patchRow(id: number, patch: Partial<RowState>) {
    setRows((r) => ({ ...r, [id]: { ...row(id), ...patch } }));
  }

  async function restore(item: TrashItem) {
    if (restoreBusy.current.has(item.track_id)) return;
    restoreBusy.current.add(item.track_id);
    patchRow(item.track_id, { busy: true, error: "" });
    try {
      await api.restoreTrash(item.track_id);
      setItems((ts) => ts?.filter((t) => t.track_id !== item.track_id) ?? ts);
    } catch (e) {
      // A 409 always means the restore conflict, whether or not the server
      // sent the "restore_conflict" code (older responses may not).
      const msg = e instanceof ApiError && e.status === 409 ? t("error.restore_conflict") : errorMessage(e, "admin.trash.restoreFailed");
      patchRow(item.track_id, { busy: false, error: msg });
    } finally {
      restoreBusy.current.delete(item.track_id);
    }
  }

  function askEmpty() {
    setConfirmEmpty(true);
    setEmptyError("");
  }
  function cancelEmpty() {
    setConfirmEmpty(false);
  }
  async function doEmpty() {
    if (emptyBusy) return;
    setConfirmEmpty(false);
    setEmptyBusy(true);
    setEmptyError("");
    setEmptyMsg("");
    try {
      const n = await api.emptyTrash();
      setEmptyMsg(t("admin.trash.emptiedCount", { count: n }));
      setItems([]);
    } catch (e) {
      setEmptyError(errorMessage(e, "admin.trash.emptyFailed"));
    } finally {
      setEmptyBusy(false);
    }
  }

  return (
    <>
      <div className="actions">
        {confirmEmpty ? (
          <>
            <button className="danger" disabled={emptyBusy} onClick={() => void doEmpty()}>{t("admin.trash.emptyConfirm")}</button>
            <button className="secondary" disabled={emptyBusy} onClick={cancelEmpty}>{t("admin.trash.cancelEmpty")}</button>
          </>
        ) : (
          <button className="secondary" disabled={emptyBusy || !items || items.length === 0} onClick={askEmpty}>{t("admin.trash.emptyButton")}</button>
        )}
      </div>
      {emptyMsg && <p className="muted">{emptyMsg}</p>}
      {emptyError && <p className="error">{emptyError}</p>}
      {loadError && <p className="error">{loadError}</p>}
      {items?.length === 0 && <p className="muted">{t("admin.trash.isEmpty")}</p>}
      <ul className="rows">
        {items?.map((item) => {
          const r = row(item.track_id);
          return (
            <li key={item.track_id} className="check-row">
              <span className="track-text">
                <span className="ellipsis track-title">{item.path}</span>
                <span className="muted small">{t("admin.trash.purgeIn", { count: daysUntil(item.purge_at) })}</span>
                {r.error && <span className="error small">{r.error}</span>}
              </span>
              <button className="secondary" disabled={r.busy} onClick={() => void restore(item)}>
                {t("admin.trash.restore", { path: item.path })}
              </button>
            </li>
          );
        })}
      </ul>
    </>
  );
}
