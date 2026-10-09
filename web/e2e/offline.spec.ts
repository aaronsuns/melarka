import { expect, test, type Page } from "@playwright/test";
import { login, trackIdByTitle } from "./playback";

async function miniProgressPct(page: Page): Promise<number> {
  const style = (await page.locator(".mini-progress").getAttribute("style")) ?? "";
  const m = /width:\s*([\d.]+)%/.exec(style);
  return m ? parseFloat(m[1]) : 0;
}

// The service worker's own network requests are only visible to (and
// blockable by) context.route in Chromium, and Playwright's WebKit doesn't
// run service workers for this origin — the proof is Chromium's.
test("a cached favorite plays with the server's stream blocked", async ({ page, context, browserName }) => {
  test.skip(browserName !== "chromium", "service-worker network interception is Chromium-only in Playwright");
  await login(page);
  const id = (await trackIdByTitle(page, "甜蜜蜜"))!;
  const wasFavorite = (await (await page.request.get(`/api/v1/tracks/${id}`)).json()).favorite as boolean;
  expect((await page.request.put(`/api/v1/favorites/${id}`)).status()).toBe(204);
  try {
    await page.getByRole("link", { name: "我的" }).click();
    await page.getByRole("button", { name: "立即缓存收藏" }).click();
    await expect
      .poll(
        () =>
          page.evaluate(async (k) => !!(await (await caches.open("lark-offline-v1")).match(k)), `/api/v1/tracks/${id}/stream?quality=high`),
        { timeout: 30_000 },
      )
      .toBe(true);
    await expect(page.getByText(/^已缓存 \d+ 首$/)).toBeVisible();

    // A reload puts the page under the (already active) service worker.
    await page.evaluate(() => navigator.serviceWorker.ready);
    await page.reload();
    await expect.poll(() => page.evaluate(() => !!navigator.serviceWorker.controller), { timeout: 15_000 }).toBe(true);

    // From here on the server's audio is unreachable — for the page and the worker alike.
    const blocked: string[] = [];
    await context.route(/\/api\/v1\/tracks\/\d+\/stream/, (route) => {
      blocked.push(route.request().url());
      return route.abort("internetdisconnected");
    });

    await page.getByRole("link", { name: "音乐库" }).click();
    const row = page.locator("li.track", { hasText: "甜蜜蜜" }).first();
    await expect(row.getByRole("img", { name: "已离线缓存" })).toBeVisible();
    await row.locator(".track-main").click();
    await expect.poll(() => miniProgressPct(page), { timeout: 20_000, intervals: [300] }).toBeGreaterThan(0);
    await expect(page.locator(".mini-info .ellipsis").first()).toHaveText("甜蜜蜜");
    // Whatever else was asked for (the next track's warm-up), the cached track never reached the network.
    expect(blocked.filter((u) => u.includes(`/tracks/${id}/stream`))).toEqual([]);
    await page.locator(".mini").getByRole("button", { name: "暂停" }).click();
  } finally {
    await context.unrouteAll({ behavior: "ignoreErrors" });
    if (!wasFavorite) await page.request.delete(`/api/v1/favorites/${id}`);
  }
});
