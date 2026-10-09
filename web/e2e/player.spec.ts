import { expect, test } from "@playwright/test";
import { login, trackIdByTitle, verifyPlaybackStarted } from "./playback";

test("login, pinyin search, play, favorite, queue survives reload", async ({ page }, info) => {
  await login(page);

  await page.getByRole("link", { name: "搜索" }).click();
  await page.getByRole("searchbox").fill("tmm");
  const row = page.locator(".track-main", { hasText: "甜蜜蜜" });
  await expect(row).toBeVisible({ timeout: 15_000 }); // first scan may still be running
  // verifyPlaybackStarted proves real playback (progress bar advancing),
  // not just that play() was called. If 甜蜜蜜 is already the server-restored
  // "current" track (another e2e test left a queue behind), the browser
  // would resume it from cache without firing a new /stream request at all
  // — a naive waitForResponse gate would hang until the track naturally
  // finishes (~30s) and the queue auto-advances elsewhere. Its own fallback
  // is scoped to this track's id and registered before the click, so it
  // can't be satisfied — or blocked — by an unrelated background request.
  const trackId = await trackIdByTitle(page, "甜蜜蜜");
  await verifyPlaybackStarted(page, info, () => row.click(), { trackId });
  await expect(page.locator(".mini")).toContainText("甜蜜蜜");
  await expect(page.locator(".mini").getByRole("button", { name: "暂停" })).toBeVisible();

  await page.locator(".mini-info").click();
  const now = page.getByRole("dialog", { name: "正在播放" });
  await expect(now).toBeVisible();
  const fav = now.getByRole("button", { name: /收藏/ });
  if ((await fav.getAttribute("aria-label")) === "收藏") await fav.click();
  await expect(now.getByRole("button", { name: "取消收藏" })).toBeVisible();
  await now.getByRole("button", { name: "收起" }).click();

  await page.getByRole("link", { name: "音乐库" }).click();
  await page.getByRole("tab", { name: "收藏" }).click();
  await expect(page.locator(".track-main", { hasText: "甜蜜蜜" })).toBeVisible();
  await page.screenshot({ path: info.outputPath("favorites.png"), fullPage: true });

  // Review focus 5: nothing overflows horizontally at this width.
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);

  // The queue surviving a reload is the "resume" behaviour: pin it (a
  // fresh user would open with a shuffle of their favorites instead).
  await page.getByRole("link", { name: "我的" }).click();
  await page.getByLabel("打开 Melarka 时").selectOption("resume");
  await page.waitForTimeout(1500); // queue save is debounced 1 s
  await page.reload();
  await expect(page.locator(".mini")).toContainText("甜蜜蜜");
});

test("Library 标签 tab shows the folder-rule tag 经典老歌", async ({ page }) => {
  await login(page);
  await page.getByRole("link", { name: "音乐库" }).click();
  await page.getByRole("tab", { name: "标签" }).click();
  await expect(page.getByRole("link", { name: /^经典老歌 · \d+$/ })).toBeVisible({ timeout: 15_000 });
});
