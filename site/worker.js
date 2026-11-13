// Static assets, plus HTTP Range for the videos: iPhone Safari won't play an <video> whose server
// ignores Range (it needs 206 Partial Content), and the assets binding answers ranges with the
// whole file. The videos are small (~4 MB), so slicing the full asset is fine.
// /api/ping: the anonymous usage ping from Melarka servers (internal/usageping in the server). Stores only
// the random install id, the version and the coarse fields below, never the IP; each key expires 35 days
// after its last ping. Older servers send only id and version.
const PING_TTL = 35 * 24 * 3600;
const json = (body, status = 200, extra = {}) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...extra } });

async function ping(request, env) {
  if (request.method !== "POST") return json({ error: "POST only" }, 405);
  if (Number(request.headers.get("Content-Length") || 0) > 1024) return json({ error: "too large" }, 413);
  let p;
  try { p = JSON.parse(await request.text()); } catch { return json({ error: "bad JSON" }, 400); }
  const id = typeof p?.id === "string" ? p.id : "";
  const version = typeof p?.version === "string" ? p.version : "";
  if (!/^[0-9a-f]{32}$/.test(id) || !/^[0-9A-Za-z.+_-]{1,40}$/.test(version)) return json({ error: "bad ping" }, 400);
  const meta = { v: version };
  const word = (x) => (typeof x === "string" && /^[0-9A-Za-z+_-]{1,16}$/.test(x) ? x : undefined);
  for (const k of ["os", "arch", "users", "tracks"]) if (word(p[k])) meta[k] = p[k];
  if (typeof p.container === "boolean") meta.container = p.container ? "yes" : "no";
  await env.PINGS.put("i:" + id, "", { expirationTtl: PING_TTL, metadata: meta });
  return new Response(null, { status: 204 });
}

// /api/ping/stats: how many installs pinged in the last 35 days, by version and by each coarse field.
async function stats(env) {
  let installs = 0, cursor;
  const out = { versions: {}, os: {}, arch: {}, container: {}, users: {}, tracks: {} };
  const add = (field, v) => { out[field][v || "?"] = (out[field][v || "?"] || 0) + 1; };
  do {
    const page = await env.PINGS.list({ prefix: "i:", cursor });
    for (const k of page.keys) {
      installs++;
      const m = k.metadata || {};
      add("versions", m.v);
      for (const f of ["os", "arch", "container", "users", "tracks"]) add(f, m[f]);
    }
    cursor = page.list_complete ? undefined : page.cursor;
  } while (cursor);
  return json({ installs_35d: installs, ...out }, 200, { "Cache-Control": "public, max-age=3600" });
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (url.pathname === "/api/ping") return ping(request, env);
    if (url.pathname === "/api/ping/stats") return stats(env);
    if (url.pathname.startsWith("/api/")) return json({ error: "no such endpoint" }, 404);
    const range = request.headers.get("Range");
    if (!range || !url.pathname.endsWith(".mp4")) return env.ASSETS.fetch(request);
    const res = await env.ASSETS.fetch(new Request(url, { method: "GET" }));
    if (res.status !== 200) return res;
    const body = await res.arrayBuffer();
    const size = body.byteLength;
    const m = /^bytes=(\d*)-(\d*)$/.exec(range.trim());
    let start, end;
    if (m && m[1] !== "") { start = +m[1]; end = m[2] !== "" ? Math.min(+m[2], size - 1) : size - 1; }
    else if (m && m[2] !== "") { start = Math.max(size - +m[2], 0); end = size - 1; }
    if (!m || start === undefined || start > end || start >= size) {
      return new Response(null, { status: 416, headers: { "Content-Range": `bytes */${size}` } });
    }
    const headers = new Headers(res.headers);
    headers.set("Content-Range", `bytes ${start}-${end}/${size}`);
    headers.set("Content-Length", String(end - start + 1));
    headers.set("Accept-Ranges", "bytes");
    return new Response(request.method === "HEAD" ? null : body.slice(start, end + 1), { status: 206, headers });
  },
};
