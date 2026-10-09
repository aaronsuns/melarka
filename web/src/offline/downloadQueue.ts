// Offline downloads, strictly one at a time. The queue only decides *when*
// a job may run; the job itself (fetch, fit under the cap, store) is `run`.

export interface Job {
  trackId: number;
  // favorite/recent/lookahead: download it. check: revalidate a cached copy.
  kind: "favorite" | "recent" | "lookahead" | "check";
  // About to play (the queue's next tracks): first in line, and not bound by
  // the mobile-data rule — the player would stream it anyway.
  urgent?: boolean;
}

// retry: it failed (network, server) — back off, give up after MAX_ATTEMPTS.
// paused: stopped on purpose (playback needs the network) — run it again
// first, without counting a failure.
export type JobResult = "done" | "skip" | "retry" | "paused";

export interface QueueDeps {
  run(job: Job): Promise<JobResult>;
  online(): boolean;
  // The player is buffering the current track: don't compete for bandwidth.
  busy(): boolean;
  // The mobile-data rule (see OfflineCache): may this job run now?
  allowed(job: Job): boolean;
  onChange?(): void;
}

export interface Progress {
  done: number;
  total: number;
  current: number | null; // track id being downloaded
}

const RETRY_BASE_MS = 30_000; // then 60 s; the third failure gives up
const MAX_ATTEMPTS = 3;

const keyOf = (j: Job) => `${j.kind === "check" ? "c" : "d"}${j.trackId}`;

export class DownloadQueue {
  private jobs: Job[] = [];
  private current: Job | null = null;
  private done = 0;
  private total = 0;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private generation = 0;
  private attempts = new Map<string, number>();
  // Given up on for this session (until clear()): never queued again.
  private failed = new Set<string>();

  constructor(private deps: QueueDeps) {}

  add(jobs: Job[]): void {
    let added = false;
    for (const j of jobs) {
      const k = keyOf(j);
      if (this.failed.has(k) || (this.current && keyOf(this.current) === k) || this.jobs.some((x) => keyOf(x) === k)) continue;
      this.jobs.push(j);
      this.total += 1;
      added = true;
    }
    if (added) this.deps.onChange?.();
    this.poke();
  }

  /** Puts these downloads first (moving any already queued), marked urgent. */
  addFront(jobs: Job[]): void {
    const front: Job[] = [];
    for (const j of jobs) {
      const k = keyOf(j);
      if (this.failed.has(k) || (this.current && keyOf(this.current) === k) || front.some((x) => keyOf(x) === k)) continue;
      const i = this.jobs.findIndex((x) => keyOf(x) === k);
      if (i >= 0) front.push({ ...this.jobs.splice(i, 1)[0], urgent: true });
      else {
        front.push({ ...j, urgent: true });
        this.total += 1;
      }
    }
    if (front.length === 0) return;
    this.jobs.unshift(...front);
    this.deps.onChange?.();
    this.poke();
  }

  /** Removes queued (not running) jobs matching pred; returns them. */
  drop(pred: (j: Job) => boolean): Job[] {
    const gone = this.jobs.filter(pred);
    if (gone.length === 0) return gone;
    this.jobs = this.jobs.filter((j) => !pred(j));
    const n = gone.length;
    this.total -= n;
    if (this.total <= this.done) {
      this.done = 0;
      this.total = this.current ? 1 : 0;
    }
    this.deps.onChange?.();
    return gone;
  }

  clear(): void {
    this.jobs = [];
    this.generation += 1; // a job still running finishes into the void
    this.current = null;
    this.done = 0;
    this.total = 0;
    this.attempts.clear();
    this.failed.clear();
    if (this.retryTimer) clearTimeout(this.retryTimer);
    this.retryTimer = null;
    this.deps.onChange?.();
  }

  /** A download of this track is queued or running. */
  has(trackId: number): boolean {
    const k = keyOf({ trackId, kind: "favorite" });
    return (this.current !== null && keyOf(this.current) === k) || this.jobs.some((j) => keyOf(j) === k);
  }

  /** Its download failed MAX_ATTEMPTS times this session. */
  gaveUp(trackId: number): boolean {
    return this.failed.has(keyOf({ trackId, kind: "favorite" }));
  }

  progress(): Progress | null {
    if (this.total === 0) return null;
    return { done: this.done, total: this.total, current: this.current?.trackId ?? null };
  }

  /** Re-check whether the next job may start (online again, player settled, permission given). */
  poke(): void {
    if (this.current || this.retryTimer || this.jobs.length === 0) return;
    if (!this.deps.online() || this.deps.busy()) return;
    const i = this.jobs.findIndex((j) => this.deps.allowed(j));
    if (i < 0) return;
    const job = this.jobs.splice(i, 1)[0];
    this.current = job;
    const gen = this.generation;
    this.deps.onChange?.();
    this.deps
      .run(job)
      .catch((): JobResult => "retry")
      .then((r) => {
        if (gen !== this.generation) return;
        this.current = null;
        const k = keyOf(job);
        if (r === "paused") {
          this.jobs.unshift(job);
        } else if (r === "retry") {
          const n = (this.attempts.get(k) ?? 0) + 1;
          this.attempts.set(k, n);
          if (n >= MAX_ATTEMPTS) {
            this.failed.add(k);
            this.done += 1;
          } else {
            // Back to the front: the network (or server) failed, not the job.
            this.jobs.unshift(job);
            this.retryTimer = setTimeout(() => {
              this.retryTimer = null;
              this.poke();
            }, RETRY_BASE_MS * 2 ** (n - 1));
          }
        } else {
          this.attempts.delete(k);
          this.done += 1;
        }
        if (this.jobs.length === 0 && !this.retryTimer) {
          this.done = 0;
          this.total = 0;
        }
        this.deps.onChange?.();
        this.poke();
      });
  }
}
