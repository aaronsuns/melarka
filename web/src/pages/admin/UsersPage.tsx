import { useEffect, useRef, useState, type FormEvent } from "react";
import { api } from "../../api/client";
import type { Role, User } from "../../api/types";
import { useAuth } from "../../auth/AuthProvider";
import { errorMessage } from "../../i18n/errors";
import { useT } from "../../i18n/i18n";

interface RowState {
  resetOpen: boolean;
  resetValue: string;
  resetBusy: boolean;
  resetError: string;
  confirmDelete: boolean;
  deleteBusy: boolean;
  deleteError: string;
}

const emptyRow: RowState = {
  resetOpen: false,
  resetValue: "",
  resetBusy: false,
  resetError: "",
  confirmDelete: false,
  deleteBusy: false,
  deleteError: "",
};

export default function UsersPage() {
  const t = useT();
  const { user: me } = useAuth();
  const [users, setUsers] = useState<User[] | null>(null);
  const [loadError, setLoadError] = useState("");
  const [rows, setRows] = useState<Record<number, RowState>>({});

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Role>("member");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState("");
  const createBusy = useRef(false);
  const resetBusy = useRef<Set<number>>(new Set());
  const deleteBusy = useRef<Set<number>>(new Set());

  useEffect(() => {
    let cancelled = false;
    api
      .users()
      .then((us) => {
        if (!cancelled) setUsers(us);
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

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    const u = username.trim();
    if (!u || !password || createBusy.current) return;
    createBusy.current = true;
    setCreating(true);
    setCreateError("");
    try {
      const created = await api.createUser({ username: u, password, role });
      setUsers((us) => [...(us ?? []), created]);
      setUsername("");
      setPassword("");
      setRole("member");
    } catch (err) {
      setCreateError(errorMessage(err, "admin.users.createFailed"));
    } finally {
      createBusy.current = false;
      setCreating(false);
    }
  }

  function openReset(id: number) {
    patchRow(id, { resetOpen: true, resetValue: "", resetError: "" });
  }
  function cancelReset(id: number) {
    patchRow(id, { ...emptyRow });
  }
  async function submitReset(id: number) {
    const r = row(id);
    if (!r.resetValue || resetBusy.current.has(id)) return;
    resetBusy.current.add(id);
    patchRow(id, { resetBusy: true, resetError: "" });
    try {
      await api.resetPassword(id, r.resetValue);
      patchRow(id, { ...emptyRow });
    } catch (err) {
      patchRow(id, { resetBusy: false, resetError: errorMessage(err, "admin.users.resetFailed") });
    } finally {
      resetBusy.current.delete(id);
    }
  }

  function askDelete(id: number) {
    patchRow(id, { confirmDelete: true, deleteError: "" });
  }
  function cancelDelete(id: number) {
    patchRow(id, { confirmDelete: false, deleteError: "" });
  }
  async function doDelete(id: number) {
    if (deleteBusy.current.has(id)) return;
    deleteBusy.current.add(id);
    patchRow(id, { deleteBusy: true, deleteError: "" });
    try {
      await api.deleteUser(id);
      setUsers((us) => us?.filter((u) => u.id !== id) ?? us);
      setRows((rs) => {
        if (!(id in rs)) return rs;
        const next = { ...rs };
        delete next[id];
        return next;
      });
    } catch (err) {
      patchRow(id, { deleteBusy: false, deleteError: errorMessage(err, "admin.users.deleteFailed") });
    } finally {
      deleteBusy.current.delete(id);
    }
  }

  return (
    <>
      <form className="inline-form" onSubmit={submitCreate}>
        <input placeholder={t("admin.users.usernamePlaceholder")} value={username} onChange={(e) => setUsername(e.target.value)} disabled={creating} />
        <input
          placeholder={t("admin.users.passwordPlaceholder")}
          type="password"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          disabled={creating}
        />
        <select aria-label={t("admin.users.roleLabel")} value={role} onChange={(e) => setRole(e.target.value as Role)} disabled={creating}>
          <option value="member">{t("admin.users.roleMember")}</option>
          <option value="admin">{t("admin.users.roleAdmin")}</option>
        </select>
        <button className="primary" disabled={!username.trim() || !password || creating}>{t("admin.users.create")}</button>
      </form>
      {createError && <p className="error">{createError}</p>}
      {loadError && <p className="error">{loadError}</p>}
      <ul className="rows">
        {users?.map((u) => {
          const r = row(u.id);
          const isSelf = u.id === me?.id;
          return (
            <li key={u.id} className="user-row">
              <div className="user-row-main">
                <span className="badge">{u.role === "admin" ? t("admin.users.roleAdmin") : t("admin.users.roleMember")}</span>
                <span className="ellipsis">{u.username}</span>
                <button className="secondary" disabled={r.resetOpen} onClick={() => openReset(u.id)}>
                  {t("admin.users.resetPassword", { username: u.username })}
                </button>
                {!isSelf && (
                  <button
                    className={r.confirmDelete ? "danger" : "secondary"}
                    disabled={r.deleteBusy}
                    onClick={() => (r.confirmDelete ? void doDelete(u.id) : askDelete(u.id))}
                  >
                    {r.confirmDelete ? t("admin.users.confirmDelete", { username: u.username }) : t("admin.users.delete", { username: u.username })}
                  </button>
                )}
                {!isSelf && r.confirmDelete && (
                  <button className="secondary" disabled={r.deleteBusy} onClick={() => cancelDelete(u.id)}>
                    {t("admin.users.cancelDelete", { username: u.username })}
                  </button>
                )}
              </div>
              {r.resetOpen && (
                <div className="inline-form">
                  <input
                    aria-label={t("admin.users.newPasswordLabel", { username: u.username })}
                    type="password"
                    autoComplete="new-password"
                    value={r.resetValue}
                    onChange={(e) => patchRow(u.id, { resetValue: e.target.value })}
                    disabled={r.resetBusy}
                  />
                  <button className="primary" disabled={!r.resetValue || r.resetBusy} onClick={() => void submitReset(u.id)}>
                    {t("admin.users.save", { username: u.username })}
                  </button>
                  <button className="secondary" onClick={() => cancelReset(u.id)}>
                    {t("admin.users.cancelReset", { username: u.username })}
                  </button>
                </div>
              )}
              {r.resetError && <p className="error">{r.resetError}</p>}
              {r.deleteError && <p className="error">{r.deleteError}</p>}
            </li>
          );
        })}
      </ul>
    </>
  );
}
