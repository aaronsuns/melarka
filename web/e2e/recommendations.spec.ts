import { expect, test } from "@playwright/test";
import { login, trackIdByJobTitle, trackIdByTitle, verifyPlaybackStarted } from "./playback";

test("为你推荐: a real listen seeds it, 刷新 fills it, 下载 → ▶ 播放 from Home", async ({ page }, info) => {
  await login(page);
  // One 30 s listen of 甜蜜蜜 makes it a seed (its video is found by the fake seed search).
  const seed = await trackIdByTitle(page, "甜蜜蜜");
  expect(seed).toBeTruthy();
  await page.evaluate(async (id) => {
    const r = await fetch("/api/v1/events/play", {
      method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ events: [{ client_event_id: `e2e-recs-${Date.now()}`, track_id: id, played_seconds: 30 }] }),
    });
    if (!r.ok) throw new Error(`play event: ${r.status}`);
  }, seed);

  // The two projects share one server: the first refreshes; the second may
  // be told it is too soon, and finds the list already there.
  await page.getByRole("link", { name: "首页" }).click();
  const section = page.locator("section.recs-section");
  await expect(section.getByRole("heading", { name: "为你推荐" })).toBeVisible({ timeout: 15_000 });
  await page.goto("/recommendations");
  await page.getByRole("button", { name: "刷新" }).click();
  const pick = info.project.name === "iphone-webkit" ? "推荐歌曲一" : "推荐歌曲二";
  await expect(page.locator(".recs li", { hasText: pick })).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(".recs li", { hasText: "合集" })).toHaveCount(0);
  await expect(page.locator(".recs li", { hasText: pick })).toContainText(/因为你(常听|收藏了)/);

  // Home: the row downloads with live status and becomes ▶ 播放, which plays it.
  await page.getByRole("link", { name: "首页" }).click();
  const row = section.locator("li", { hasText: pick });
  await expect(row).toBeVisible({ timeout: 15_000 });
  await row.getByRole("button", { name: "下载" }).click();
  const play = row.getByRole("button", { name: /播放/ });
  await expect(play).toBeVisible({ timeout: 45_000 });
  const trackId = await trackIdByJobTitle(page, pick);
  expect(trackId).toBeTruthy();
  await verifyPlaybackStarted(page, info, () => play.click(), { trackId });
  await expect(page.locator(".mini")).toContainText(pick);
});
