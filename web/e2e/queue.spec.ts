import { expect, test, type Locator, type Page } from "@playwright/test";
import { login, trackIdByTitle } from "./playback";

// The queue sheet in a real engine (WebKit as on an iPhone, and Chromium):
// the handle drags a row with pointer events, a left swipe removes one, ✕
// shows only where there is hover, and ⋯ → 添加到队列 queues a track.
async function openQueue(page: Page): Promise<Locator> {
  await page.locator(".mini-info").click();
  const now = page.getByRole("dialog", { name: "正在播放" });
  await now.getByRole("button", { name: "队列" }).click();
  return now.locator(".queue-list");
}
// Only this test's tracks: the never-stop refill may append others after them.
const mine = ["月亮代表我的心", "Faded", "甜蜜蜜"];
const titles = async (list: Locator) => (await list.locator(".queue-row .queue-title").allTextContents()).filter((t) => mine.includes(t));

test("queue sheet: drag to reorder, swipe to remove, add to queue", async ({ page }, info) => {
  await login(page);
  const [a, b, c] = mine;
  const ids: number[] = [];
  for (const title of [a, b, c]) {
    await expect.poll(() => trackIdByTitle(page, title), { timeout: 15_000 }).toBeDefined(); // first scan may still be running
    ids.push((await trackIdByTitle(page, title))!);
  }
  // A paused queue to resume (on open is pinned to resume for e2e), set with
  // the app closed: its own save on leaving would overwrite it.
  await page.goto("about:blank");
  expect((await page.request.put("/api/v1/queue", { data: { track_ids: ids, current_index: 0, position_ms: 0 } })).status()).toBe(200);
  await page.goto("/");
  await expect(page.locator(".mini")).toContainText(a);

  let list = await openQueue(page);
  await expect.poll(() => titles(list)).toEqual([a, b, c]);
  await expect(list.locator(".queue-row").first().locator(".queue-main")).toHaveAttribute("aria-current", "true");

  // Drag c's handle up one row.
  const handle = list.getByRole("button", { name: `移动 ${c}` });
  const box = (await handle.boundingBox())!;
  const rowH = (await list.locator(".queue-row").first().boundingBox())!.height;
  const x = box.x + box.width / 2;
  await page.mouse.move(x, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(x, box.y + box.height / 2 - rowH / 2, { steps: 4 });
  await page.mouse.move(x, box.y + box.height / 2 - rowH, { steps: 4 });
  await page.mouse.up();
  await expect.poll(() => titles(list)).toEqual([a, c, b]);
  await expect(page.locator(".mini")).toContainText(a); // the current track stays

  // Swipe b's row left past 35 % of its width.
  const row = (await list.locator(".queue-row").nth(2).boundingBox())!;
  const y = row.y + row.height / 2;
  await page.mouse.move(row.x + row.width * 0.6, y);
  await page.mouse.down();
  await page.mouse.move(row.x + row.width * 0.1, y, { steps: 6 });
  await page.mouse.up();
  await expect.poll(() => titles(list)).toEqual([a, c]);
  await expect(page.locator(".mini")).toContainText(a); // nothing was played by the swipe

  // ✕ only where there is hover (a desktop), never on the current row.
  const hover = await page.evaluate(() => matchMedia("(hover: hover)").matches);
  await expect(list.getByRole("button", { name: `移除 ${c}` })).toBeVisible({ visible: hover });
  await expect(list.getByRole("button", { name: `移除 ${a}` })).toHaveCount(0);
  if (hover) {
    await list.getByRole("button", { name: `移除 ${c}` }).click();
    await expect.poll(() => titles(list)).toEqual([a]);
  }
  await page.screenshot({ path: info.outputPath("queue.png") });
  await page.getByRole("button", { name: "收起" }).click();

  // ⋯ → 添加到队列 from search: after the current track (nothing else was queued).
  await page.getByRole("link", { name: "搜索" }).click();
  await page.getByRole("searchbox").fill("tmm");
  await page.getByRole("button", { name: "更多：甜蜜蜜" }).click();
  await page.getByRole("menuitem", { name: "添加到队列" }).click();
  list = await openQueue(page);
  await expect.poll(() => titles(list)).toEqual([a, c]); // a copy already queued moves there
});
