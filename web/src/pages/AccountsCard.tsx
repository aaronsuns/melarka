import { useEffect, useState, type FormEvent } from "react";
import { api, type LinkedAccount } from "../api/client";
import { useAuth } from "../auth/AuthProvider";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";

// Settings → Accounts: the quick account switch (docs/superpowers/specs/quick-account-switch-design.md).
// Every account signed in on this device is one tap away, with no password; "Add account" signs in to
// one more. `beforeLeave` stops playback and sends the play events of the account being left.
export function AccountsCard({ beforeLeave }: { beforeLeave: () => Promise<void> }) {
  const t = useT();
  const { user, addAccount, switchAccount } = useAuth();
  const [accounts, setAccounts] = useState<LinkedAccount[]>([]);
  const [adding, setAdding] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let live = true;
    api.accounts().then((a) => { if (live) setAccounts(a); }).catch(() => {});
    return () => { live = false; };
  }, []);

  async function run(fn: () => Promise<void>, fallback: string) {
    setBusy(true);
    setError("");
    try {
      await fn();
    } catch (e) {
      setError(errorMessage(e, fallback));
      setBusy(false);
    }
    // On success the app remounts for the new account, so this card is gone.
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    void run(async () => {
      await beforeLeave();
      await addAccount(username.trim(), password);
    }, "login.failed");
  }

  async function remove(a: LinkedAccount) {
    setError("");
    try {
      await api.removeAccount(a.user_id);
      setAccounts((xs) => xs.filter((x) => x.user_id !== a.user_id));
    } catch (e) {
      setError(errorMessage(e, "accounts.failed"));
    }
  }

  const others = accounts.filter((a) => !a.current && a.user_id !== user?.id);
  return (
    <section className="settings-group accounts" aria-label={t("accounts.title")}>
      {others.map((a) => (
        <div key={a.user_id} className="field account-row">
          <button
            type="button"
            className="account-switch"
            disabled={busy}
            onClick={() => void run(async () => { await beforeLeave(); await switchAccount(a.user_id); }, "accounts.failed")}
          >
            {t("accounts.switchTo", { name: a.username })}
          </button>
          <button type="button" className="link small" disabled={busy} onClick={() => void remove(a)} aria-label={t("accounts.remove", { name: a.username })}>
            {t("accounts.removeShort")}
          </button>
        </div>
      ))}
      {adding ? (
        <form className="field account-add" onSubmit={submit}>
          <label>{t("login.username")}<input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" autoCapitalize="none" required /></label>
          <label>{t("login.password")}<input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="off" required /></label>
          <div className="actions">
            <button type="submit" className="primary" disabled={busy}>{t("accounts.addAndSwitch")}</button>
            <button type="button" className="secondary" disabled={busy} onClick={() => { setAdding(false); setError(""); }}>{t("common.cancel")}</button>
          </div>
        </form>
      ) : (
        <div className="field">
          <button type="button" className="account-switch" onClick={() => setAdding(true)}>{t("accounts.add")}</button>
          <span className="small">{t("accounts.hint")}</span>
        </div>
      )}
      {error && <p className="error" role="alert">{error}</p>}
    </section>
  );
}
