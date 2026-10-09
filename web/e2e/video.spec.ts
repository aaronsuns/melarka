import { expect, test, type Page, type TestInfo } from "@playwright/test";
import { login, loginWith } from "./playback";

// The card grid under a 视频 section heading.
const grid = (page: Page, heading: string) => page.locator("h2", { hasText: heading }).locator("xpath=following-sibling::div[1]");

// Both projects share one server. Each signs in as its own member (its own
// 视频 history, 为你推荐 and kept episodes) and searches with its own words,
// which the fake answers with its own video ids (fakevideo01/02 on webkit,
// fakevideo11/12 on chromium): neither finds the other's previews done.
// fakevideo11's 360p takes the merged path, fakevideo01's the progressive one.
function ids(info: TestInfo) {
  const second = info.project.name === "chromium-360";
  return { query: `测试 ${info.project.name}`, watch: second ? "fakevideo11" : "fakevideo01", keep: second ? "fakevideo12" : "fakevideo02" };
}

async function loginAsProjectMember(page: Page, info: TestInfo) {
  await login(page);
  const username = `v-${info.project.name}`;
  const password = "video-e2e-pass";
  const r = await page.request.post("/api/v1/users", { data: { username, password, role: "member" } });
  expect([201, 409]).toContain(r.status()); // 409: a retry of this test
  await page.context().clearCookies();
  await loginWith(page, username, password);
  // A new user opens on a shuffle of favorites: music that starts on a tap
  // here would take the session from the video. Open on nothing, like the
  // shared admin's "resume" with an empty queue (global-setup.ts).
  const prefs = await (await page.request.get("/api/v1/me/preferences")).json();
  expect((await page.request.put("/api/v1/me/preferences", { data: { ...prefs, on_open: "nothing" } })).ok()).toBe(true);
  await page.reload();
  await expect(page.locator("nav.tabs a").first()).toBeVisible();
}

async function searchVideos(page: Page, query: string) {
  await page.getByRole("link", { name: "视频", exact: true }).click();
  await page.getByRole("searchbox", { name: "搜索视频" }).fill(query);
  await page.getByRole("searchbox", { name: "搜索视频" }).press("Enter");
}

test("视频: search → card → watch plays a 206 preview → 高清 switches at the same second → history and 为你推荐", async ({ page }, info) => {
  const { query, watch } = ids(info);
  await loginAsProjectMember(page, info);
  await searchVideos(page, query);
  const card = page.locator(`.video-card:has(a[href="/watch/${watch}"])`);
  await expect(card).toBeVisible({ timeout: 15_000 });
  // Thumbnails come through Lark (never from YouTube), and this one really loaded.
  const img = card.locator("img");
  await expect(img).toHaveAttribute("src", `/api/v1/videos/${watch}/thumbnail`);
  await expect.poll(() => img.evaluate((i: HTMLImageElement) => (i.complete ? i.naturalWidth : 0)), { timeout: 15_000 }).toBeGreaterThan(0);

  const ranged = page.waitForResponse((r) => /\/api\/v1\/previews\/\d+\/stream$/.test(r.url()) && r.status() === 206, { timeout: 30_000 });
  await card.click();
  // fakevideo11 has no usable format 18 (fake-yt-dlp.sh): its 360p is merged
  // at the end, so the page says 准备中 N% until it is complete, then plays it.
  if (watch === "fakevideo11") await expect(page.getByText(/^准备中 \d+%$/)).toBeVisible({ timeout: 15_000 });
  await ranged;
  const video = page.locator("video.watch-video");
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime), { timeout: 20_000 }).toBeGreaterThan(0);
  await expect(page.getByText("假视频的简介")).toBeVisible();
  await expect(page.getByText("<b>第二行</b>")).toBeVisible(); // text, not markup

  // 高清: wait for the HD file, then the same second — on the HD stream, not the 360p.
  const sd = await video.getAttribute("src");
  expect(sd).toMatch(/^\/api\/v1\/previews\/\d+\/stream$/);
  await video.evaluate((v: HTMLVideoElement) => { v.currentTime = 12; });
  await page.getByRole("button", { name: /^高清/ }).click();
  await expect(page.getByRole("button", { name: "高清 ✓" })).toBeVisible({ timeout: 30_000 });
  const hd = await video.getAttribute("src");
  expect(hd).toMatch(/^\/api\/v1\/previews\/\d+\/stream$/);
  expect(hd).not.toBe(sd);
  const hdInfo = await (await page.request.get(hd!.replace(/\/stream$/, ""))).json();
  expect(hdInfo).toMatchObject({ media: "hd", status: "done", video_id: watch });
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime)).toBeGreaterThanOrEqual(11);

  // 相关视频 from the (fake) Mix, and back on 视频: 最近观看 + 为你推荐 (the Mix minus what was watched).
  await expect(page.locator(".watch-page .video-card", { hasText: "推荐歌手 - 推荐歌曲一" })).toBeVisible({ timeout: 15_000 });
  await page.getByRole("link", { name: "视频", exact: true }).click();
  await page.getByRole("searchbox", { name: "搜索视频" }).fill("");
  await expect(grid(page, "最近观看").locator(`a[href="/watch/${watch}"]`)).toBeVisible();
  await expect(async () => {
    await page.reload();
    await expect(grid(page, "为你推荐").locator(".video-card", { hasText: "推荐歌曲一" })).toBeVisible({ timeout: 2_000 });
  }).toPass({ timeout: 60_000 });
});

test("视频 保留 keeps the watched video in Channels", async ({ page }, info) => {
  const { query, keep: id } = ids(info);
  await loginAsProjectMember(page, info);
  await searchVideos(page, query);
  const card = page.locator(`.video-card:has(a[href="/watch/${id}"])`);
  await expect(card).toBeVisible({ timeout: 15_000 });
  await card.click();
  const keep = page.getByRole("button", { name: "保留", exact: true });
  await expect(keep).toBeEnabled({ timeout: 30_000 });
  await keep.click();
  await expect(page.getByRole("link", { name: "已保留到频道" })).toHaveAttribute("href", `/episodes/${id}`);
  await page.goto("/channels?tab=kept");
  await expect(page.locator(`a[href="/episodes/${id}"]`).first()).toBeVisible({ timeout: 15_000 });
});
