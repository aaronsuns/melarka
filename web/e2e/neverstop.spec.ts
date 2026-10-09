import { expect, test, type Page } from "@playwright/test";
import { login, trackIdByTitle } from "./playback";

async function miniProgressPct(page: Page): Promise<number> {
  const style = (await page.locator(".mini-progress").getAttribute("style")) ?? "";
  const m = /width:\s*([\d.]+)%/.exec(style);
  return m ? parseFloat(m[1]) : 0;
}

const streamKey = (id: number) => `/api/v1/tracks/${id}/stream?quality=high`;

// Never stop: a track ends while the page is hidden (a locked phone) and
// the next one could only come over the network — which is blocked here.
// The player must go on with a cached favorite instead of going silent.
// Chromium only: Playwright's WebKit doesn't run service workers for this
// origin, and only Chromium lets context.route see the worker's requests.
test("hidden, with the next track unreachable, playback continues with a cached favorite", async ({ page, context, browserName }) => {
  test.skip(browserName !== "chromium", "service-worker network interception is Chromium-only in Playwright");
  await login(page);
  const fav = (await trackIdByTitle(page, "甜蜜蜜"))!;
  const first = (await trackIdByTitle(page, "月亮代表我的心"))!;
  const unreachable = (await trackIdByTitle(page, "Faded"))!;
  const wasFav = async (id: number) => (await (await page.request.get(`/api/v1/tracks/${id}`)).json()).favorite as boolean;
  const before = { fav: await wasFav(fav), first: await wasFav(first), unreachable: await wasFav(unreachable) };
  expect((await page.request.put(`/api/v1/favorites/${fav}`)).status()).toBe(204);
  for (const id of [first, unreachable]) await page.request.delete(`/api/v1/favorites/${id}`);
  try {
    await page.getByRole("link", { name: "我的" }).click();
    await page.getByRole("button", { name: "立即缓存收藏" }).click();
    await expect
      .poll(() => page.evaluate(async (k) => !!(await (await caches.open("lark-offline-v1")).match(k)), streamKey(fav)), { timeout: 30_000 })
      .toBe(true);
    // Every cached favorite's title: whichever one the player falls back to.
    const cachedTitles = await page.evaluate(async () => {
      const c = await caches.open("lark-offline-v1");
      const favs = (await (await fetch("/api/v1/tracks?favorite=1&limit=500", { credentials: "same-origin" })).json()).items as { id: number; title: string }[];
      const out: string[] = [];
      for (const f of favs) if (await c.match(`/api/v1/tracks/${f.id}/stream?quality=high`)) out.push(f.title);
      return out;
    });
    expect(cachedTitles).toContain("甜蜜蜜");
    expect(cachedTitles).not.toContain("Faded");

    // The queue to resume: 月亮代表我的心 5 s before its end, then Faded. Set
    // with the app closed — its own save on leaving would overwrite it.
    await page.evaluate(() => navigator.serviceWorker.ready);
    await page.goto("about:blank");
    expect(
      (await page.request.put("/api/v1/queue", { data: { track_ids: [first, unreachable], current_index: 0, position_ms: 25_000 } })).status(),
    ).toBe(200);

    // From here on no stream but the first track's reaches the server — for
    // the page and the worker alike. The player's own request for the next
    // track hangs, like a car's weak signal (nothing fails, nothing arrives).
    const blocked: string[] = [];
    await context.route(/\/api\/v1\/tracks\/\d+\/stream/, (route) => {
      const url = route.request().url();
      if (url.includes(`/tracks/${first}/stream`)) return route.continue();
      blocked.push(url);
      if (url.includes(`/tracks/${unreachable}/stream`) && !url.includes("lark_sw=bypass")) return new Promise<void>(() => {});
      return route.abort("internetdisconnected");
    });

    await page.goto("/");
    await expect.poll(() => page.evaluate(() => !!navigator.serviceWorker.controller), { timeout: 15_000 }).toBe(true);
    await expect(page.locator(".mini-info .ellipsis").first()).toHaveText("月亮代表我的心");
    await page.locator(".mini").getByRole("button", { name: "播放" }).click();
    await expect(page.locator(".mini").getByRole("button", { name: "暂停" })).toBeVisible();

    // The phone is locked.
    await page.evaluate(() => {
      Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
      document.dispatchEvent(new Event("visibilitychange"));
    });

    await expect.poll(() => page.locator(".mini-info .ellipsis").first().textContent(), { timeout: 25_000 }).not.toBe("月亮代表我的心");
    const now = await page.locator(".mini-info .ellipsis").first().textContent();
    expect(cachedTitles).toContain(now);
    await expect.poll(() => miniProgressPct(page), { timeout: 20_000, intervals: [300] }).toBeGreaterThan(0);
    // The unreachable track was never asked for by the player (its
    // lookahead download, marked lark_sw=bypass, may have been).
    expect(blocked.filter((u) => u.includes(`/tracks/${unreachable}/stream`) && !u.includes("lark_sw=bypass"))).toEqual([]);
    await page.locator(".mini").getByRole("button", { name: "暂停" }).click();
  } finally {
    await context.unrouteAll({ behavior: "ignoreErrors" });
    for (const [id, was] of [[fav, before.fav], [first, before.first], [unreachable, before.unreachable]] as const) {
      if (was) await page.request.put(`/api/v1/favorites/${id}`);
      else await page.request.delete(`/api/v1/favorites/${id}`);
    }
  }
});
