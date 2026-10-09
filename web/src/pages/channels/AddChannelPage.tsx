import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { api } from "../../api/client";
import type { ChannelHit } from "../../api/types";
import { Cover } from "../../components/Cover";
import { FollowButton } from "../../components/FollowButton";
import { isSafeThumbnailUrl } from "../../format";
import { searchErrorText } from "../../i18n/errors";
import { useT } from "../../i18n/i18n";

const looksLikeLink = (s: string) => /^(https?:\/\/|www\.|youtube\.com|youtu\.be|m\.youtube\.com)/i.test(s.trim());

/** Find a channel: paste a channel / handle / video link, or search by name. */
export default function AddChannelPage() {
  const t = useT();
  const [q, setQ] = useState("");
  const [hits, setHits] = useState<ChannelHit[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  async function find(e: FormEvent) {
    e.preventDefault();
    const text = q.trim();
    if (!text) return;
    setBusy(true);
    setErr("");
    setHits(null);
    try {
      if (looksLikeLink(text)) {
        const url = /^https?:\/\//i.test(text) ? text : `https://${text}`;
        setHits([await api.resolveChannel(url)]);
      } else {
        setHits(await api.searchChannels(text));
      }
    } catch (e) {
      setErr(searchErrorText(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <h1 className="page-title">{t("channels.add")}</h1>
      <form className="search-form" onSubmit={find}>
        <input aria-label={t("channels.searchLabel")} placeholder={t("channels.searchLabel")} value={q} onChange={(e) => setQ(e.target.value)} />
        <button className="secondary" disabled={busy}>{t("channels.find")}</button>
      </form>
      {err && <p className="error">{err}</p>}
      {hits && hits.length === 0 && <p className="muted">{t("channels.noResults")}</p>}
      {hits && hits.length > 0 && (
        <ul className="rows">
          {hits.map((h) => (
            <li key={h.id} className="row channel-card">
              <Cover seed={h.id} label={h.title} size={48} src={isSafeThumbnailUrl(h.avatar) ? h.avatar : undefined} noReferrer />
              <Link to={`/channels/${h.id}`} className="yt-text">
                <span className="ellipsis">{h.title}</span>
                <span className="ellipsis muted small">
                  {h.handle}
                  {h.followers > 0 && ` · ${t("channels.followers", { count: h.followers })}`}
                </span>
                {h.description && <span className="ellipsis muted small">{h.description}</span>}
              </Link>
              <FollowButton channelId={h.id} following={h.following} />
            </li>
          ))}
        </ul>
      )}
    </>
  );
}
