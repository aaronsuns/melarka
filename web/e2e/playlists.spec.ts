import { expect, test } from "@playwright/test";
import { login } from "./playback";

test("a YouTube playlist downloads into a Lark playlist, in order; pasted links offer the right choices", async ({ page }) => {
  await login(page);
  await page.getByRole("link", { name: "搜索" }).click();
  const box = page.getByRole("searchbox");
  await box.fill("zzzz-list");

  const card = page.locator(".playlist-card", { hasText: "假歌单" });
  await expect(card).toBeVisible({ timeout: 15_000 });
  await card.getByRole("button", { name: "下载全部", exact: true }).click();
  await card.getByRole("button", { name: "下载全部（2）" }).click();
  await expect(card.getByText("已加入 2 首到「假歌单」")).toBeVisible({ timeout: 15_000 });
  await card.getByRole("link", { name: "打开歌单" }).click();

  // The songs land as their downloads finish, in YouTube's order.
  await expect(async () => {
    await page.reload();
    const titles = await page.locator(".track-main").allTextContents();
    expect(titles.findIndex((x) => x.includes("测试歌曲"))).toBeGreaterThanOrEqual(0);
    expect(titles.findIndex((x) => x.includes("第二首歌"))).toBeGreaterThan(titles.findIndex((x) => x.includes("测试歌曲")));
  }).toPass({ timeout: 30_000, intervals: [3_000] });

  // Pasted links are offered for download, not searched.
  await page.getByRole("link", { name: "搜索" }).click();
  await box.fill("https://www.youtube.com/watch?v=fakevideo03&list=PLfakelist0001");
  await expect(page.getByRole("button", { name: "这首歌" })).toBeVisible();
  await expect(page.getByRole("button", { name: "整个歌单" })).toBeVisible();

  await box.fill("https://www.youtube.com/watch?v=fakevideo03&list=RDfakevideo03");
  await expect(page.getByRole("button", { name: "下载这首歌" })).toBeVisible();
  await expect(page.getByRole("button", { name: "整个歌单" })).toHaveCount(0);
});
