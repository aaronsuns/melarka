import { useState } from "react";
import { Link } from "react-router";
import { api } from "../api/client";
import type { ListRef } from "../api/types";
import { errorMessage } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { playlistUrl } from "../youtubeLink";

const MAX_LIST = 200; // the server downloads at most this many entries of a list

type State =
  | { kind: "idle" }
  | { kind: "loading" } // fetching the list's length
  | { kind: "confirm"; count: number; name: string }
  | { kind: "posting"; count: number; name: string }
  | { kind: "done"; count: number; ref: ListRef | null; name: string }
  | { kind: "error"; message: string; again: () => void };

/**
 * "Download all" for a YouTube list: asks the server for the list's length,
 * confirms, then queues it. The server turns the list into a Lark playlist.
 */
export function ListDownloadButton({ listId, title, label }: { listId: string; title?: string; label?: string }) {
  const t = useT();
  const [state, setState] = useState<State>({ kind: "idle" });

  async function lookUp() {
    setState({ kind: "loading" });
    try {
      const info = await api.youtubePlaylist(listId);
      setState({ kind: "confirm", count: info.count, name: info.title || title || "" });
    } catch (e) {
      setState({ kind: "error", message: errorMessage(e, "yt.downloadFailed"), again: () => void lookUp() });
    }
  }

  async function post(count: number, name: string) {
    setState({ kind: "posting", count, name });
    try {
      const res = await api.enqueue({ url: playlistUrl(listId) });
      setState({ kind: "done", count: res.jobs.length, ref: res.playlist ?? null, name: res.playlist?.name ?? name });
    } catch (e) {
      setState({ kind: "error", message: errorMessage(e, "yt.downloadFailed"), again: () => void post(count, name) });
    }
  }

  switch (state.kind) {
    case "idle":
      return <button className="secondary" onClick={() => void lookUp()}>{label ?? t("yt.downloadAll")}</button>;
    case "loading":
      return <button className="secondary" disabled>{label ?? t("yt.downloadAll")}</button>;
    case "confirm":
    case "posting": {
      const { count, name } = state;
      const posting = state.kind === "posting";
      const text =
        count > MAX_LIST
          ? t("yt.downloadFirst", { max: MAX_LIST, count })
          : count > 0
            ? t("yt.downloadAllCount", { count })
            : t("yt.downloadAll");
      return (
        <span className="job-actions">
          <button className="primary" disabled={posting} onClick={() => void post(count, name)}>{text}</button>
          <button className="secondary" disabled={posting} onClick={() => setState({ kind: "idle" })}>{t("common.cancel")}</button>
        </span>
      );
    }
    case "done":
      return (
        <span className="list-added">
          <span className="muted small">{t("yt.addedToPlaylist", { count: state.count, name: state.name })}</span>
          {state.ref && <Link to={`/playlists/${state.ref.id}`}>{t("yt.openPlaylist")}</Link>}
        </span>
      );
    case "error":
      return (
        <span className="job-actions">
          <span className="error small">{state.message}</span>
          <button className="secondary" onClick={state.again}>{t("common.retry")}</button>
        </span>
      );
  }
}
