import { useEffect, useState } from "react";
import { Link } from "react-router";
import { api } from "../../api/client";
import type { MyChannels as Data } from "../../api/types";
import { Cover } from "../../components/Cover";
import { formatSize, isSafeThumbnailUrl } from "../../format";
import { errorMessage } from "../../i18n/errors";
import { useT } from "../../i18n/i18n";
import { ChannelSettingsForm } from "./ChannelSettingsForm";

export function MyChannels() {
  const t = useT();
  const [data, setData] = useState<Data | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [err, setErr] = useState(""); // the list couldn't be loaded
  const [rowErr, setRowErr] = useState<{ id: string; msg: string } | null>(null); // an unfollow failed
  useEffect(() => {
    api.myChannels().then(setData, (e) => setErr(errorMessage(e, "common.loadFailed")));
  }, []);
  async function unfollow(id: string, title: string) {
    if (!window.confirm(t("channels.unfollowConfirm", { name: title }))) return;
    setRowErr(null);
    try {
      await api.unfollowChannel(id);
      setData((d) => d && { ...d, channels: d.channels.filter((c) => c.channel.id !== id) });
    } catch (e) {
      setRowErr({ id, msg: errorMessage(e, "common.actionFailed") });
    }
  }
  if (err) return <p className="error">{err}</p>;
  if (!data) return <p className="muted">{t("common.loading")}</p>;
  return (
    <>
      <p className="muted small">{t("channels.usage", { used: formatSize(data.usage.bytes), max: formatSize(data.usage.max_bytes) })}</p>
      {data.channels.length === 0 && <p className="muted">{t("channels.noChannels")}</p>}
      <ul className="rows">
        {data.channels.map((c) => (
          <li key={c.channel.id} className="channel-item">
            <div className="row">
              <Cover seed={c.channel.id} label={c.channel.title} size={48} src={isSafeThumbnailUrl(c.channel.avatar) ? c.channel.avatar : undefined} noReferrer />
              <Link to={`/channels/${c.channel.id}`} className="yt-text">
                <span className="ellipsis">{c.channel.title}</span>
                {c.unplayed > 0 && <span className="muted small">{t("channels.unplayed", { count: c.unplayed })}</span>}
              </Link>
              <span className="job-actions">
                <button className="secondary" aria-expanded={open === c.channel.id} onClick={() => setOpen(open === c.channel.id ? null : c.channel.id)}>{t("channels.settings")}</button>
              </span>
            </div>
            {open === c.channel.id && (
              <>
                <ChannelSettingsForm channelId={c.channel.id} initial={c.settings} defaultKeepDays={data.default_keep_days} />
                <button className="secondary danger" onClick={() => unfollow(c.channel.id, c.channel.title)}>{t("channels.unfollow")}</button>
                {rowErr?.id === c.channel.id && <p className="error small" role="alert">{rowErr.msg}</p>}
              </>
            )}
          </li>
        ))}
      </ul>
    </>
  );
}
