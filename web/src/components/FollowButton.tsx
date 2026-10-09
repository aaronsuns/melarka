import { useEffect, useState } from "react";
import { api } from "../api/client";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { FollowIcon, FollowingIcon } from "./icons";

/**
 * 关注 / 已关注 for one channel; tapping 已关注 unfollows. `label` is a longer
 * name for 关注 (关注频道 on a video row): its name for screen readers, and
 * what wide screens show — a phone shows the short 关注. With `onError` the
 * caller shows a failure (e.g. in the row's text) and only the button renders.
 * `icon`: a small round outlined button (a person with + / ✓) for result rows.
 */
export function FollowButton({ channelId, following, onChange, label, onError, icon }: { channelId: string; following: boolean; onChange?: (f: boolean) => void; label?: string; onError?: (msg: string) => void; icon?: boolean }) {
  const t = useT();
  const [on, setOn] = useState(following);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  // The caller learned the real state later (a list loaded, a page reloaded).
  useEffect(() => setOn(following), [following]);
  async function toggle() {
    setBusy(true);
    setErr("");
    onError?.("");
    try {
      if (on) await api.unfollowChannel(channelId);
      else await api.followChannel({ id: channelId });
      setOn(!on);
      onChange?.(!on);
    } catch (e) {
      const msg = errorMessage(e, "common.actionFailed");
      if (onError) onError(msg);
      else setErr(msg);
    } finally {
      setBusy(false);
    }
  }
  const name = on ? t("channels.following") : (label ?? t("channels.follow"));
  const button = icon ? (
    <button className={on ? "icon-btn outline follow on" : "icon-btn outline follow"} disabled={busy} aria-pressed={on} aria-label={name} title={name} onClick={toggle}>
      {on ? <FollowingIcon size={16} /> : <FollowIcon size={16} />}
    </button>
  ) : (
    <button className={on ? "secondary" : "secondary primary"} disabled={busy} aria-pressed={on} aria-label={!on && label ? label : undefined} onClick={toggle}>
      {on ? (
        t("channels.following")
      ) : label ? (
        <>
          <span className="wide-only">{label}</span>
          <span className="narrow-only">{t("channels.follow")}</span>
        </>
      ) : (
        t("channels.follow")
      )}
    </button>
  );
  if (onError) return button;
  return (
    <span className="job-actions">
      {err && <span className="error small">{err}</span>}
      {button}
    </span>
  );
}
