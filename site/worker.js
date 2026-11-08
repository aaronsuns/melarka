// Static assets, plus HTTP Range for the videos: iPhone Safari won't play an <video> whose server
// ignores Range (it needs 206 Partial Content), and the assets binding answers ranges with the
// whole file. The videos are small (~4 MB), so slicing the full asset is fine.
export default {
  async fetch(request, env) {
    const url = new URL(request.url);
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
