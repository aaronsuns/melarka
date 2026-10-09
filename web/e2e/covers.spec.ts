import { expect, test } from "./fixtures";
import { login } from "./playback";

test("covers load from the folder image; songs without one keep their initials", async ({ page }) => {
  await login(page);
  await page.getByRole("link", { name: "音乐库" }).click();
  await page.getByRole("tab", { name: "专辑" }).click();
  const album = page.locator(".card", { hasText: "邓丽君精选" });
  await expect(album.locator("img.cover-img.loaded")).toBeVisible({ timeout: 15_000 });
  const box = await album.locator(".cover").boundingBox();
  expect(Math.round(box!.width)).toBe(Math.round(box!.height)); // square tile, no shift
  await page.getByRole("link", { name: "搜索" }).click();
  await page.getByRole("searchbox").fill("Faded");
  const faded = page.locator("li", { has: page.locator(".track-main", { hasText: "Faded" }) });
  await expect(faded.locator(".cover")).toBeVisible();
  await expect(faded.locator("img.cover-img")).toHaveCount(0, { timeout: 15_000 }); // 404 → initials only
  const res = await page.request.get("/api/v1/albums/1/cover?size=1000");
  expect([200, 404]).toContain(res.status()); // never a 500
});
