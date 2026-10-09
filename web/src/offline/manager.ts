// The offline cache: full "high" copies of the user's favorites and of
// recently played tracks in Cache Storage, under a per-device cap, served by
// the service worker (sw.ts) when there's no network.
//
// The mobile-data rule: a web page can't tell Wi-Fi from mobile data on iOS,
// so automatic caching runs only when "Allow mobile data" is on, or after
// the user tapped "Cache favorites now" in this session (taken as "the
// network I'm on now is fine").

import type { Track } from "../api/types";
import { DownloadQueue, type Job, type JobResult } from "./downloadQueue";
import { bypass, CACHE_NAME, COVER_SIZES, coverKey, INDEX_KEY, lyricsKey, streamKey } from "./keys";
import { enforceCap, planFit, planLookahead, usedBytes, type Entry } from "./policy";

const MB = 1024 * 1024;
export const CAP_CHOICES = [500 * MB, 1024 * MB, 2048 * MB, 5120 * MB] as const;
export const DEFAULT_CAP = 1024 * MB;
const CAP_KEY = "lark.offline.cap";
const MOBILE_KEY = "lark.offline.mobileData";
// Never cached: the page holds a download in memory while it plays music,
// and iOS kills a tab that grows too big.
const MAX_BYTES = 150 * MB;
const MAX_DURATION_MS = 60 * 60_000;
// A cached copy (and its covers) is checked against the server this often.
const CHECK_EVERY_MS = 7 * 24 * 3600_000;
// "online" flaps in tunnels: re-read the favorites at most this often on it.
const ONLINE_SYNC_MS = 60_000;

interface CacheLike {
  match(key: string): Promise<Response | undefined>;
  put(key: string, res: Response): Promise<void>;
  delete(key: string): Promise<boolean>;
  keys(): Promise<readonly { url: string }[]>;
}

export interface ManagerDeps {
  caches: { open(name: string): Promise<CacheLike>; delete(name: string): Promise<boolean> };
  fetch(url: string, init?: RequestInit): Promise<Response>;
  storage: Pick<Storage, "getItem" | "setItem">;
  listFavorites(): Promise<Track[]>; // newest favorited first
  online(): boolean;
  busy(): boolean; // the player is buffering the current track
  now(): number;
  notifyWorker(): void; // the cached set changed
  persist(): Promise<boolean>;
  estimate(): Promise<{ usage: number; quota: number } | null>;
  // A service worker controls the page (only then can a cached copy play).
  controlled?(): boolean;
}

/** A cached favorite, for the player's never-stop fallback. */
export interface CachedFavorite {
  track: Track;
  lastPlayedAt: number;
}

export interface OfflineSnapshot {
  count: number;
  usedBytes: number;
  cap: number;
  allowMobile: boolean;
  // Automatic caching may run right now (the mobile-data rule, storage not full).
  active: boolean;
  progress: { done: number; total: number; title: string | null } | null;
  // The last favorite that didn't fit: the cap is full of favorites.
  capReached: boolean;
  // The browser refused to store more: automatic caching stopped for this session.
  storageFull: boolean;
  estimate: { usage: number; quota: number } | null;
  // Changes identity whenever the cached set does (for row indicators).
  cachedIds: ReadonlySet<number>;
}

type Meta = NonNullable<Entry["meta"]>;

// Roughly what the "high" tier sends: mp3/aac up to 320k pass through
// as-is, everything else becomes AAC 256k. A little over, so a track that
// clearly can't fit is never fetched.
function estimateBytes(t: Meta): number {
  const kbps = (t.codec === "mp3" || t.codec === "aac") && t.bitrate > 0 && t.bitrate <= 320 ? t.bitrate : 256;
  return Math.round((t.duration_ms / 1000) * kbps * 125 * 1.05);
}

const metaOf = (t: Track): Meta => ({ title: t.title, duration_ms: t.duration_ms, bitrate: t.bitrate, codec: t.codec, artist: t.artist, album: t.album });

// Enough of a Track to queue and play a cached favorite whose full record
// this session hasn't seen (the index keeps only the metadata).
function trackFromMeta(id: number, m: Meta): Track {
  return {
    id, title: m.title, artist: m.artist ?? "", artist_id: null, album: m.album ?? "", album_id: null, year: null,
    duration_ms: m.duration_ms, codec: m.codec, lossless: false, bitrate: m.bitrate, status: "kept", broken: false,
    broken_reason: "", favorite: true, disliked: false, library_id: 0, path: "", added_at: 0, track_no: null, disc_no: null,
  };
}

function storedCap(s: ManagerDeps["storage"]): number {
  const n = Number(s.getItem(CAP_KEY));
  return Number.isFinite(n) && n > 0 ? n : DEFAULT_CAP;
}

const isAbort = (e: unknown) => (e as { name?: string } | null)?.name === "AbortError";

export class OfflineCache {
  private entries = new Map<number, Entry>();
  private meta = new Map<number, Meta>(); // tracks with a queued download
  private played = new Map<number, number>(); // play history of a copy being replaced
  private tracks = new Map<number, Track>(); // full records seen this session (favorites fallback)
  // The player's last lookahead: the upcoming tracks, the one playing and
  // the previous one (a car's "previous" must not have to stream).
  private protect = new Set<number>();
  private listeners = new Set<() => void>();
  private cap: number;
  private allowMobile: boolean;
  private sessionGranted = false;
  private capReached = false;
  private storageFull = false;
  // Didn't fit under the cap: not queued again until the cap or the cached set changes.
  private tooBig = new Set<number>();
  // Over MAX_BYTES: never, this session.
  private tooLarge = new Set<number>();
  private lastOnlineSync = -Infinity;
  private inFlight: AbortController | null = null;
  private estimate: OfflineSnapshot["estimate"] = null;
  private generation = 0;
  private loaded: Promise<void> | null = null;
  private snap: OfflineSnapshot;
  private ids: ReadonlySet<number> = new Set();
  private queue: DownloadQueue;

  constructor(private deps: ManagerDeps) {
    this.cap = storedCap(deps.storage);
    this.allowMobile = deps.storage.getItem(MOBILE_KEY) === "1";
    this.queue = new DownloadQueue({
      run: (j) => this.run(j),
      online: () => deps.online(),
      busy: () => deps.busy(),
      allowed: (j) => (j.urgent || this.active()) && !this.storageFull,
      onChange: () => this.emit(),
    });
    this.snap = this.build();
  }

  // --- reading -------------------------------------------------------------

  subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => void this.listeners.delete(fn);
  };

  snapshot = (): OfflineSnapshot => this.snap;

  isCached(id: number): boolean {
    return this.entries.has(id);
  }

  /** Whether the player should try this track now: offline, only cached ones can play. */
  playableOffline(id: number): boolean {
    if (this.deps.online() || this.entries.size === 0) return true;
    return this.entries.has(id);
  }

  /** Entries under the cap: everything but the lookahead copies (they have their own budget). */
  private capped(): Entry[] {
    return [...this.entries.values()].filter((e) => e.kind !== "lookahead");
  }

  private lookaheads(): Entry[] {
    return [...this.entries.values()].filter((e) => e.kind === "lookahead");
  }

  // Only a page the service worker controls can play a cached copy.
  private servable(): boolean {
    return this.deps.controlled?.() ?? true;
  }

  /** Which kind of copy is cached for this track — if the page can play it from there. */
  localKind(id: number): Entry["kind"] | null {
    if (!this.servable()) return null;
    return this.entries.get(id)?.kind ?? null;
  }

  /** The cached favorites the page can play, as full tracks, with when each was last played here. */
  cachedFavorites(): CachedFavorite[] {
    const out: CachedFavorite[] = [];
    if (!this.servable()) return out;
    for (const e of this.entries.values()) {
      if (e.kind !== "favorite") continue;
      const track = this.tracks.get(e.trackId) ?? (e.meta ? trackFromMeta(e.trackId, e.meta) : null);
      if (track) out.push({ track, lastPlayedAt: e.lastPlayedAt });
    }
    return out;
  }

  /**
   * The queue's next tracks (soonest first): downloads the missing ones before
   * anything else, whatever the mobile-data rule says (they'd be streamed
   * anyway), as evictable "lookahead" copies, and forgets queued lookaheads
   * no longer upcoming; copies outside the tracks and `keep` (the playing
   * and previous tracks) are deleted. Returns the ids that are or will be cached; empty
   * when there is no offline cache to play them from.
   */
  lookahead(tracks: Track[], keep: number[] = []): number[] {
    if (this.storageFull || !this.servable()) return [];
    const want = new Set(tracks.map((t) => t.id));
    this.protect = new Set([...want, ...keep]);
    for (const j of this.queue.drop((j) => j.kind === "lookahead" && !want.has(j.trackId))) this.meta.delete(j.trackId);
    // Copies the queue moved past: gone.
    const passed = this.lookaheads().filter((e) => !this.protect.has(e.trackId)).map((e) => e.trackId);
    if (passed.length > 0) void this.evict(passed).then(() => this.changed());
    const accepted: number[] = [];
    const jobs: Job[] = [];
    const room = this.lookaheads().filter((e) => this.protect.has(e.trackId));
    for (const t of tracks) {
      this.tracks.set(t.id, t);
      if (this.entries.has(t.id)) {
        accepted.push(t.id);
        continue;
      }
      if (!this.wanted(t) || this.queue.gaveUp(t.id)) continue;
      const est = estimateBytes(metaOf(t));
      if (planLookahead(room, est, this.protect) === null) continue;
      room.push({ trackId: t.id, bytes: est, kind: "lookahead", lastPlayedAt: 0, cachedAt: 0 }); // reserved
      accepted.push(t.id);
      if (!this.meta.has(t.id)) this.meta.set(t.id, metaOf(t));
      jobs.push({ trackId: t.id, kind: "lookahead" });
    }
    this.queue.addFront(jobs);
    return accepted;
  }

  private active(): boolean {
    return (this.allowMobile || this.sessionGranted) && !this.storageFull;
  }

  private build(): OfflineSnapshot {
    const p = this.queue.progress();
    const all = this.capped();
    const title = (id: number | null) => (id === null ? null : (this.meta.get(id)?.title ?? this.entries.get(id)?.meta?.title ?? null));
    return {
      count: all.length,
      usedBytes: usedBytes(all),
      cap: this.cap,
      allowMobile: this.allowMobile,
      active: this.active(),
      progress: p && { done: p.done, total: p.total, title: title(p.current) },
      capReached: this.capReached,
      storageFull: this.storageFull,
      estimate: this.estimate,
      cachedIds: this.ids,
    };
  }

  private emit(): void {
    this.snap = this.build();
    this.listeners.forEach((fn) => fn());
  }

  private changed(): void {
    this.ids = new Set(this.entries.keys());
    this.deps.notifyWorker();
    void this.saveIndex();
    void this.refreshEstimate();
    this.emit();
  }

  private async refreshEstimate(): Promise<void> {
    try {
      this.estimate = await this.deps.estimate();
      this.emit();
    } catch {
      /* not available */
    }
  }

  // --- lifecycle -----------------------------------------------------------

  /** Loads the index and, when automatic caching is allowed, syncs the favorites and checks stale copies. */
  start(): Promise<void> {
    this.loaded ??= this.loadIndex();
    return this.loaded.then(async () => {
      void this.refreshEstimate();
      if (!this.active()) return;
      await this.syncFavorites().catch(() => {});
      this.queueChecks();
    });
  }

  /** Signed out (or another user): drop this session's permission and everything queued. */
  endSession(): void {
    this.generation += 1;
    this.inFlight?.abort();
    this.inFlight = null;
    this.queue.clear();
    this.meta.clear();
    this.tracks.clear();
    this.sessionGranted = false;
    this.storageFull = false;
    this.emit();
  }

  /** The player started or stopped buffering: stop a download in flight, or carry on. */
  bufferingChanged(): void {
    if (this.deps.busy()) this.inFlight?.abort();
    else this.queue.poke();
  }

  /** The network is back: carry on, and (at most once a minute) catch up with favorites changed meanwhile. */
  wentOnline(): void {
    this.queue.poke();
    const now = this.deps.now();
    if (!this.active() || now - this.lastOnlineSync < ONLINE_SYNC_MS) return;
    this.lastOnlineSync = now;
    void this.syncFavorites().catch(() => {});
  }

  private async open(): Promise<CacheLike> {
    return this.deps.caches.open(CACHE_NAME);
  }

  private async loadIndex(): Promise<void> {
    try {
      const c = await this.open();
      const res = await c.match(INDEX_KEY);
      const list = res ? ((await res.json()) as Entry[]) : [];
      for (const e of list) if (typeof e?.trackId === "number" && typeof e.bytes === "number") this.entries.set(e.trackId, e);
      // Reconcile: the page can be killed between storing a file and saving
      // the index. Files the index doesn't know are deleted (uncounted, they
      // would never be evicted); entries whose audio is gone are dropped.
      const audio = new Set<number>();
      const orphans: string[] = [];
      for (const r of await c.keys()) {
        const u = new URL(r.url, "http://x");
        const m = /^\/api\/v1\/tracks\/(\d+)\/(stream|lyrics|cover)$/.exec(u.pathname);
        if (!m) continue;
        const id = Number(m[1]);
        if (!this.entries.has(id)) orphans.push(u.pathname + u.search);
        else if (m[2] === "stream") audio.add(id);
      }
      let dropped = false;
      for (const id of [...this.entries.keys()]) {
        if (audio.has(id)) continue;
        this.entries.delete(id);
        orphans.push(lyricsKey(id), ...COVER_SIZES.map((s) => coverKey(id, s)));
        dropped = true;
      }
      await Promise.all(orphans.map((k) => c.delete(k)));
      if (dropped) void this.saveIndex();
    } catch {
      /* no or unreadable index: start empty */
    }
    this.ids = new Set(this.entries.keys());
    this.deps.notifyWorker();
    this.emit();
  }

  private async saveIndex(): Promise<void> {
    try {
      const body = JSON.stringify([...this.entries.values()]);
      await (await this.open()).put(INDEX_KEY, new Response(body, { headers: { "Content-Type": "application/json" } }));
    } catch {
      /* storage full or gone: the next change tries again */
    }
  }

  // --- settings ------------------------------------------------------------

  async setCap(bytes: number): Promise<void> {
    this.cap = bytes;
    this.deps.storage.setItem(CAP_KEY, String(bytes));
    this.capReached = false;
    this.tooBig.clear();
    await this.evict(enforceCap(this.capped(), bytes));
    this.changed();
    if (this.active()) void this.syncFavorites().catch(() => {});
  }

  setAllowMobile(on: boolean): void {
    this.allowMobile = on;
    this.deps.storage.setItem(MOBILE_KEY, on ? "1" : "0");
    this.emit();
    if (on) {
      void this.deps.persist().catch(() => false);
      void this.syncFavorites().catch(() => {});
    }
  }

  /** The "Cache favorites now" tap: allows automatic caching for this session and starts on the favorites. */
  async cacheFavoritesNow(): Promise<void> {
    this.sessionGranted = true;
    this.storageFull = false; // asked for explicitly: try again
    this.emit();
    void this.deps.persist().catch(() => false);
    await this.syncFavorites();
    this.queueChecks();
  }

  async clear(): Promise<void> {
    this.generation += 1;
    this.inFlight?.abort();
    this.queue.clear();
    this.meta.clear();
    this.sessionGranted = false;
    this.capReached = false;
    this.storageFull = false;
    this.tooBig.clear();
    this.entries.clear();
    try {
      await this.deps.caches.delete(CACHE_NAME);
    } catch {
      /* already gone */
    }
    this.changed();
  }

  // --- what to cache -------------------------------------------------------

  private wanted(t: Track): boolean {
    return !this.entries.has(t.id) && !t.broken && t.duration_ms <= MAX_DURATION_MS && !this.tooBig.has(t.id) && !this.tooLarge.has(t.id);
  }

  /** Re-reads the favorites: pins/unpins cached tracks and queues the missing ones (newest favorited first). */
  async syncFavorites(): Promise<void> {
    const favs = await this.deps.listFavorites();
    const favIds = new Set(favs.map((t) => t.id));
    favs.forEach((t) => this.tracks.set(t.id, t));
    let touched = false;
    for (const e of this.entries.values()) {
      if (e.kind === "favorite" && !favIds.has(e.trackId)) {
        e.kind = "recent";
        e.formerFavorite = true;
        touched = true;
      } else if ((e.kind === "recent" || (e.kind === "lookahead" && this.active())) && favIds.has(e.trackId)) {
        e.kind = "favorite";
        touched = true;
      }
    }
    if (touched) this.changed();
    if (!this.active()) return;
    const missing = favs.filter((t) => this.wanted(t));
    if (missing.length > 0) this.capReached = false;
    missing.forEach((t) => this.meta.set(t.id, metaOf(t)));
    this.queue.add(missing.map((t): Job => ({ trackId: t.id, kind: "favorite" })));
  }

  /** Queues a check of every copy not confirmed current for a week. */
  private queueChecks(): void {
    if (!this.active()) return;
    const now = this.deps.now();
    const due = [...this.entries.values()].filter((e) => now - (e.checkedAt ?? e.cachedAt) > CHECK_EVERY_MS);
    this.queue.add(due.map((e): Job => ({ trackId: e.trackId, kind: "check" })));
  }

  /** A favorite toggle anywhere in the app: unfavorited is no longer pinned; a new favorite gets cached. */
  favoriteChanged(id: number, on: boolean): void {
    const e = this.entries.get(id);
    if (e) {
      if (e.kind === "lookahead" && !this.active()) return; // a passing copy: not kept for this
      if (on) e.kind = "favorite";
      else if (e.kind === "favorite") {
        e.kind = "recent";
        e.formerFavorite = true;
      }
      this.tooBig.clear(); // what's evictable changed
      this.changed();
    } else if (on && this.active()) {
      void this.syncFavorites().catch(() => {});
    }
  }

  /** The player started (finished=false) or played to the end (finished=true) a track. */
  notePlayed(t: Track, finished: boolean): void {
    this.tracks.set(t.id, t);
    const e = this.entries.get(t.id);
    if (e?.kind === "lookahead" && finished) {
      // Played: kept as what automatic caching would have kept anyway, else
      // left as a passing copy — gone once it's no longer the previous track.
      e.lastPlayedAt = this.deps.now();
      const fit = this.active() ? planFit(this.capped(), this.cap, e.bytes) : null;
      if (fit === null) {
        void this.saveIndex();
        return;
      }
      e.kind = t.favorite ? "favorite" : "recent";
      void this.evict(fit).then(() => this.changed());
      return;
    }
    if (e) {
      e.lastPlayedAt = this.deps.now();
      void this.saveIndex();
      return;
    }
    if (!finished || !this.active() || this.queue.has(t.id) || !this.wanted(t)) return;
    this.meta.set(t.id, metaOf(t));
    this.queue.add([{ trackId: t.id, kind: t.favorite ? "favorite" : "recent" }]);
  }

  // --- downloading ---------------------------------------------------------

  private async evict(ids: number[]): Promise<void> {
    if (ids.length === 0) return;
    const c = await this.open();
    for (const id of ids) {
      this.entries.delete(id);
      await Promise.all([c.delete(streamKey(id)), c.delete(lyricsKey(id)), ...COVER_SIZES.map((s) => c.delete(coverKey(id, s)))]);
    }
    this.tooBig.clear(); // room was made
  }

  /** Fetches and stores one small extra (lyrics, a cover); its size, or 0. A 404 removes a stale copy. */
  private async keep(c: CacheLike, key: string): Promise<number> {
    try {
      const res = await this.deps.fetch(bypass(key), { credentials: "same-origin" });
      if (res.status === 404) await c.delete(key);
      if (!res.ok) return 0;
      const ct = res.headers.get("Content-Type");
      const blob = await res.blob();
      await c.put(key, new Response(blob, { headers: ct ? { "Content-Type": ct } : {} }));
      return blob.size;
    } catch {
      return 0; // best effort: the audio is what matters
    }
  }

  private run(job: Job): Promise<JobResult> {
    return job.kind === "check" ? this.check(job.trackId) : this.download(job);
  }

  private async download(job: Job): Promise<JobResult> {
    const id = job.trackId;
    const t = this.meta.get(id);
    const gen = this.generation;
    const done = (r: JobResult) => {
      if (r === "done" || r === "skip") this.meta.delete(id);
      return r;
    };
    if (!t || this.entries.has(id)) return done("skip");
    const noRoom = () => {
      if (job.kind !== "lookahead") this.tooBig.add(id); // a lookahead's room changes with the queue
      if (job.kind === "favorite") this.capReached = true;
      return done("skip");
    };
    const look = job.kind === "lookahead";
    const fits = (bytes: number) =>
      look ? planLookahead(this.lookaheads(), bytes, new Set([...this.protect].filter((k) => k !== id))) : planFit(this.capped(), this.cap, bytes);
    if (fits(estimateBytes(t)) === null) return noRoom();

    const ac = new AbortController();
    this.inFlight = ac;
    let blob: Blob;
    let res: Response;
    try {
      res = await this.deps.fetch(bypass(streamKey(id)), { credentials: "same-origin", signal: ac.signal });
      if (!res.ok) return done(res.status >= 500 ? "retry" : "skip");
      const len = Number(res.headers.get("Content-Length"));
      if (len > MAX_BYTES) {
        ac.abort();
        this.tooLarge.add(id);
        return done("skip");
      }
      if (len > 0 && fits(len) === null) {
        ac.abort();
        return noRoom();
      }
      blob = await res.blob();
    } catch (e) {
      // Aborted because playback needed the network: not a failure.
      return ac.signal.aborted && isAbort(e) ? "paused" : "retry";
    } finally {
      if (this.inFlight === ac) this.inFlight = null;
    }
    if (gen !== this.generation) return done("skip"); // cleared meanwhile
    if (blob.size > MAX_BYTES) {
      this.tooLarge.add(id);
      return done("skip");
    }
    if (look && !this.protect.has(id)) return done("skip"); // passed while downloading
    let evict = fits(blob.size);
    if (evict === null) return noRoom();

    // Store first, evict after: a failed put must not have thrown anything away.
    const c = await this.open();
    const audio = () => new Response(blob, { headers: { "Content-Type": res.headers.get("Content-Type") || "audio/mp4" } });
    try {
      await c.put(streamKey(id), audio());
    } catch {
      try {
        await this.evict(evict); // maybe the browser's quota, not our cap: make the room, try once more
        evict = [];
        await c.put(streamKey(id), audio());
      } catch {
        this.storageFull = true; // stop automatic caching for this session
        this.meta.clear();
        this.queue.clear(); // nothing queued would fit either (this job's result is dropped with it)
        this.changed();
        return "skip";
      }
    }
    await this.evict(evict);
    if (gen !== this.generation) {
      await c.delete(streamKey(id));
      return done("skip");
    }
    const now = this.deps.now();
    const entry: Entry = {
      trackId: id,
      bytes: blob.size,
      kind: job.kind === "favorite" ? "favorite" : job.kind === "lookahead" ? "lookahead" : "recent",
      lastPlayedAt: this.played.get(id) ?? (job.kind === "recent" ? now : 0),
      cachedAt: now,
      checkedAt: now,
      version: res.headers.get("X-Lark-Version") ?? undefined,
      meta: t,
    };
    this.played.delete(id);
    // In the index as soon as the audio is stored (the extras can wait).
    this.entries.set(id, entry);
    this.changed();
    const extras = await Promise.all([this.keep(c, lyricsKey(id)), ...COVER_SIZES.map((s) => this.keep(c, coverKey(id, s)))]);
    if (gen !== this.generation) return done("skip");
    entry.bytes += extras.reduce((a, b) => a + b, 0); // counted too (small: a few percent)
    this.changed();
    return done("done");
  }

  /** Is the cached copy still the server's file? Re-downloads a changed one; refreshes the covers. */
  private async check(id: number): Promise<JobResult> {
    const e = this.entries.get(id);
    if (!e) return "skip";
    let res: Response;
    try {
      res = await this.deps.fetch(bypass(streamKey(id)), { credentials: "same-origin", cache: "no-store", headers: { Range: "bytes=0-0" } });
      void res.body?.cancel().catch(() => {});
    } catch {
      return "retry";
    }
    if (res.status === 404 || res.status === 410) {
      await this.evict([id]); // gone from the server
      this.changed();
      return "done";
    }
    if (!res.ok) return "skip";
    const v = res.headers.get("X-Lark-Version");
    if (v && e.version && v !== e.version && e.meta) {
      // The source file changed: replace the copy (same kind, same play history).
      await this.evict([id]);
      this.changed();
      this.meta.set(id, e.meta);
      if (e.lastPlayedAt) this.played.set(id, e.lastPlayedAt);
      this.queue.add([{ trackId: id, kind: e.kind === "lookahead" ? "lookahead" : e.kind === "favorite" ? "favorite" : "recent" }]);
      return "done";
    }
    const c = await this.open();
    await Promise.all(COVER_SIZES.map((s) => this.keep(c, coverKey(id, s))));
    if (v && !e.version) e.version = v;
    e.checkedAt = this.deps.now();
    void this.saveIndex();
    return "done";
  }
}
