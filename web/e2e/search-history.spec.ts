import { expect, test } from "@playwright/test";
import { login } from "./playback";

test("searches are remembered as chips and can be cleared", async ({ page }) => {
  await login(page);
  await page.getByRole("link", { name: "搜索" }).click();
  const box = page.getByRole("searchbox");
  await box.fill("甜蜜蜜");
  await box.press("Enter");
  await box.fill("");
  const recent = page.getByRole("group", { name: "最近搜索" });
  await expect(recent.getByRole("button", { name: "甜蜜蜜" })).toBeVisible();
  await page.reload();
  await expect(recent.getByRole("button", { name: "甜蜜蜜" })).toBeVisible(); // stored on the server
  await recent.getByRole("button", { name: "甜蜜蜜" }).click();
  await expect(box).toHaveValue("甜蜜蜜");
  await box.fill("");
  await recent.getByRole("button", { name: "清除" }).click();
  await expect(page.getByRole("heading", { name: "最近搜索" })).toHaveCount(0);
});
