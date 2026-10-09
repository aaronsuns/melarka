import { useState } from "react";
import { api } from "../../api/client";
import type { ChannelSettings } from "../../api/types";
import { errorMessage } from "../../i18n/errors";
import { useT } from "../../i18n/i18n";

const KEEP_CHOICES = [3, 7, 15, 30, 90, 365];

/** One followed channel's settings: what to download, how long to keep, pause, Shorts, live replays. */
export function ChannelSettingsForm({ channelId, initial, defaultKeepDays }: { channelId: string; initial: ChannelSettings; defaultKeepDays: number }) {
  const t = useT();
  const [s, setS] = useState(initial);
  const [msg, setMsg] = useState("");
  async function save() {
    setMsg("");
    try {
      setS(await api.channelSettings(channelId, s));
      setMsg(t("channels.saved"));
    } catch (e) {
      setMsg(errorMessage(e, "common.actionFailed"));
    }
  }
  return (
    <div className="channel-settings">
      <fieldset>
        <legend>{t("channels.media")}</legend>
        {(["audio", "video"] as const).map((m) => (
          <label key={m} className="radio">
            <input type="radio" name={`media-${channelId}`} checked={s.media === m} onChange={() => setS({ ...s, media: m })} />
            {t(`channels.media.${m}`)}
          </label>
        ))}
      </fieldset>
      <div className="field">
        <label htmlFor={`keep-${channelId}`}>{t("channels.keepDays")}</label>
        <select id={`keep-${channelId}`} value={s.keep_days ?? ""} onChange={(e) => setS({ ...s, keep_days: e.target.value === "" ? null : Number(e.target.value) })}>
          <option value="">{t("channels.keepDaysDefault", { days: defaultKeepDays })}</option>
          {KEEP_CHOICES.map((d) => <option key={d} value={d}>{t("channels.days", { count: d })}</option>)}
        </select>
      </div>
      <label className="check"><input type="checkbox" className="switch" checked={s.paused} onChange={(e) => setS({ ...s, paused: e.target.checked })} />{t("channels.paused")}</label>
      <label className="check"><input type="checkbox" className="switch" checked={s.include_shorts} onChange={(e) => setS({ ...s, include_shorts: e.target.checked })} />{t("channels.includeShorts")}</label>
      <label className="check"><input type="checkbox" className="switch" checked={s.include_live} onChange={(e) => setS({ ...s, include_live: e.target.checked })} />{t("channels.includeLive")}</label>
      <button className="secondary" onClick={save}>{t("common.save")}</button>
      {msg && <span className="muted small" role="status">{msg}</span>}
    </div>
  );
}
