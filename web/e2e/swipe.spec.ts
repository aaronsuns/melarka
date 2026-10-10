import { expect, test, type Locator, type Page } from "./fixtures";
import { login, trackIdByTitle } from "./playback";

// Swipe to change track, with real pointer events in a real engine (WebKit
// as on an iPhone, and Chromium): left = next, right = previous, on the mini
// player and on Now Playing's title; a vertical drag or a short one changes
// nothing, and the drag never also counts as a tap (opening Now Playing).
const mine = ["月亮代表我的心", "Faded", "甜蜜蜜"];

async function drag(page: Page, target: Locator, dx: number, dy = 0) {
  const box = (await target.boundingBox())!;
  const x = box.x + box.width / 2;
  const y = box.y + box.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x + dx / 2, y + dy / 2, { steps: 4 });
  await page.mouse.move(x + dx, y + dy, { steps: 4 });
  await page.mouse.up();
}

test("swipe on the mini player and on Now Playing changes track: left = next, right = previous", async ({ page }) => {
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
  const mini = page.locator(".mini");
  const title = mini.locator(".mini-info .track-text > :first-child");
  await expect(title).toHaveText(mine[0]);

  const info = mini.locator(".mini-info");
  await expect(info).toHaveCSS("touch-action", "pan-y");
  await drag(page, info, -140);
  await expect(title).toHaveText(mine[1]);
  const now = page.getByRole("dialog", { name: "正在播放" });
  await expect(now).toHaveCount(0); // the drag was not a tap
  await drag(page, info, 140);
  await expect(title).toHaveText(mine[0]);

  // Too short, or more vertical than sideways: nothing changes.
  await drag(page, info, -40);
  await drag(page, info, -70, 60);
  await expect(title).toHaveText(mine[0]);
  // On a transport button it is no swipe (and no track change).
  await drag(page, mini.getByRole("button", { name: "下一首" }), 140);
  await expect(title).toHaveText(mine[0]);

  // Now Playing: the title area swipes too.
  await info.click();
  await expect(now).toBeVisible();
  const meta = now.locator(".now-meta");
  await expect(meta).toHaveCSS("touch-action", "pan-y");
  await drag(page, meta, -140);
  await expect(meta.locator("h2")).toHaveText(mine[1]);
  await drag(page, meta, 140);
  await expect(meta.locator("h2")).toHaveText(mine[0]);
  // Not on the progress slider.
  await drag(page, now.getByRole("slider", { name: "进度" }), -140);
  await expect(meta.locator("h2")).toHaveText(mine[0]);
  await now.getByRole("button", { name: "收起" }).click();
  await expect(now).toHaveCount(0);
});
