import { useEffect, useId, useRef, useState } from "react";
import { Link } from "react-router";
import { api } from "../api/client";
import type { OnOpen, Quality } from "../api/types";
import { useAuth } from "../auth/AuthProvider";
import { usePlayer } from "../player/PlayerProvider";
import { usePrefs } from "../prefs/PrefsProvider";
import { hasNative, nativePost } from "../native/bridge";
import { OfflineSettings } from "./OfflineSettings";
import { getServerDefault, LANGUAGE_NAMES, LOCALES, useT, type Locale } from "../i18n/i18n";

const isIOSSafari =
  typeof navigator !== "undefined" &&
  /iPhone|iPad/.test(navigator.userAgent) &&
  !("standalone" in navigator && (navigator as { standalone?: boolean }).standalone);

export default function SettingsPage() {
  const { user, logout } = useAuth();
  const p = usePlayer();
  const { prefs, save } = usePrefs();
  const t = useT();
  const loggingOut = useRef(false);
  const carHint = useId();
  const loudnessHint = useId();
  const nativeHint = useId();
  // Inside the Lark iPhone app: its own settings (cache, server) replace the
  // web's offline cache and the Home Screen tip.
  const [native] = useState(hasNative);
  // The server's version, for the footer; the line is hidden without one.
  const [version, setVersion] = useState<string>();
  useEffect(() => {
    let live = true;
    api.info().then((i) => { if (live) setVersion(i.version); }).catch(() => {});
    return () => { live = false; };
  }, []);

  async function handleLogout() {
    if (loggingOut.current) return;
    loggingOut.current = true;
    p.pause();
    try {
      // Send buffered play events while the session is still valid (they
      // stay stored under this user's key if it takes too long).
      let timer: ReturnType<typeof setTimeout> | undefined;
      await Promise.race([p.flushEvents(), new Promise((r) => (timer = setTimeout(r, 3000)))]);
      clearTimeout(timer);
      await logout();
    } finally {
      loggingOut.current = false;
    }
  }

  const server = getServerDefault();
  return (
    <>
      <h1 className="page-title">{t("nav.me")}</h1>
      <p className="muted">
        {t("settings.signedInAs", { name: user?.username ?? "" })}
        {user?.role === "admin" ? t("settings.adminSuffix") : ""}
      </p>
      <label className="field">
        {t("settings.quality")}
        <select aria-label={t("settings.quality")} value={p.quality} onChange={(e) => p.setQuality(e.target.value as Quality)}>
          <option value="lossless">{t("settings.quality.lossless")}</option>
          <option value="high">{t("settings.quality.high")}</option>
          <option value="saver">{t("settings.quality.saver")}</option>
        </select>
      </label>
      <label className="field">
        {t("settings.language")}
        <select
          aria-label={t("settings.language")}
          value={prefs.language ?? ""}
          onChange={(e) => void save({ ...prefs, language: (e.target.value || null) as Locale | null }).catch(() => {})}
        >
          <option value="">{t("settings.languageServerDefault", { name: LANGUAGE_NAMES[server ?? "en"] })}</option>
          {LOCALES.map((l) => (
            <option key={l} value={l}>
              {LANGUAGE_NAMES[l]}
            </option>
          ))}
        </select>
      </label>
      <label className="field">
        {t("settings.onOpen")}
        <select
          aria-label={t("settings.onOpen")}
          value={prefs.on_open}
          onChange={(e) => void save({ ...prefs, on_open: e.target.value as OnOpen }).catch(() => {})}
        >
          <option value="shuffle_favorites">{t("settings.onOpen.shuffle_favorites")}</option>
          <option value="resume">{t("settings.onOpen.resume")}</option>
          <option value="nothing">{t("settings.onOpen.nothing")}</option>
        </select>
      </label>
      <div className="field field-check">
        <label className="field-check-row">
          <input
            type="checkbox"
            aria-describedby={carHint}
            checked={prefs.car_lyrics !== false}
            onChange={(e) => void save({ ...prefs, car_lyrics: e.target.checked }).catch(() => {})}
          />
          {t("settings.carLyrics")}
        </label>
        <span id={carHint} className="small">{t("settings.carLyricsHint")}</span>
      </div>
      <div className="field field-check">
        <label className="field-check-row">
          <input
            type="checkbox"
            aria-describedby={loudnessHint}
            checked={prefs.normalize_loudness !== false}
            onChange={(e) => void save({ ...prefs, normalize_loudness: e.target.checked }).catch(() => {})}
          />
          {t("settings.loudness")}
        </label>
        <span id={loudnessHint} className="small">{t("settings.loudnessHint")}</span>
      </div>
      {isIOSSafari && !native && <p className="muted small">{t("settings.iosTip")}</p>}
      {native ? (
        <div className="field">
          <button type="button" className="settings-link" aria-describedby={nativeHint} onClick={() => nativePost({ type: "openSettings" })}>
            {t("settings.nativeApp")}
          </button>
          <span id={nativeHint} className="small">{t("settings.nativeAppHint")}</span>
        </div>
      ) : (
        <OfflineSettings />
      )}
      {user?.role === "admin" && <Link to="/admin" className="settings-link">{t("settings.admin")}</Link>}
      <Link to="/change-password" className="settings-link">{t("settings.changePassword")}</Link>
      <Link to="/downloads" className="settings-link">{t("settings.myDownloads")}</Link>
      <button className="secondary" onClick={() => void handleLogout()}>{t("settings.logout")}</button>
      {/* The source offer (AGPL §13) is always shown; the version only when the server reports one. */}
      <p className="muted small">
        {version && <>{t("settings.version", { v: version })} · </>}
        <a href="https://github.com/aaronsuns/melarka" target="_blank" rel="noreferrer">{t("settings.source")}</a>
      </p>
    </>
  );
}
