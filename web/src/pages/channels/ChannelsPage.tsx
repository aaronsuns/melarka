import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useSearchParams } from "react-router";
import { api } from "../../api/client";
import type { ChannelGroup, Episode } from "../../api/types";
import { useVisiblePoll } from "../../channels/useVisiblePoll";
import { Cover } from "../../components/Cover";
import { PlusIcon } from "../../components/icons";
import { isSafeThumbnailUrl } from "../../format";
import { errorMessage } from "../../i18n/errors";
import { useT } from "../../i18n/i18n";
import { EpisodeList, PlayAll } from "./EpisodeList";
import { MyChannels } from "./MyChannels";
import { Suggestions } from "./Suggestions";

const TABS = ["latest", "channels", "kept", "mine", "suggested"] as const;
type Tab = (typeof TABS)[number];
const LABEL: Record<Tab, string> = { latest: "channels.latest", channels: "channels.byChannel", kept: "channels.kept", mine: "channels.mine", suggested: "channels.suggested" };
const RELOAD_MS = 30_000;

// 最新's sort and filter, remembered on this device.
type Sort = "newest" | "oldest";
const SORT_KEY = "lark.latestSort";
const UNPLAYED_KEY = "lark.latestUnplayed";
function readPref(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}
function writePref(key: string, v: string) {
  try {
    localStorage.setItem(key, v);
  } catch {
    /* not remembered; this visit still follows it */
  }
}

// Answers that arrive after the component is gone (or was asked again) are dropped.
function useLive() {
  const live = useRef(true);
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
    };
  }, []);
  return live;
}

function LatestList() {
  const t = useT();
  const [sort, setSortState] = useState<Sort>(() => (readPref(SORT_KEY) === "oldest" ? "oldest" : "newest"));
  const [unplayed, setUnplayedState] = useState(() => readPref(UNPLAYED_KEY) === "1");
  const [items, setItems] = useState<Episode[] | null>(null);
  const [err, setErr] = useState("");
  const live = useLive();
  const asked = useRef(0);
  // Episodes still downloading: look again now and then, while visible.
  const load = useCallback(() => {
    const n = ++asked.current;
    api.latestEpisodes({ order: sort === "oldest" ? "asc" : undefined, unplayed: unplayed ? 1 : undefined }).then(
      (r) => {
        if (!live.current || n !== asked.current) return;
        setItems(r.items);
        setErr("");
      },
      (e) => live.current && n === asked.current && setErr(errorMessage(e, "common.loadFailed")),
    );
  }, [sort, unplayed, live]);
  useVisiblePoll(load, RELOAD_MS, load);
  const setSort = (v: Sort) => {
    writePref(SORT_KEY, v);
    setSortState(v);
  };
  const setUnplayed = (v: boolean) => {
    writePref(UNPLAYED_KEY, v ? "1" : "0");
    setUnplayedState(v);
  };
  let body: ReactNode;
  if (err) body = <p className="error">{err}</p>;
  else if (!items) body = <p className="muted">{t("common.loading")}</p>;
  else if (items.length === 0) body = <p className="muted">{unplayed ? t("channels.noUnplayed") : t("channels.noLatest")}</p>;
  else body = <EpisodeList items={items} order={sort} />;
  return (
    <>
      <div className="list-tools">
        <div className="list-filters">
          <div className="seg" role="group" aria-label={t("channels.sort")}>
            {(["newest", "oldest"] as const).map((v) => (
              <button key={v} aria-pressed={sort === v} onClick={() => setSort(v)}>{t(v === "newest" ? "channels.sortNewest" : "channels.sortOldest")}</button>
            ))}
          </div>
          <label className="check"><input type="checkbox" className="switch" checked={unplayed} onChange={(e) => setUnplayed(e.target.checked)} />{t("channels.unplayedOnly")}</label>
        </div>
        {items && items.length > 0 && <PlayAll items={items} order={sort} />}
      </div>
      {body}
    </>
  );
}

const SECTION_ROWS = 3; // shown at first
const SECTION_MORE = 10; // shown expanded
const SECTION_QUEUE = 50; // fetched, for ▶ 全部播放 / 🔀 随机

/** 按频道: each followed channel with its latest episodes, ▶ 全部播放 / 🔀 随机 per channel. */
function ByChannelList() {
  const t = useT();
  const [groups, setGroups] = useState<ChannelGroup[] | null>(null);
  const [err, setErr] = useState("");
  const [open, setOpen] = useState<Set<string>>(() => new Set());
  const live = useLive();
  const load = useCallback(() => {
    api.episodesByChannel(SECTION_QUEUE).then(
      (g) => {
        if (!live.current) return;
        setGroups(g);
        setErr("");
      },
      (e) => live.current && setErr(errorMessage(e, "common.loadFailed")),
    );
  }, [live]);
  useVisiblePoll(load, RELOAD_MS);
  if (err) return <p className="error">{err}</p>;
  if (!groups) return <p className="muted">{t("common.loading")}</p>;
  if (groups.length === 0) return <p className="muted">{t("channels.noChannels")}</p>;
  const toggle = (id: string) =>
    setOpen((s) => {
      const n = new Set(s);
      if (!n.delete(id)) n.add(id);
      return n;
    });
  return (
    <>
      {groups.map((g) => {
        const c = g.channel;
        const expanded = open.has(c.id);
        return (
          <section key={c.id} className="channel-group">
            <div className="channel-group-head">
              <Cover seed={c.id} label={c.title} size={40} src={isSafeThumbnailUrl(c.avatar) ? c.avatar : undefined} noReferrer />
              <span className="yt-text">
                <h2 className="ellipsis"><Link to={`/channels/${c.id}`}>{c.title}</Link></h2>
                {g.unplayed > 0 && <span className="muted small">{t("channels.unplayed", { count: g.unplayed })}</span>}
              </span>
            </div>
            {g.episodes.length === 0 ? (
              <p className="muted small">{t("channels.noEpisodesYet")}</p>
            ) : (
              <>
                <PlayAll items={g.episodes} />
                <EpisodeList items={g.episodes.slice(0, expanded ? SECTION_MORE : SECTION_ROWS)} queueFrom={g.episodes} />
                {g.episodes.length > SECTION_ROWS && (
                  <button className="link" aria-expanded={expanded} onClick={() => toggle(c.id)}>{expanded ? t("episodes.less") : t("episodes.more")}</button>
                )}
              </>
            )}
          </section>
        );
      })}
    </>
  );
}

function KeptList() {
  const t = useT();
  const [items, setItems] = useState<Episode[] | null>(null);
  const [err, setErr] = useState("");
  useEffect(() => {
    api.keptEpisodes().then(setItems, (e) => setErr(errorMessage(e, "common.loadFailed")));
  }, []);
  if (err) return <p className="error">{err}</p>;
  if (!items) return <p className="muted">{t("common.loading")}</p>;
  if (items.length === 0) return <p className="muted">{t("channels.noKept")}</p>;
  return <EpisodeList items={items} />;
}

export default function ChannelsPage() {
  const t = useT();
  const [params, setParams] = useSearchParams();
  const tab: Tab = (TABS as readonly string[]).includes(params.get("tab") ?? "") ? (params.get("tab") as Tab) : "latest";
  // On a phone the tabs scroll sideways: keep the selected one in view (推荐 is
  // the last). Only the strip scrolls (scrollIntoView would move the page too).
  const selected = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const el = selected.current;
    const strip = el?.parentElement;
    if (!el || !strip) return;
    const left = el.offsetLeft - strip.offsetLeft;
    // FADE: the strip's soft right edge, which the selected tab is kept clear of.
    const FADE = 28;
    if (left < strip.scrollLeft) strip.scrollLeft = left;
    else if (left + el.offsetWidth + FADE > strip.scrollLeft + strip.clientWidth) strip.scrollLeft = left + el.offsetWidth + FADE - strip.clientWidth;
  }, [tab]);
  return (
    <>
      <div className="page-head">
        <h1 className="page-title">{t("channels.title")}</h1>
        <Link className="secondary head-action" to="/channels/add" aria-label={t("channels.add")}>
          <PlusIcon />
          <span className="wide-only">{t("channels.add")}</span>
          <span className="narrow-only">{t("channels.addShort")}</span>
        </Link>
      </div>
      <div className="pill-tabs" role="tablist">
        {TABS.map((k) => (
          <button key={k} ref={tab === k ? selected : undefined} role="tab" aria-selected={tab === k} onClick={() => setParams(k === "latest" ? {} : { tab: k })}>{t(LABEL[k])}</button>
        ))}
      </div>
      {tab === "latest" && <LatestList />}
      {tab === "channels" && <ByChannelList />}
      {tab === "kept" && <KeptList />}
      {tab === "mine" && <MyChannels />}
      {tab === "suggested" && <Suggestions />}
    </>
  );
}
