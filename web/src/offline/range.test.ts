import { describe, expect, test } from "vitest";
import { Blob as NodeBlob } from "node:buffer";
import { parseRange, rangeResponse } from "./range";

// Node's Blob (via Response), like the one a cached Response hands the
// service worker — jsdom's own Blob can't be streamed into a Response.
const body = () => new NodeBlob([new Uint8Array(Array.from({ length: 100 }, (_, i) => i))], { type: "audio/mp4" }) as unknown as Blob;
const bytes = async (r: Response) => [...new Uint8Array(await r.arrayBuffer())];

describe("parseRange", () => {
  test("no or malformed header means the whole body", () => {
    expect(parseRange(null, 100)).toBeNull();
    expect(parseRange("", 100)).toBeNull();
    expect(parseRange("items=0-1", 100)).toBeNull();
    expect(parseRange("bytes=abc", 100)).toBeNull();
  });
  test("start-", () => expect(parseRange("bytes=10-", 100)).toEqual({ start: 10, end: 99 }));
  test("start-end, end clamped to the size", () => {
    expect(parseRange("bytes=0-1", 100)).toEqual({ start: 0, end: 1 });
    expect(parseRange("bytes=90-500", 100)).toEqual({ start: 90, end: 99 });
  });
  test("suffix", () => {
    expect(parseRange("bytes=-10", 100)).toEqual({ start: 90, end: 99 });
    expect(parseRange("bytes=-500", 100)).toEqual({ start: 0, end: 99 });
  });
  test("several ranges, or an invalid one, are ignored: the whole body", () => {
    expect(parseRange("bytes=0-1, 5-6", 100)).toBeNull();
    expect(parseRange("bytes=5-2", 100)).toBeNull();
  });
  test("unsatisfiable", () => {
    expect(parseRange("bytes=100-", 100)).toBe("unsatisfiable");
    expect(parseRange("bytes=-0", 100)).toBe("unsatisfiable");
  });
});

describe("rangeResponse", () => {
  test("no Range: 200 with the full body and length", async () => {
    const r = rangeResponse(body(), "audio/mp4", null);
    expect(r.status).toBe(200);
    expect(r.headers.get("Content-Length")).toBe("100");
    expect(r.headers.get("Accept-Ranges")).toBe("bytes");
    expect(r.headers.get("Content-Type")).toBe("audio/mp4");
    expect((await bytes(r)).length).toBe(100);
  });
  test("start-end: 206 with Content-Range", async () => {
    const r = rangeResponse(body(), "audio/mp4", "bytes=0-1");
    expect(r.status).toBe(206);
    expect(r.headers.get("Content-Range")).toBe("bytes 0-1/100");
    expect(r.headers.get("Content-Length")).toBe("2");
    expect(r.headers.get("Accept-Ranges")).toBe("bytes");
    expect(await bytes(r)).toEqual([0, 1]);
  });
  test("start-: 206 to the end", async () => {
    const r = rangeResponse(body(), "audio/mp4", "bytes=98-");
    expect(r.headers.get("Content-Range")).toBe("bytes 98-99/100");
    expect(await bytes(r)).toEqual([98, 99]);
  });
  test("suffix: the last n bytes", async () => {
    const r = rangeResponse(body(), "audio/mp4", "bytes=-3");
    expect(r.status).toBe(206);
    expect(r.headers.get("Content-Range")).toBe("bytes 97-99/100");
    expect(await bytes(r)).toEqual([97, 98, 99]);
  });
  test("out of range: 416 with the size", () => {
    const r = rangeResponse(body(), "audio/mp4", "bytes=200-");
    expect(r.status).toBe(416);
    expect(r.headers.get("Content-Range")).toBe("bytes */100");
  });
  test("never carries auth headers", () => {
    const r = rangeResponse(body(), "audio/mp4", "bytes=0-1");
    expect(r.headers.get("Set-Cookie")).toBeNull();
    expect(r.headers.get("Authorization")).toBeNull();
  });
});
