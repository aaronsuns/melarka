import { useRef, useState, type FormEvent } from "react";
import { api } from "../api/client";
import { useAuth } from "../auth/AuthProvider";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";

export default function ChangePassword() {
  const { logout } = useAuth();
  const t = useT();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [repeat, setRepeat] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [msg, setMsg] = useState("");
  const submitting = useRef(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (submitting.current) return;
    setError("");
    setMsg("");
    if (next !== repeat) {
      setError(t("password.mismatch"));
      return;
    }
    submitting.current = true;
    setBusy(true);
    try {
      await api.changePassword(current, next);
      setMsg(t("password.success"));
      // The server already revoked every device's token; logout() swallows
      // whatever status its own request now comes back with.
      await logout();
    } catch (err) {
      setError(errorMessage(err, "password.changeFailed"));
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  }

  return (
    <>
      <h1 className="page-title">{t("password.title")}</h1>
      <form className="login-form" onSubmit={submit}>
        <label>
          {t("password.current")}
          <input
            type="password"
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            disabled={busy}
          />
        </label>
        <label>
          {t("password.new")}
          <input
            type="password"
            autoComplete="new-password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
            disabled={busy}
          />
        </label>
        <label>
          {t("password.repeat")}
          <input
            type="password"
            autoComplete="new-password"
            value={repeat}
            onChange={(e) => setRepeat(e.target.value)}
            disabled={busy}
          />
        </label>
        {error && <p className="error">{error}</p>}
        {msg && <p className="muted">{msg}</p>}
        <button className="primary" disabled={!current || !next || !repeat || busy}>{t("common.save")}</button>
      </form>
    </>
  );
}
