import { expect, test } from "./fixtures";
import { login } from "./playback";

test("YouTube results: 显示更多 grows the list to the cap; a playlist opens in place", async ({ page }) => {
  await login(page);
  await page.getByRole("link", { name: "搜索" }).click();
  await page.getByRole("searchbox").fill("zzzz-many");

  const rows = page.locator("ul.yt-results:not(.yt-tracks) > li");
  await expect(rows).toHaveCount(10, { timeout: 15_000 });
  const more = page.getByRole("button", { name: "显示更多" });
  for (const n of [20, 30, 40, 50]) {
    await more.click();
    await expect(rows).toHaveCount(n, { timeout: 15_000 });
  }
  // 50 is the cap: no further page, and every video shows once.
  await expect(more).toHaveCount(0);
  const titles = await rows.locator(".row-title").allTextContents();
  expect(new Set(titles).size).toBe(50);
  expect(titles[49]).toBe("多多歌曲 50");

  // The playlist result opens its tracks, each with 试听 and 下载; 下载全部 stays.
  const toggle = page.getByRole("button", { name: "「假歌单」的歌曲" });
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  const panel = page.getByRole("region", { name: "「假歌单」的歌曲" });
  const track = panel.locator("li", { hasText: "第二首歌" });
  await expect(track).toBeVisible({ timeout: 15_000 });
  await expect(track).toContainText("假歌手频道 · 1:39");
  await expect(track.getByRole("button", { name: "试听" })).toBeVisible();
  await expect(track.getByRole("button", { name: "下载" })).toBeVisible();
  await expect(panel.locator("li")).toHaveCount(2);
  await expect(page.locator(".playlist-card", { hasText: "假歌单" }).getByRole("button", { name: "下载全部", exact: true })).toBeVisible();
  // Nothing spills sideways at phone width.
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  await toggle.click();
  await expect(panel).toBeHidden();
});
