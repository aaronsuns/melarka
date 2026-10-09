// Without an offline cache (no service worker, the kill switch, storage
// full) the next track is fetched whole into memory while the current one
// plays, and played from a blob: URL — so a locked iPhone's track change
// never waits on the network. One track at a time, never while the current
// one is buffering.

export interface PreloadDeps {
  fetch(url: string, init: RequestInit): Promise<Response>;
  busy(): boolean; // the player is buffering the current track (call busyChanged() when that changes)
  createURL(b: Blob): string;
  revokeURL(u: string): void;
}

import type { Track } from "../api/types";

// The page holds the file in memory: iOS kills a tab that grows too big.
export const BLOB_MAX_BYTES = 30 * 1024 * 1024;
const MAX_BYTES = BLOB_MAX_BYTES;

/** Roughly what the "high" tier sends for a track (as the offline cache estimates). */
export function estimateHighBytes(t: Pick<Track, "duration_ms" | "bitrate" | "codec">): number {
  const kbps = (t.codec === "mp3" || t.codec === "aac") && t.bitrate > 0 && t.bitrate <= 320 ? t.bitrate : 256;
  return Math.round((t.duration_ms / 1000) * kbps * 125);
}

interface Slot {
  id: number;
  url: string;
}

export class BlobPreloader {
  private want: Slot | null = null; // to fetch (url: the stream url)
  private ready: Slot | null = null; // fetched (url: the blob url)
  private inUse: Slot | null = null; // loaded in the element
  private ac: AbortController | null = null;
  private failed = new Set<string>();

  constructor(private deps: PreloadDeps) {}

  /** The player started or stopped buffering: pause a fetch in flight, or carry on. */
  busyChanged(): void {
    this.kick();
  }

  /** Have this track (the next one) ready in memory. */
  request(id: number, url: string): void {
    if (this.ready?.id === id || this.want?.id === id || this.inUse?.id === id) return;
    this.cancel();
    if (this.failed.has(url)) return;
    this.want = { id, url };
    this.kick();
  }

  has(id: number): boolean {
    return this.ready?.id === id || this.inUse?.id === id;
  }

  /** The track is being loaded: its blob URL if it was preloaded, else null. Releases the previous one. */
  take(id: number): string | null {
    if (this.inUse?.id === id) return this.inUse.url;
    const prev = this.inUse;
    this.inUse = null;
    if (this.ready?.id === id) {
      this.inUse = this.ready;
      this.ready = null;
    }
    if (prev) this.deps.revokeURL(prev.url);
    return this.inUse?.url ?? null;
  }

  /** Drops everything (the player went away); usable again afterwards. */
  release(): void {
    this.cancel();
    if (this.inUse) this.deps.revokeURL(this.inUse.url);
    this.inUse = null;
  }

  private cancel(): void {
    this.want = null;
    this.ac?.abort();
    this.ac = null;
    if (this.ready) this.deps.revokeURL(this.ready.url);
    this.ready = null;
  }

  private kick(): void {
    if (this.deps.busy()) {
      this.ac?.abort(); // playback needs the network; carried on when it settles
      return;
    }
    if (!this.want || this.ac) return;
    const want = this.want;
    const ac = new AbortController();
    this.ac = ac;
    void (async () => {
      try {
        const res = await this.deps.fetch(want.url, { credentials: "same-origin", signal: ac.signal });
        if (!res.ok || Number(res.headers.get("Content-Length")) > MAX_BYTES) throw new Error(`status ${res.status}`);
        const blob = await res.blob();
        if (blob.size > MAX_BYTES) throw new Error("too large");
        if (this.want !== want) return;
        this.want = null;
        this.ready = { id: want.id, url: this.deps.createURL(blob) };
      } catch {
        if (ac.signal.aborted) return; // replaced, or paused for buffering (want is kept)
        this.failed.add(want.url);
        if (this.want === want) this.want = null;
      } finally {
        if (this.ac === ac) this.ac = null;
        if (this.want === want && !this.ready) this.kick();
      }
    })();
  }
}
