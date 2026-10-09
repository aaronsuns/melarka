import { Blob as NodeBlob } from "node:buffer";
import { describe, expect, test, vi } from "vitest";
import { coverKey, lyricsKey, streamKey } from "./keys";
import { handleFetch, type SwCache, type SwDeps } from "./swHandler";

const ORIGIN = "https://lark.example";

class FakeCache implements SwCache {
  store = new Map<string, Response>();
  async match(key: string) {
    return this.store.get(key)?.clone();
  }
  async put(key: string, res: Response) {
    this.store.set(key, res);
  }
}

const audio = () => new NodeBlob([new Uint8Array(Array.from({ length: 100 }, (_, i) => i))], { type: "audio/mp4" }) as unknown as Blob;

function setup(over: Partial<SwDeps> = {}) {
  const cache = new FakeCache();
  const net = vi.fn(async (_req: Request) => new Response("net", { status: 200, headers: { "Content-Type": "text/plain" } }));
  const ids = new Set<number>();
  let online = true;
  const deps: SwDeps = {
    origin: ORIGIN,
    cache: async () => cache,
    fetch: net,
    online: () => online,
    cachedIds: () => ids,
    ...over,
  };
  return { cache, net, ids, deps, setOnline: (v: boolean) => (online = v) };
}

const req = (path: string, headers: Record<string, string> = {}, method = "GET") => new Request(ORIGIN + path, { headers, method });

describe("what the service worker leaves alone", () => {
  test("app shell, JS, CSS and API JSON are never touched", () => {
    const { deps } = setup();
    for (const p of ["/", "/index.html", "/assets/index-abc.js", "/assets/index.css", "/api/v1/me", "/api/v1/tracks?favorite=1", "/api/v1/queue", "/sw.js"]) {
      expect(handleFetch(req(p), deps)).toBeNull();
    }
  });
  test("an uncached track passes through to the network unchanged", () => {
    const { deps } = setup();
    expect(handleFetch(req("/api/v1/tracks/7/stream?quality=high", { Range: "bytes=0-1" }), deps)).toBeNull();
    expect(handleFetch(req("/api/v1/tracks/7/lyrics"), deps)).toBeNull();
    expect(handleFetch(req("/api/v1/tracks/7/cover?size=300"), deps)).toBeNull();
  });
  test("other origins and non-GET requests are left alone", () => {
    const { deps, ids } = setup();
    ids.add(7);
    expect(handleFetch(new Request("https://elsewhere.example/api/v1/tracks/7/stream?quality=high"), deps)).toBeNull();
    expect(handleFetch(req("/api/v1/tracks/7/lyrics", {}, "PUT"), deps)).toBeNull();
    expect(handleFetch(req("/api/v1/tracks/7/lyrics/refresh", {}, "POST"), deps)).toBeNull();
  });
});

describe("cached audio", () => {
  test("served from the cache with Range, never the network", async () => {
    const { deps, ids, cache, net } = setup();
    ids.add(7);
    await cache.put(streamKey(7), new Response(audio(), { headers: { "Content-Type": "audio/mp4" } }));
    const res = (await handleFetch(req("/api/v1/tracks/7/stream?quality=high", { Range: "bytes=0-1" }), deps))!;
    expect(res.status).toBe(206);
    expect(res.headers.get("Content-Range")).toBe("bytes 0-1/100");
    expect(res.headers.get("Content-Type")).toBe("audio/mp4");
    expect(net).not.toHaveBeenCalled();
    const full = (await handleFetch(req("/api/v1/tracks/7/stream?quality=high"), deps))!;
    expect(full.status).toBe(200);
    expect(full.headers.get("Content-Length")).toBe("100");
  });

  test("another quality goes to the network while online, and falls back to the cached copy when that fails or offline", async () => {
    const { deps, ids, cache, net, setOnline } = setup();
    ids.add(7);
    await cache.put(streamKey(7), new Response(audio(), { headers: { "Content-Type": "audio/mp4" } }));
    const r1 = (await handleFetch(req("/api/v1/tracks/7/stream?quality=lossless"), deps))!;
    expect(await r1.text()).toBe("net");
    net.mockRejectedValueOnce(new TypeError("offline"));
    const r2 = (await handleFetch(req("/api/v1/tracks/7/stream?quality=lossless", { Range: "bytes=-2" }), deps))!;
    expect(r2.status).toBe(206);
    setOnline(false);
    net.mockClear();
    const r3 = (await handleFetch(req("/api/v1/tracks/7/stream?quality=saver"), deps))!;
    expect(r3.status).toBe(200);
    expect(net).not.toHaveBeenCalled();
  });

  test("before the cached-id list is known (a worker just woke), online requests are left alone", () => {
    const { deps } = setup({ cachedIds: () => null });
    expect(handleFetch(req("/api/v1/tracks/7/stream?quality=high", { Range: "bytes=0-1" }), deps)).toBeNull();
    expect(handleFetch(req("/api/v1/tracks/7/lyrics"), deps)).toBeNull();
    expect(handleFetch(req("/api/v1/tracks/7/cover?size=300"), deps)).toBeNull();
  });

  test("before the list is known, offline, it looks in the cache", async () => {
    const { deps, cache, net, setOnline } = setup({ cachedIds: () => null });
    await cache.put(streamKey(7), new Response(audio(), { headers: { "Content-Type": "audio/mp4" } }));
    setOnline(false);
    expect((await handleFetch(req("/api/v1/tracks/7/stream?quality=high"), deps))!.status).toBe(200);
    expect(net).not.toHaveBeenCalled();
  });

  test("the cache manager's own requests always go to the network", () => {
    const { deps, ids, setOnline } = setup();
    ids.add(7);
    setOnline(false);
    expect(handleFetch(req("/api/v1/tracks/7/stream?quality=high&lark_sw=bypass"), deps)).toBeNull();
    expect(handleFetch(req("/api/v1/tracks/7/cover?size=300&lark_sw=bypass"), deps)).toBeNull();
    expect(handleFetch(req("/api/v1/tracks/7/lyrics?lark_sw=bypass"), deps)).toBeNull();
  });

  test("another quality falls back to the cached copy when the network takes over 4 s", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    try {
      const { deps, ids, cache, net } = setup();
      ids.add(7);
      await cache.put(streamKey(7), new Response(audio(), { headers: { "Content-Type": "audio/mp4" } }));
      net.mockImplementationOnce(() => new Promise<Response>(() => {}));
      const p = handleFetch(req("/api/v1/tracks/7/stream?quality=lossless"), deps)!;
      await vi.advanceTimersByTimeAsync(4000);
      expect((await p).status).toBe(200);
    } finally {
      vi.useRealTimers();
    }
  });

  test("offline, a stale id list still finds a track in the cache", async () => {
    const { deps, cache, setOnline } = setup();
    await cache.put(streamKey(9), new Response(audio(), { headers: { "Content-Type": "audio/mp4" } }));
    setOnline(false);
    expect((await handleFetch(req("/api/v1/tracks/9/stream?quality=high"), deps))!.status).toBe(200);
  });
});

describe("cached lyrics", () => {
  test("network first (refreshing the cached copy), the cached copy when the network fails", async () => {
    const { deps, ids, cache, net } = setup();
    ids.add(7);
    net.mockResolvedValueOnce(new Response('{"found":true}', { status: 200, headers: { "Content-Type": "application/json", "Set-Cookie": "s=1" } }));
    const r1 = (await handleFetch(req("/api/v1/tracks/7/lyrics"), deps))!;
    expect(await r1.text()).toBe('{"found":true}');
    const stored = (await cache.match(lyricsKey(7)))!;
    expect(stored.headers.get("Set-Cookie")).toBeNull();
    net.mockRejectedValueOnce(new TypeError("offline"));
    const r2 = (await handleFetch(req("/api/v1/tracks/7/lyrics"), deps))!;
    expect(await r2.json()).toEqual({ found: true });
  });
  test("an error answer isn't cached", async () => {
    const { deps, ids, cache, net } = setup();
    ids.add(7);
    net.mockResolvedValueOnce(new Response("{}", { status: 401 }));
    expect((await handleFetch(req("/api/v1/tracks/7/lyrics"), deps))!.status).toBe(401);
    expect(await cache.match(lyricsKey(7))).toBeUndefined();
  });
});

describe("cached covers", () => {
  test("cache first; a miss is fetched and kept", async () => {
    const { deps, ids, cache, net } = setup();
    ids.add(7);
    await cache.put(coverKey(7, 300), new Response("jpeg300", { headers: { "Content-Type": "image/jpeg" } }));
    expect(await (await handleFetch(req("/api/v1/tracks/7/cover?size=300"), deps))!.text()).toBe("jpeg300");
    expect(net).not.toHaveBeenCalled();
    net.mockResolvedValueOnce(new Response("jpeg1000", { headers: { "Content-Type": "image/jpeg" } }));
    expect(await (await handleFetch(req("/api/v1/tracks/7/cover?size=1000"), deps))!.text()).toBe("jpeg1000");
    expect(await (await cache.match(coverKey(7, 1000)))!.text()).toBe("jpeg1000");
  });
  test("other sizes are left alone", () => {
    const { deps, ids } = setup();
    ids.add(7);
    expect(handleFetch(req("/api/v1/tracks/7/cover?size=64"), deps)).toBeNull();
  });
});

describe("right after the worker woke", () => {
  test("a stream request waits for the cached list instead of going to the network", async () => {
    const { cache, net, deps } = setup({ cachedIds: () => null });
    await cache.put(streamKey(7), new Response(audio(), { headers: { "Content-Type": "audio/mp4" } }));
    deps.loadIds = async () => new Set([7]);
    const res = await handleFetch(req("/api/v1/tracks/7/stream?quality=high"), deps)!;
    expect(res.status).toBe(200);
    expect(net).not.toHaveBeenCalled();
    deps.loadIds = async () => new Set<number>();
    await handleFetch(req("/api/v1/tracks/8/stream?quality=high"), deps)!;
    expect(net).toHaveBeenCalledTimes(1);
  });
});
