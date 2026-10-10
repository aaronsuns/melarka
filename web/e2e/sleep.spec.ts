import { expect, test } from "./fixtures";
import { login, trackIdByTitle, verifyPlaybackStarted } from "./playback";

// The sleep timer's menu and 🌙 chip in a real engine: ⋯ → 15 分钟 shows the
// chip counting down; the chip opens the same menu, where 关闭定时 removes it.
test("sleep timer: set from ⋯, the chip counts down, turn it off from the chip", async ({ page }) => {
  await login(page);
  const title = "Faded";
  await expect.poll(() => trackIdByTitle(page, title), { timeout: 15_000 }).toBeDefined();
  const id = (await trackIdByTitle(page, title))!;
  await page.goto("about:blank");
  expect((await page.request.put("/api/v1/queue", { data: { track_ids: [id], current_index: 0, position_ms: 0 } })).status()).toBe(200);
  await page.goto("/");
  await expect(page.locator(".mini")).toContainText(title);

  await page.locator(".mini-info").click();
  const now = page.getByRole("dialog", { name: "正在播放" });
  await now.getByRole("button", { name: "更多" }).click();
  await now.getByRole("menu", { name: "睡眠定时" }).getByRole("menuitem", { name: "15 分钟" }).click();
  const chip = now.getByRole("button", { name: /^睡眠定时，剩余 1[45]:\d\d$/ });
  await expect(chip).toBeVisible();
  await expect(chip).toContainText("🌙");
  await expect(chip).toHaveText(/14:5\d/, { timeout: 5000 }); // it ticks

  await chip.click();
  await now.getByRole("menuitem", { name: "关闭定时" }).click();
  await expect(now.getByRole("button", { name: /^睡眠定时/ })).toHaveCount(0);
});

// At the deadline the music fades out over 10 s and pauses, and the chip goes.
// The page's clock is faked so the test needn't wait 15 minutes: installed
// before the app loads, it runs on in real time until fast-forwarded.
test("sleep timer: at the deadline playback pauses and the chip goes", async ({ page }, info) => {
  await page.clock.install();
  await login(page);
  const title = "甜蜜蜜"; // 3 minutes: still playing when the timer fires
  await expect.poll(() => trackIdByTitle(page, title), { timeout: 15_000 }).toBeDefined();
  const id = (await trackIdByTitle(page, title))!;
  await page.goto("about:blank");
  expect((await page.request.put("/api/v1/queue", { data: { track_ids: [id], current_index: 0, position_ms: 0 } })).status()).toBe(200);
  await page.goto("/");
  await expect(page.locator(".mini")).toContainText(title);

  await page.locator(".mini-info").click();
  const now = page.getByRole("dialog", { name: "正在播放" });
  await verifyPlaybackStarted(page, info, () => now.getByRole("button", { name: "播放", exact: true }).click(), { trackId: id });
  await now.getByRole("button", { name: "更多" }).click();
  await now.getByRole("menu", { name: "睡眠定时" }).getByRole("menuitem", { name: "15 分钟" }).click();
  await expect(now.getByRole("button", { name: /^睡眠定时，剩余 1[45]:\d\d$/ })).toBeVisible();
  await expect(now.getByRole("button", { name: "暂停" })).toBeVisible();

  await page.clock.fastForward("15:00");
  // The fade's 10 s then run in real time.
  await expect(now.getByRole("button", { name: "播放", exact: true })).toBeVisible({ timeout: 20_000 });
  await expect(now.getByRole("button", { name: /^睡眠定时/ })).toHaveCount(0);
  await expect(page.locator(".mini")).toContainText(title); // paused, not skipped
});
