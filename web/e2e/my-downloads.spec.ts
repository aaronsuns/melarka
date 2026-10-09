import { expect, test } from "@playwright/test";
import { login } from "./playback";

test("a finished download appears in My downloads and plays from Playlists", async ({ page }) => {
  await login(page);
  await page.evaluate(async () => {
    await fetch("/api/v1/downloads", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ url: "https://www.youtube.com/watch?v=fakevideo01" }) });
  });
  await expect.poll(async () => page.evaluate(async () =>
    ((await (await fetch("/api/v1/downloads/tracks", { credentials: "same-origin" })).json()) as { title: string }[]).map((t) => t.title).join("|"),
  ), { timeout: 30_000 }).toContain("测试歌曲");
  // ▶ plays the list from its top: the newest download, which other specs
  // (playlists, recommendations) may have added after 测试歌曲.
  const newest = await page.evaluate(async () =>
    ((await (await fetch("/api/v1/downloads/tracks", { credentials: "same-origin" })).json()) as { title: string }[])[0].title);
  await page.goto("/playlists");
  await page.getByRole("button", { name: "播放我的下载", exact: true }).click();
  await expect(page.locator(".mini")).toContainText(newest, { timeout: 10_000 });
  await page.getByRole("link", { name: /我的下载/ }).click();
  await expect(page.getByRole("heading", { name: "我的下载" })).toBeVisible();
  await expect(page.locator(".track-main", { hasText: "测试歌曲" })).toBeVisible();
});
