import { expect, test } from "@playwright/test";
import { login } from "./playback";

test("▶ 试听 an episode that isn't downloaded, then 保留到频道 without downloading again", async ({ page }, info) => {
  await login(page);
  // Idempotent: channels.spec (which runs first) already follows it; this spec doesn't rely on that.
  await page.request.post("/api/v1/channels/follow", { data: { url: "https://www.youtube.com/@fakechannel" } });
  // One older episode per project (both share a server).
  const title = info.project.name === "iphone-webkit" ? "假节目 旧的A" : "假节目 旧的B";
  await page.goto("/channels/UCfakechannel00000000001");
  const row = page.locator(".episode-row", { hasText: title });
  await expect(row).toBeVisible({ timeout: 15_000 });
  const preview = row.getByRole("button", { name: "▶ 试听" });
  if (!(await preview.isVisible())) {
    // exact: "听" is also a substring of "▶ 试听".
    await expect(row.getByRole("button", { name: "听", exact: true })).toBeVisible(); // kept by an earlier run of this project
    return;
  }
  const stream = page.waitForResponse((r) => r.url().includes("/api/v1/previews/") && r.url().endsWith("/stream") && r.status() < 300, { timeout: 30_000 });
  await preview.click();
  const sheet = page.getByRole("dialog", { name: title });
  await expect(sheet).toBeVisible();
  await stream;
  const keep = sheet.getByRole("button", { name: "保留到频道" });
  await expect(keep).toBeEnabled({ timeout: 30_000 });
  await keep.click();
  await expect(sheet.getByText("已保留到频道")).toBeVisible();
  await sheet.getByRole("button", { name: "关闭试听" }).click();
  await expect(sheet).toHaveCount(0);
  await page.goto("/channels?tab=kept");
  await expect(page.locator(".episode-row", { hasText: title }).getByRole("button", { name: "听", exact: true })).toBeVisible({ timeout: 15_000 });
});
