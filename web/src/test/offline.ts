// In-memory Cache Storage for offline-cache tests.
export class FakeCache {
  store = new Map<string, Response>();
  // Makes put() throw like a full quota (QuotaExceededError) when it returns true.
  failPut: (key: string) => boolean = () => false;
  async match(k: string) {
    return this.store.get(k)?.clone();
  }
  async put(k: string, r: Response) {
    if (this.failPut(k)) throw new DOMException("quota", "QuotaExceededError");
    this.store.set(k, r);
  }
  async delete(k: string) {
    return this.store.delete(k);
  }
  async keys() {
    return [...this.store.keys()].map((k) => ({ url: "https://lark.example" + k }));
  }
}

export class FakeCaches {
  caches = new Map<string, FakeCache>();
  async open(n: string) {
    if (!this.caches.has(n)) this.caches.set(n, new FakeCache());
    return this.caches.get(n)!;
  }
  async delete(n: string) {
    return this.caches.delete(n);
  }
}
