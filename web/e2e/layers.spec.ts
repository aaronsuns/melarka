import { expect, test } from "./fixtures";
import { login, trackIdByTitle } from "./playback";

// Now Playing covers everything in the page: even a row's ⋯ menu left open
// under it (the bottom bars are a stacking context; Now Playing renders at
// the top of the page, so nothing in the content can paint over it). And the
// ⋯ menu closes on a tap outside it.
const mine = ["月亮代表我的心", "Faded", "甜蜜蜜"];

test("an open ⋯ menu never paints over Now Playing; a tap outside closes it", async ({ page }) => {
  await login(page);
  const ids: number[] = [];
  for (const title of mine) {
    await expect.poll(() => trackIdByTitle(page, title), { timeout: 15_000 }).toBeDefined(); // first scan may still be running
    ids.push((await trackIdByTitle(page, title))!);
  }
  // A paused queue to resume, set with the app closed: its own save on leaving would overwrite it.
  await page.goto("about:blank");
  expect((await page.request.put("/api/v1/queue", { data: { track_ids: ids, current_index: 0, position_ms: 0 } })).status()).toBe(200);
  await page.goto("/");
  await expect(page.locator(".mini")).toContainText(mine[0]);

  await page.getByRole("link", { name: "音乐库" }).click();
  await page.getByRole("tab", { name: "歌曲" }).click();
  await page.getByRole("button", { name: `更多：${mine[1]}` }).click();
  const menu = page.getByRole("menu");
  await expect(menu).toBeVisible();

  // Opened from the keyboard: a tap would close the menu first.
  await page.locator(".mini-info").focus();
  await page.keyboard.press("Enter");
  const now = page.getByRole("dialog", { name: "正在播放" });
  await expect(now).toBeVisible();
  await expect(menu).toHaveCount(1); // still open, underneath
  const box = (await menu.boundingBox())!;
  const top = await page.evaluate(([x, y]) => !!document.elementFromPoint(x, y)?.closest(".now"), [box.x + box.width / 2, box.y + box.height / 2]);
  expect(top, "Now Playing is on top of the menu").toBe(true);
  // The tab bar and the mini player are under it too.
  const tabs = (await page.locator("nav.tabs").boundingBox())!;
  expect(await page.evaluate(([x, y]) => !!document.elementFromPoint(x, y)?.closest(".now"), [tabs.x + tabs.width / 2, tabs.y + tabs.height / 2])).toBe(true);
  await now.getByRole("button", { name: "收起" }).click();
  await expect(now).toHaveCount(0);

  // A tap outside the menu closes it (收起 just did); reopen it and tap the title.
  await expect(menu).toHaveCount(0);
  await page.getByRole("button", { name: `更多：${mine[1]}` }).click();
  await expect(menu).toBeVisible();
  await page.getByRole("heading", { name: "音乐库" }).click();
  await expect(menu).toHaveCount(0);
});
