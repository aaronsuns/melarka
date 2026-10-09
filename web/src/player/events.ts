import type { PlayEvent } from "../api/types";

export class EventBuffer {
  private inFlight: Promise<void> | null = null;

  constructor(
    private post: (evs: PlayEvent[]) => Promise<number>,
    private storage: Storage = localStorage,
    private key = "lark.pendingEvents",
  ) {}

  pending(): PlayEvent[] {
    try {
      return JSON.parse(this.storage.getItem(this.key) ?? "[]") as PlayEvent[];
    } catch {
      return [];
    }
  }

  private save(evs: PlayEvent[]) {
    this.storage.setItem(this.key, JSON.stringify(evs));
  }

  add(ev: Omit<PlayEvent, "client_event_id">) {
    this.save([...this.pending(), { ...ev, client_event_id: crypto.randomUUID() }]);
  }

  // No-op while a flush is already in flight (the next one picks up
  // whatever was added meanwhile).
  async flush(): Promise<void> {
    if (this.inFlight) return;
    const batch = this.pending();
    if (batch.length === 0) return;
    this.inFlight = (async () => {
      try {
        await this.post(batch);
        const sent = new Set(batch.map((e) => e.client_event_id));
        this.save(this.pending().filter((e) => !sent.has(e.client_event_id)));
      } catch {
        /* keep everything for the next flush; the server dedupes by id */
      }
    })();
    try {
      await this.inFlight;
    } finally {
      this.inFlight = null;
    }
  }

  // Waits for any in-flight flush, then sends everything still pending.
  async drain(): Promise<void> {
    while (this.inFlight) await this.inFlight;
    await this.flush();
  }
}
