import { api, ApiError, onFavoriteSet, setUnauthorizedHandler, streamUrl } from "./client";
import { mockFetch } from "../test/setup";

test("login posts credentials and returns the user", async () => {
  const f = mockFetch({ "POST /api/v1/auth/login": (init) => {
    expect(JSON.parse(init.body as string)).toMatchObject({ username: "bob", password: "pw" });
    return { body: { token: "t", user: { id: 2, username: "bob", role: "member" } } };
  } });
  const u = await api.login("bob", "pw");
  expect(u.username).toBe("bob");
  expect(f.mock.calls[0][1]!.credentials).toBe("same-origin");
});

test("errors carry status and server message", async () => {
  mockFetch({ "POST /api/v1/playlists": () => ({ status: 400, body: { error: "name required" } }) });
  await expect(api.createPlaylist(" ", [])).rejects.toMatchObject({ status: 400, message: "name required" });
});

test("401 triggers the unauthorized handler", async () => {
  const onUnauth = vi.fn();
  setUnauthorizedHandler(onUnauth);
  mockFetch({ "GET /api/v1/me": () => ({ status: 401, body: { error: "not signed in" } }) });
  await expect(api.me()).rejects.toBeInstanceOf(ApiError);
  expect(onUnauth).toHaveBeenCalledOnce();
});

test("login 401 does not trigger the unauthorized handler", async () => {
  const onUnauth = vi.fn();
  setUnauthorizedHandler(onUnauth);
  mockFetch({ "POST /api/v1/auth/login": () => ({ status: 401, body: { error: "wrong username or password" } }) });
  await expect(api.login("a", "b")).rejects.toMatchObject({ status: 401 });
  expect(onUnauth).not.toHaveBeenCalled();
});

test("query params are encoded and undefined ones dropped", async () => {
  const f = mockFetch({ "GET /api/v1/tracks": () => ({ body: { items: [], next_cursor: "" } }) });
  await api.tracks({ sort: "title", cursor: undefined, limit: 50 });
  expect(f.mock.calls[0][0]).toBe("/api/v1/tracks?sort=title&limit=50");
  mockFetch({ "GET /api/v1/search": (_i, url) => { expect(url).toBe("/api/v1/search?q=%E9%82%93%20l"); return { body: { tracks: [], albums: [], artists: [] } }; } });
  await api.search("邓 l");
});

test("radio passes exclude as comma list", async () => {
  mockFetch({ "GET /api/v1/radio/next": (_i, url) => { expect(url).toBe("/api/v1/radio/next?n=10&exclude=1%2C2"); return { body: [] }; } });
  await api.radio(10, [1, 2]);
});

test("streamUrl", () => {
  expect(streamUrl(7, "saver")).toBe("/api/v1/tracks/7/stream?quality=saver");
});

test("randomTracks passes exclude as comma list", async () => {
  mockFetch({ "GET /api/v1/tracks/random": (_i, url) => { expect(url).toBe("/api/v1/tracks/random?n=50&exclude=1%2C2"); return { body: [] }; } });
  await api.randomTracks(50, [1, 2]);
});

test("youtubeSearch encodes the query", async () => {
  mockFetch({
    "GET /api/v1/youtube/search": (_i, url) => {
      expect(url).toBe("/api/v1/youtube/search?q=%E9%82%93%E4%B8%BD%E5%90%9B");
      return { body: { videos: [{ id: "v1", title: "t", channel: "c", url: "u", thumbnail: "th", duration_s: 10 }], playlists: [] } };
    },
  });
  const vs = await api.youtubeSearch("邓丽君");
  expect(vs.videos[0]).toMatchObject({ id: "v1", title: "t" });
});

test("createDownload posts a bare url and unwraps jobs", async () => {
  const job = { id: 1, user_id: 2, username: "u", url: "x", video_id: "v", title: "t", channel: "c", duration_s: 1, thumbnail: "", status: "queued", progress: 0, error: "", track_id: null, created_at: 0, updated_at: 0 };
  mockFetch({ "POST /api/v1/downloads": (init) => { expect(JSON.parse(init.body as string)).toEqual({ url: "https://youtu.be/x" }); return { status: 201, body: { jobs: [job] } }; } });
  const jobs = await api.createDownload({ url: "https://youtu.be/x" });
  expect(jobs).toEqual([job]);
});

test("createDownload posts a video object", async () => {
  const video = { id: "v1", title: "t", channel: "c", url: "https://youtu.be/v1", thumbnail: "", duration_s: 1 };
  mockFetch({ "POST /api/v1/downloads": (init) => { expect(JSON.parse(init.body as string)).toEqual({ video }); return { status: 201, body: { jobs: [] } }; } });
  await api.createDownload({ video });
});

test("downloads passes all=1 only when requested", async () => {
  const f = mockFetch({ "GET /api/v1/downloads": () => ({ body: [] }) });
  await api.downloads();
  expect(f.mock.calls[0][0]).toBe("/api/v1/downloads");
  await api.downloads(true);
  expect(f.mock.calls[1][0]).toBe("/api/v1/downloads?all=1");
});

test("cancelDownload and retryDownload hit the right routes", async () => {
  const f = mockFetch({ "DELETE /api/v1/downloads/5": () => ({ status: 204 }), "POST /api/v1/downloads/5/retry": () => ({ status: 204 }) });
  await api.cancelDownload(5);
  await api.retryDownload(5);
  expect(f.mock.calls[0][1]?.method).toBe("DELETE");
  expect(f.mock.calls[1][1]?.method).toBe("POST");
});

test("changePassword sends current and new", async () => {
  mockFetch({ "PUT /api/v1/me/password": (init) => { expect(JSON.parse(init.body as string)).toEqual({ current: "old", new: "nw" }); return { status: 204 }; } });
  await api.changePassword("old", "nw");
});

test("users, createUser, deleteUser, resetPassword hit the right routes and bodies", async () => {
  mockFetch({
    "GET /api/v1/users": () => ({ body: [] }),
    "POST /api/v1/users": (init) => { expect(JSON.parse(init.body as string)).toEqual({ username: "bob", password: "pw", role: "member" }); return { status: 201, body: { id: 3, username: "bob", role: "member" } }; },
    "DELETE /api/v1/users/3": () => ({ status: 204 }),
    "PUT /api/v1/users/3/password": (init) => { expect(JSON.parse(init.body as string)).toEqual({ password: "pw2" }); return { status: 204 }; },
  });
  await api.users();
  await api.createUser({ username: "bob", password: "pw", role: "member" });
  await api.deleteUser(3);
  await api.resetPassword(3, "pw2");
});

test("libraries, scanStatus, triggerScan hit the right routes", async () => {
  const scan = vi.fn((_init: RequestInit, _url: string) => ({ status: 202, body: { queued_at: 1 } }));
  const f = mockFetch({
    "GET /api/v1/admin/libraries": () => ({ body: [] }),
    "GET /api/v1/admin/scan/status": () => ({ body: [] }),
    "POST /api/v1/admin/scan": scan,
  });
  await api.libraries();
  await api.scanStatus();
  await api.triggerScan();
  expect(f.mock.calls[2][0]).toBe("/api/v1/admin/scan");
  await api.triggerScan(7);
  expect(scan.mock.calls[1][1]).toBe("/api/v1/admin/scan?library=7");
});

test("trash, restoreTrash and emptyTrash unwraps purged count", async () => {
  mockFetch({
    "GET /api/v1/trash": () => ({ body: [] }),
    "POST /api/v1/trash/9/restore": () => ({ status: 204 }),
    "DELETE /api/v1/trash": () => ({ body: { purged: 3 } }),
  });
  await api.trash();
  await api.restoreTrash(9);
  expect(await api.emptyTrash()).toBe(3);
});

test("ytdlpInfo and ytdlpUpdate hit the right routes", async () => {
  mockFetch({
    "GET /api/v1/admin/ytdlp": () => ({ body: { version: "1", path: "/p" } }),
    "POST /api/v1/admin/ytdlp/update": () => ({ body: { version: "2", path: "/p" } }),
  });
  expect(await api.ytdlpInfo()).toEqual({ version: "1", path: "/p" });
  expect(await api.ytdlpUpdate()).toEqual({ version: "2", path: "/p" });
});

test("a successful favorite toggle is announced; a failed one isn't", async () => {
  const heard: [number, boolean][] = [];
  const off = onFavoriteSet((id, on) => heard.push([id, on]));
  mockFetch({
    "PUT /api/v1/favorites/3": () => ({ status: 204 }),
    "DELETE /api/v1/favorites/3": () => ({ status: 204 }),
    "PUT /api/v1/favorites/4": () => ({ status: 500, body: { error: "boom" } }),
  });
  await api.setFavorite(3, true);
  await api.setFavorite(3, false);
  await expect(api.setFavorite(4, true)).rejects.toBeInstanceOf(ApiError);
  off();
  expect(heard).toEqual([[3, true], [3, false]]);
});

// 视频 (§18.2)
test("videoSearch sends record=1 only for a submitted search; videoRelated encodes the id", async () => {
  const f = mockFetch({
    "GET /api/v1/videos/search": () => ({ body: { videos: [{ id: "dQw4w9WgXcQ", title: "t", channel: "c", url: "", thumbnail: "/api/v1/videos/dQw4w9WgXcQ/thumbnail", duration_s: 1 }] } }),
    "GET /api/v1/videos/dQw4w9WgXcQ/related": () => ({ body: { videos: [] } }),
  });
  expect((await api.videoSearch("邓 紫棋", false)).videos).toHaveLength(1);
  await api.videoSearch("a&b", true);
  await api.videoRelated("dQw4w9WgXcQ");
  expect(f.mock.calls.map(([u]) => u)).toEqual([
    "/api/v1/videos/search?q=%E9%82%93%20%E7%B4%AB%E6%A3%8B",
    "/api/v1/videos/search?q=a%26b&record=1",
    "/api/v1/videos/dQw4w9WgXcQ/related",
  ]);
});

test("recordWatch posts exactly the five fields the server accepts", async () => {
  const f = mockFetch({ "POST /api/v1/me/video-history/watches": () => ({ status: 204 }) });
  // A wider object (a whole YTVideo, say) must not leak extra fields: the server's decoder is strict.
  const wide = { video_id: "dQw4w9WgXcQ", title: "t", channel: "c", channel_id: "UC0e5c4U67Vm6sAVK0vxN3Uw", duration_s: 212, thumbnail: "x", url: "y" };
  await api.recordWatch(wide);
  expect(JSON.parse(f.mock.calls[0][1]!.body as string)).toEqual({ video_id: "dQw4w9WgXcQ", title: "t", channel: "c", channel_id: "UC0e5c4U67Vm6sAVK0vxN3Uw", duration_s: 212 });
  await api.recordWatch({ video_id: "dQw4w9WgXcQ", title: "t", channel: "c", duration_s: 1 });
  expect(Object.keys(JSON.parse(f.mock.calls[1][1]!.body as string)).sort()).toEqual(["channel", "duration_s", "title", "video_id"]);
});

test("video history, clearing it and 为你推荐 hit the right routes", async () => {
  const f = mockFetch({
    "GET /api/v1/me/video-history": () => ({ body: { watches: [], searches: ["q"] } }),
    "DELETE /api/v1/me/video-history": () => ({ status: 204 }),
    "GET /api/v1/me/video-recommendations": () => ({ body: { items: [], refreshed_at: null, refreshing: true } }),
  });
  expect((await api.videoHistory()).searches).toEqual(["q"]);
  await api.clearVideoHistory();
  expect((await api.videoRecommendations()).refreshing).toBe(true);
  expect(f.mock.calls.map(([u, i]) => `${(i as RequestInit).method} ${u}`)).toEqual([
    "GET /api/v1/me/video-history", "DELETE /api/v1/me/video-history", "GET /api/v1/me/video-recommendations",
  ]);
});

test("startPreview asks for hd", async () => {
  const f = mockFetch({ "POST /api/v1/previews": () => ({ status: 201, body: { id: 3, media: "hd", status: "downloading", progress: 12.5 } }) });
  expect((await api.startPreview({ video_id: "dQw4w9WgXcQ", media: "hd" })).progress).toBe(12.5);
  expect(JSON.parse(f.mock.calls[0][1]!.body as string)).toEqual({ video_id: "dQw4w9WgXcQ", media: "hd" });
});
