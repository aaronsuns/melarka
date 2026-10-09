import { useState, type FormEvent } from "react";
import { BRAND } from "../brand";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { useAuth } from "./AuthProvider";

export default function LoginPage() {
  const { login } = useAuth();
  const t = useT();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await login(username.trim(), password);
    } catch (err) {
      setError(errorMessage(err, "login.failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="login">
      <h1 className="login-title">{BRAND}</h1>
      <form onSubmit={submit} className="login-form">
        <label>{t("login.username")}<input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" autoCapitalize="none" required /></label>
        <label>{t("login.password")}<input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required /></label>
        {error && <p className="error" role="alert">{error}</p>}
        <button type="submit" className="primary" disabled={busy}>{t("login.signIn")}</button>
      </form>
    </main>
  );
}
