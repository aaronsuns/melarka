import { useSyncExternalStore } from "react";
import type { VideoSource } from "./videoLink";

// The video sources the server has on (GET /info video_sources; an admin
// chooses them in Admin → System). Assumed ["youtube"] until /info answers:
// an older server without the field behaves as before.
let sources: VideoSource[] = ["youtube"];
const listeners = new Set<() => void>();

export function setVideoSources(next: VideoSource[]) {
  if (next.length === sources.length && next.every((s, i) => s === sources[i])) return;
  sources = next;
  listeners.forEach((fn) => fn());
}

function subscribe(fn: () => void) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function useVideoSources(): VideoSource[] {
  return useSyncExternalStore(subscribe, () => sources);
}

/** YouTube-only features (频道, 视频, For you) need YouTube on. */
export function useYouTubeOn(): boolean {
  return useVideoSources().includes("youtube");
}

const STORAGE_KEY = "lark.videoSource";

/** The source this browser last searched, if it is still on; else the first one on. */
export function preferredSource(on: VideoSource[]): VideoSource | null {
  let saved: string | null = null;
  try {
    saved = localStorage.getItem(STORAGE_KEY);
  } catch {
    /* storage unavailable: no memory */
  }
  return on.find((s) => s === saved) ?? on[0] ?? null;
}

export function rememberSource(s: VideoSource) {
  try {
    localStorage.setItem(STORAGE_KEY, s);
  } catch {
    /* storage unavailable */
  }
}
