// The bridge to the Lark iPhone app (lark-ios). Inside the app the page runs
// in a WKWebView that injects `window.larkNative = { version, post }`; the
// web posts the messages below, and native answers with `lark-native`
// CustomEvents on window. The field names are the wire protocol of
// lark-ios Sources/Bridge/WebMessage.swift and NativeEvent.swift: change
// them only together.
import type { Episode, OnOpen, Quality, Track } from "../api/types";
import type { QueueState } from "../player/queue";

export type ItemKind = "track" | "episode";

// One queue entry. `meta` is the web's own Track or Episode object: native
// stores it and echoes it back in `queue` events, so the web renders its
// existing components from it.
export interface NativeItem {
  kind: ItemKind;
  id: string;
  title: string;
  artist: string;
  album: string;
  durationMs: number;
  meta: Track | Episode;
}

export type QueueSource = QueueState["source"];

export type ToNative =
  | { type: "hello"; onOpen: OnOpen }
  | { type: "setQueue"; kind: ItemKind; items: NativeItem[]; index: number; positionMs?: number; play: boolean; source: QueueSource }
  | { type: "play" | "next" | "prev"; kind: ItemKind }
  | { type: "pause"; kind?: ItemKind }
  | { type: "seek" | "skip"; kind: ItemKind; ms: number }
  | { type: "setRate"; rate: number }
  | { type: "stop"; kind: ItemKind }
  | { type: "setPrefs"; quality: Quality; carLyrics: boolean }
  | { type: "auth"; signedIn: boolean; userId?: number }
  | { type: "pauseForWeb" }
  | { type: "favoriteChanged"; trackId: number; on: boolean }
  | { type: "flushEvents"; id: string }
  | { type: "openSettings" };

export interface NativeState {
  type: "state";
  kind: ItemKind;
  itemId: string | null;
  index: number;
  playing: boolean;
  positionMs: number;
  durationMs: number;
  buffering: boolean;
  error: string | null;
  rate: number;
}

export type FromNative =
  | NativeState
  | { type: "queue"; kind: ItemKind; items: NativeItem[]; index: number; source: QueueSource }
  | { type: "flushed"; id: string }
  | { type: "authRequired" }
  | { type: "notice"; text: string };

interface LarkNative {
  version: number;
  post(msg: unknown): void;
}

declare global {
  interface Window {
    larkNative?: LarkNative;
  }
}

export const NATIVE_EVENT = "lark-native";

/** True inside the Lark iPhone app. */
export function hasNative(): boolean {
  return typeof window !== "undefined" && !!window.larkNative;
}

/** Posts a message to native; a no-op without the app. */
export function nativePost(m: ToNative): void {
  if (!hasNative()) return;
  // Plain JSON only: an `undefined` member (an absent positionMs or userId,
  // or anywhere inside a Track/Episode `meta`) must be a missing key on the
  // wire, never something WebKit serializes and native's decoder rejects.
  try {
    window.larkNative!.post(JSON.parse(JSON.stringify(m)));
  } catch {
    /* the web view is going away */
  }
}

/** Listens to native's events; returns the unsubscribe. */
export function onNative(fn: (m: FromNative) => void): () => void {
  if (typeof window === "undefined") return () => {};
  const h = (e: Event) => {
    const d = (e as CustomEvent<unknown>).detail;
    if (d && typeof d === "object" && typeof (d as { type?: unknown }).type === "string") fn(d as FromNative);
  };
  window.addEventListener(NATIVE_EVENT, h);
  return () => window.removeEventListener(NATIVE_EVENT, h);
}
