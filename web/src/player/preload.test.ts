import { describe, expect, test, vi } from "vitest";
import { BlobPreloader, type PreloadDeps } from "./preload";

function setup(over: Partial<PreloadDeps> = {}) {
  let busy = false;
  let n = 0;
  const revoked: string[] = [];
  const fetchFn = vi.fn(async (url: string) => new Response(new Uint8Array(10), { status: url.includes("/404/") ? 404 : 200 }));
  const deps: PreloadDeps = {
    fetch: fetchFn as unknown as PreloadDeps["fetch"],
    busy: () => busy,
    createURL: () => `blob:${++n}`,
    revokeURL: (u) => void revoked.push(u),
    ...over,
  };
  const p = new BlobPreloader(deps);
  return { p, fetchFn, revoked, setBusy: (b: boolean) => ((busy = b), p.busyChanged()) };
}

describe("BlobPreloader (no offline cache: the next track is fetched whole into memory)", () => {
  test("waits until the current track stops buffering, then fetches the whole file", async () => {
    const s = setup();
    s.setBusy(true);
    s.p.request(2, "/api/v1/tracks/2/stream?quality=high");
    expect(s.fetchFn).not.toHaveBeenCalled();
    s.setBusy(false);
    await vi.waitFor(() => expect(s.p.has(2)).toBe(true));
    expect(new Headers((s.fetchFn.mock.calls[0] as unknown[])[1] as HeadersInit).get("Range")).toBeNull();
    expect(s.p.take(2)).toBe("blob:1");
  });

  test("the blob stays in use while its track plays and is released at the next load", async () => {
    const s = setup();
    s.p.request(2, "/2");
    await vi.waitFor(() => expect(s.p.has(2)).toBe(true));
    expect(s.p.take(2)).toBe("blob:1");
    expect(s.p.take(2)).toBe("blob:1"); // a reload of the same track
    expect(s.revoked).toEqual([]);
    expect(s.p.take(3)).toBeNull();
    expect(s.revoked).toEqual(["blob:1"]);
  });

  test("a new next track replaces the old one; buffering aborts a fetch in flight and resumes it later", async () => {
    let release!: () => void;
    const f = vi.fn((_u: string, init?: RequestInit) =>
        new Promise<Response>((resolve, reject) => {
          init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
          release = () => resolve(new Response(new Uint8Array(4)));
        }),
    );
    const s = setup({ fetch: f as unknown as PreloadDeps["fetch"] });
    s.p.request(2, "/2");
    s.setBusy(true); // aborted
    s.setBusy(false); // and started again
    await vi.waitFor(() => expect(f).toHaveBeenCalledTimes(2));
    release();
    await vi.waitFor(() => expect(s.p.has(2)).toBe(true));
    s.p.request(3, "/3");
    expect(s.p.has(2)).toBe(false);
    expect(s.revoked).toEqual(["blob:1"]);
  });

  test("a failed fetch isn't tried again for the same url", async () => {
    const s = setup();
    s.p.request(4, "/api/v1/tracks/404/stream");
    await vi.waitFor(() => expect(s.fetchFn).toHaveBeenCalledTimes(1));
    s.p.request(5, "/5");
    await vi.waitFor(() => expect(s.p.has(5)).toBe(true));
    s.p.request(4, "/api/v1/tracks/404/stream");
    await new Promise((r) => setTimeout(r, 10));
    expect(s.fetchFn).toHaveBeenCalledTimes(2);
  });
});
