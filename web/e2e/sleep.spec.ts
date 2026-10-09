import { expect, test } from "@playwright/test";
import { login, trackIdByTitle } from "./playback";

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
