import { expect, test } from "@playwright/test";
import { login, trackIdByTitle, verifyPlaybackStarted } from "./playback";

test("Now Playing opens on the synced lyrics from the sidecar .lrc; they follow playback, a tapped line seeks, and the cover shows the current line", async ({ page }, info) => {
  await login(page);
  await page.getByRole("link", { name: "搜索" }).click();
  await page.getByRole("searchbox").fill("甜蜜蜜");
  const row = page.locator(".track-main", { hasText: "甜蜜蜜" });
  await expect(row).toBeVisible({ timeout: 15_000 }); // first scan may still be running
  const trackId = await trackIdByTitle(page, "甜蜜蜜");
  await verifyPlaybackStarted(page, info, () => row.click(), { trackId });

  await page.locator(".mini-info").click();
  const now = page.getByRole("dialog", { name: "正在播放" });
  // The track has synced lyrics: Now Playing opens on them, not on the cover.
  await expect(now.getByRole("button", { name: "封面" })).toBeVisible();
  await expect(now.getByRole("button", { name: "歌词", exact: true })).toHaveCount(0);
  for (const line of ["甜蜜蜜第一句", "甜蜜蜜第二句", "甜蜜蜜第三句"]) {
    await expect(now.getByRole("button", { name: line })).toBeVisible();
  }
  await expect(now.locator('.lyrics-synced [aria-current="true"]')).toHaveCount(1, { timeout: 6_000 });
  await page.screenshot({ path: info.outputPath("lyrics.png"), fullPage: true });

  await now.getByRole("button", { name: "甜蜜蜜第三句" }).click();
  await expect(async () => {
    const shown = (await now.locator(".now-times span").first().textContent()) ?? "";
    const [m, s] = shown.split(":").map(Number);
    expect(m * 60 + s).toBeGreaterThanOrEqual(4);
  }).toPass({ timeout: 5_000 });

  await now.getByRole("button", { name: "封面" }).click();
  await expect(now.getByRole("button", { name: "歌词", exact: true })).toBeVisible();
  // On the cover, the current line shows under the title, and the mini player
  // carries it in place of the artist.
  await expect(now.locator('.now-lyric-strip [aria-current="true"]')).toHaveText(/甜蜜蜜第[一二三]句/);
  await expect(page.locator(".mini-info .track-text .muted")).toHaveText(/甜蜜蜜第[一二三]句/);

  // The cover choice is remembered: reopening stays on the cover.
  await now.getByRole("button", { name: "收起" }).click();
  await page.locator(".mini-info").click();
  await expect(now.getByRole("button", { name: "歌词", exact: true })).toBeVisible();
  await now.getByRole("button", { name: "歌词", exact: true }).click();
  await expect(now.getByRole("button", { name: "甜蜜蜜第一句" })).toBeVisible();
});

test("Change lyrics: broad search, a delete that asks first inside the row, and the no-lyrics action fit a phone", async ({ page }, info) => {
  await login(page);
  await page.getByRole("link", { name: "搜索" }).click();
  await page.getByRole("searchbox").fill("甜蜜蜜");
  const row = page.locator(".track-main", { hasText: "甜蜜蜜" });
  await expect(row).toBeVisible({ timeout: 15_000 });
  const trackId = await trackIdByTitle(page, "甜蜜蜜");
  await verifyPlaybackStarted(page, info, () => row.click(), { trackId });
  await page.locator(".mini-info").click();
  const now = page.getByRole("dialog", { name: "正在播放" });
  await now.getByRole("button", { name: "更换歌词" }).click();
  const sheet = page.getByRole("dialog", { name: "更换歌词" });
  const cand = sheet.locator(".lyrics-cand").first();
  await expect(cand.getByRole("button", { name: /文件内嵌/ })).toHaveAttribute("aria-pressed", "true");

  // Broad search (only the file's own lyrics in e2e): the sidecar stays the pick.
  await sheet.getByRole("button", { name: "宽搜索" }).click();
  await expect(sheet.getByRole("button", { name: "宽搜索" })).toBeEnabled();
  await expect(cand.getByRole("button", { name: /文件内嵌/ })).toHaveAttribute("aria-pressed", "true");

  await cand.getByRole("button", { name: "删除" }).click();
  await expect(cand.getByText("删除这份歌词？")).toBeVisible();
  await expect(cand.getByRole("button", { name: "确认删除" })).toBeVisible();
  await expect(sheet.getByRole("button", { name: "这首没有歌词" })).toBeVisible();
  await sheet.getByRole("button", { name: "这首没有歌词" }).click();
  await expect(sheet.getByText("这首歌没有歌词？之后不再自动查找。")).toBeVisible();
  await expect(sheet.getByRole("button", { name: "确认没有" })).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
  await page.screenshot({ path: info.outputPath("lyrics-picker.png"), fullPage: true });
  await cand.getByRole("button", { name: "保留" }).click(); // keep it: other specs need these lyrics
  await expect(cand.getByText("删除这份歌词？")).toHaveCount(0);
  await sheet.getByRole("button", { name: "不，继续找" }).click();
  await expect(sheet.getByRole("button", { name: "这首没有歌词" })).toBeVisible();
  await sheet.getByRole("button", { name: "取消" }).click();
  await expect(now.getByRole("button", { name: "甜蜜蜜第一句" })).toBeVisible();
});

test("the lyrics view's shift, align and wrong-lyrics controls fit a phone, and a shift is saved for everyone", async ({ page }, info) => {
  await login(page);
  await page.getByRole("link", { name: "搜索" }).click();
  await page.getByRole("searchbox").fill("甜蜜蜜");
  const row = page.locator(".track-main", { hasText: "甜蜜蜜" });
  await expect(row).toBeVisible({ timeout: 15_000 });
  const trackId = await trackIdByTitle(page, "甜蜜蜜");
  // Start from no shift, whatever an earlier (failed) run left on the server.
  const shown = await (await page.request.get(`/api/v1/tracks/${trackId}/lyrics`)).json();
  if (shown.offset_ms) {
    expect((await page.request.put(`/api/v1/tracks/${trackId}/lyrics/offset`, { data: { offset_ms: 0, lyrics_id: shown.id } })).ok()).toBe(true);
  }
  await verifyPlaybackStarted(page, info, () => row.click(), { trackId });
  await page.locator(".mini-info").click();
  const now = page.getByRole("dialog", { name: "正在播放" });
  await expect(now.getByRole("button", { name: "甜蜜蜜第一句" })).toBeVisible();

  const width = page.viewportSize()!.width;
  for (const name of ["歌词提前 0.5 秒", "歌词延后 0.5 秒", "歌词不对"]) {
    const box = (await now.getByRole("button", { name }).boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(width);
  }
  const offsetOnServer = async () => (await (await page.request.get(`/api/v1/tracks/${trackId}/lyrics`)).json()).offset_ms;
  const saved = (ms: number) => page.waitForResponse((r) => r.url().endsWith(`/tracks/${trackId}/lyrics/offset`) && r.request().method() === "PUT" && JSON.parse(r.request().postData() ?? "{}").offset_ms === ms);

  let put = saved(1000);
  await now.getByRole("button", { name: "歌词延后 0.5 秒" }).click();
  await now.getByRole("button", { name: "歌词延后 0.5 秒" }).click();
  await expect(now.locator(".lyrics-offset")).toHaveText("+1.0s");
  await put;
  expect(await offsetOnServer()).toBe(1000);

  // Long-press a line: aligned to now, saved at once. The new shift is the
  // playback position minus the line's time (0.5 s), so first move playback
  // well past that line: aligned right at 0.5 s, the shift would come out 0.0
  // and leave nothing for 复位 to reset, however fast the test got there.
  await now.getByRole("button", { name: "甜蜜蜜第三句" }).click();
  await expect(async () => {
    const shown = (await now.locator(".now-times span").first().textContent()) ?? "";
    const [m, s] = shown.split(":").map(Number);
    expect(m * 60 + s).toBeGreaterThanOrEqual(4);
  }).toPass({ timeout: 5_000 });
  const line = now.getByRole("button", { name: "甜蜜蜜第一句" });
  const aligned = page.waitForResponse((r) => r.url().endsWith(`/tracks/${trackId}/lyrics/offset`) && r.request().method() === "PUT");
  // hover waits for the line to stop moving: the list scrolls (smoothly) to
  // the line just tapped, and a press on a moving line lands elsewhere.
  await line.hover();
  // Held until the alignment is saved, not for a fixed time: a busy page may
  // run its long-press timer late, and a release before it fires is a tap.
  await page.mouse.down();
  const alignedRes = await aligned;
  await page.mouse.up();
  expect(JSON.parse(alignedRes.request().postData() ?? "{}").offset_ms).toBeGreaterThanOrEqual(3000);
  await expect(now.getByText("已对齐")).toBeVisible();

  // Wrong lyrics asks first; keep them here.
  await now.getByRole("button", { name: "歌词不对" }).click();
  await expect(now.getByRole("button", { name: "是，不对" })).toBeVisible();
  await now.getByRole("button", { name: "保留" }).click();

  put = saved(0);
  await now.getByRole("button", { name: "复位" }).click();
  await put;
  expect(await offsetOnServer()).toBe(0);
});
