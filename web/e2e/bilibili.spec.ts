import { expect, test } from "./fixtures";
import { login } from "./playback";

// Admin turns Bilibili on next to YouTube; search shows the switch, and
// 哔哩哔哩 answers from the (fake) Bilibili search. Sources are put back so
// the other specs, sharing this server, see YouTube only.
test("both sources on: the search switch moves to Bilibili", async ({ page }) => {
  await login(page);
  const setSources = (sources: string[]) =>
    page.evaluate(async (s) => {
      const r = await fetch("/api/v1/admin/video-sources", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ sources: s }),
      });
      return r.status;
    }, sources);
  expect(await setSources(["youtube", "bilibili"])).toBe(200);
  try {
    await page.reload(); // /info is read at start
    await page.getByRole("link", { name: "搜索" }).click();
    await page.getByRole("searchbox").fill("zzzz-bili");
    await page.getByRole("radio", { name: "哔哩哔哩" }).check({ force: true, timeout: 15_000 });
    await expect(page.locator(".yt-results li", { hasText: "哔哩测试歌曲" })).toBeVisible({ timeout: 15_000 });
  } finally {
    await setSources(["youtube"]);
  }
});
