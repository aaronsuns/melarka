import { useEffect, useState } from "react";
import { useParams } from "react-router";
import { api } from "../../api/client";
import type { ChannelPageData } from "../../api/types";
import { Cover } from "../../components/Cover";
import { FollowButton } from "../../components/FollowButton";
import { PreviewButton } from "../../components/PreviewButton";
import { isSafeThumbnailUrl } from "../../format";
import { errorMessage } from "../../i18n/errors";
import { useT } from "../../i18n/i18n";
import { EpisodeList, PlayAll } from "./EpisodeList";

export default function ChannelPage() {
  const t = useT();
  const { id = "" } = useParams();
  const [data, setData] = useState<ChannelPageData | null>(null);
  const [err, setErr] = useState("");
  useEffect(() => {
    setData(null);
    setErr("");
    let live = true;
    api.channelPage(id).then((d) => live && setData(d), (e) => live && setErr(errorMessage(e, "common.loadFailed")));
    return () => {
      live = false;
    };
  }, [id]);
  if (err) return <p className="error">{err}</p>;
  if (!data) return <p className="muted">{t("common.loading")}</p>;
  const c = data.channel;
  return (
    <>
      <div className="channel-head">
        <Cover seed={c.id} label={c.title} size={72} src={isSafeThumbnailUrl(c.avatar) ? c.avatar : undefined} noReferrer />
        <div className="yt-text">
          <h1 className="page-title">{c.title}</h1>
          {c.handle && <span className="muted small">{c.handle}</span>}
        </div>
        <FollowButton channelId={c.id} following={data.following !== null} />
      </div>
      {c.description && <p className="muted small clamp-3">{c.description}</p>}
      {data.episodes.some((e) => e.audio?.status === "done") && <PlayAll items={data.episodes} />}
      <EpisodeList
        items={data.episodes}
        extra={(ep) =>
          ep.audio?.status !== "done" && (
            <PreviewButton target={{ videoId: ep.video_id, media: "audio", title: ep.title, channel: ep.channel_title, durationS: ep.duration_s, keepTo: "channel" }} />
          )
        }
      />
    </>
  );
}
