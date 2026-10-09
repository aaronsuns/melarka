// HTTP Range support for audio served from Cache Storage. iOS's <audio>
// always asks with a Range header (it starts with "bytes=0-1") and refuses a
// source that doesn't answer 206 correctly, so the service worker builds the
// responses itself from the cached full body.

export interface ByteRange {
  start: number;
  end: number; // inclusive
}

/**
 * The single range a Range header asks for, clamped to size; null when there
 * is no usable header — none, another unit, several ranges, or an invalid
 * one: serve the whole body, 200 (RFC 9110 lets a server ignore those);
 * "unsatisfiable" for 416.
 */
export function parseRange(header: string | null, size: number): ByteRange | "unsatisfiable" | null {
  if (!header) return null;
  const m = /^bytes=\s*(\d*)\s*-\s*(\d*)\s*$/.exec(header.trim());
  if (!m || (m[1] === "" && m[2] === "")) return null;
  if (m[1] === "") {
    const n = Number(m[2]);
    if (n === 0 || size === 0) return "unsatisfiable";
    return { start: Math.max(0, size - n), end: size - 1 };
  }
  const start = Number(m[1]);
  const end = m[2] === "" ? size - 1 : Math.min(Number(m[2]), size - 1);
  if (m[2] !== "" && Number(m[2]) < start) return null; // invalid: ignored
  if (start >= size) return "unsatisfiable";
  return { start, end };
}

/** A 200, 206 or 416 response for body per the request's Range header. Only content headers: never anything auth-related. */
export function rangeResponse(body: Blob, contentType: string, rangeHeader: string | null): Response {
  const size = body.size;
  const r = parseRange(rangeHeader, size);
  const base = { "Content-Type": contentType, "Accept-Ranges": "bytes" };
  if (r === null) {
    return new Response(body, { status: 200, headers: { ...base, "Content-Length": String(size) } });
  }
  if (r === "unsatisfiable") {
    return new Response(null, { status: 416, headers: { ...base, "Content-Range": `bytes */${size}` } });
  }
  return new Response(body.slice(r.start, r.end + 1), {
    status: 206,
    headers: { ...base, "Content-Range": `bytes ${r.start}-${r.end}/${size}`, "Content-Length": String(r.end - r.start + 1) },
  });
}
