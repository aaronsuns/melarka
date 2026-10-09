import { useEffect, useRef, useState } from "react";
import { api } from "../api/client";
import type { PreviewInfo } from "../api/types";
import { PREVIEW_POLL_MS, type PreviewTarget } from "../channels/PreviewProvider";
import { failedPreviewText, isTransient, previewErrorText } from "../i18n/errors";
import { useT } from "../i18n/i18n";
import { CheckIcon, DownloadIcon, ProgressRing } from "./icons";

// A request that failed without a reason is asked again, 2 s, 4 s, … at most 30 s apart.
const MAX_BACKOFF_MS = 30_000;

class Gone extends Error {}

/**
 * 下载 for channel content: fetched once (as a preview) and kept in Channels —
 * never into the music library. With `onError` the caller shows the error
 * (in the row's text, so the actions never grow) and only the button renders.
 * A round icon: ⬇, a spinner while it is fetched, ✓ once kept, ⬇ with ! after a failure.
 */
export function ChannelDownloadButton({ target, onError }: { target: Omit<PreviewTarget, "keepTo" | "media" | "listenInstead">; onError?: (msg: string) => void }) {
  const t = useT();
  const [state, setState] = useState<"idle" | "busy" | "done" | "error">("idle");
  const [err, setErr] = useState("");
  const live = useRef(true);
  const cleanups = useRef(new Set<() => void>());
  useEffect(() => {
    live.current = true;
    const set = cleanups.current;
    return () => {
      live.current = false;
      set.forEach((fn) => fn());
      set.clear();
    };
  }, []);

  // Waits ms, then (if the page is hidden) until it is visible again; throws Gone once unmounted.
  function pause(ms: number): Promise<void> {
    return new Promise((resolve, reject) => {
      const done = () => {
        cleanups.current.delete(cancel);
        if (live.current) resolve();
        else reject(new Gone());
      };
      const onVis = () => {
        if (document.visibilityState === "hidden") return;
        document.removeEventListener("visibilitychange", onVis);
        done();
      };
      const timer = setTimeout(() => {
        if (document.visibilityState === "hidden") document.addEventListener("visibilitychange", onVis);
        else done();
      }, ms);
      const cancel = () => {
        clearTimeout(timer);
        document.removeEventListener("visibilitychange", onVis);
        reject(new Gone());
      };
      cleanups.current.add(cancel);
    });
  }

  // One request, asked again after a transient failure.
  async function patiently<T>(fn: () => Promise<T>): Promise<T> {
    for (let wait = PREVIEW_POLL_MS; ; wait = Math.min(wait * 2, MAX_BACKOFF_MS)) {
      try {
        return await fn();
      } catch (e) {
        if (!isTransient(e)) throw e;
        await pause(wait);
      }
    }
  }

  function fail(msg: string) {
    setErr(onError ? "" : msg);
    onError?.(msg);
    setState("error");
  }

  async function run() {
    setState("busy");
    setErr("");
    onError?.("");
    try {
      let p: PreviewInfo = await patiently(() =>
        api.startPreview({ video_id: target.videoId, media: "audio", title: target.title, channel: target.channel, duration_s: target.durationS }),
      );
      // One status read right away (a fast download needs no wait), then every 2 s while visible.
      if (p.status === "downloading") {
        const id = p.id;
        p = await patiently(() => api.preview(id));
      }
      while (p.status === "downloading") {
        await pause(PREVIEW_POLL_MS);
        const id = p.id;
        p = await patiently(() => api.preview(id));
      }
      if (p.status === "failed") {
        if (live.current) fail(failedPreviewText(p.error));
        return;
      }
      const id = p.id;
      await patiently(() => api.keepPreview(id, "channel"));
      if (live.current) setState("done");
    } catch (e) {
      if (e instanceof Gone || !live.current) return; // the row is gone: the preview just expires
      fail(previewErrorText(e));
    }
  }

  if (state === "done") {
    return (
      <span className="icon-btn state" title={t("preview.keptChannel")}>
        <CheckIcon />
        <span className="sr-only">{t("preview.keptChannel")}</span>
      </span>
    );
  }
  const button =
    state === "busy" ? (
      <button className="icon-btn filled busy" disabled aria-label={t("preview.preparing")} title={t("preview.preparing")}><ProgressRing pct={null} /></button>
    ) : (
      <button className="icon-btn filled" aria-label={t("yt.download")} title={t("yt.download")} onClick={run}>
        <DownloadIcon />
        {state === "error" && <span className="icon-badge" aria-hidden="true">!</span>}
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
