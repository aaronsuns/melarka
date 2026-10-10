import { expect, test, type Page } from "./fixtures";
import { login, trackIdByTitle, verifyPlaybackStarted } from "./playback";

// Shuffle and repeat in Now Playing, in a real engine: the buttons cycle,
// the choice survives a reload (it is kept per device), and repeat one plays
// the same track again at its natural end, as a new play each time.

// A paused queue of these tracks to resume (on open is pinned to resume for
// e2e), set with the app closed: its own save on leaving would overwrite it.
async function queueOf(page: Page, titles: string[]): Promise<number[]> {
  const ids: number[] = [];
  for (const title of titles) {
    await expect.poll(() => trackIdByTitle(page, title), { timeout: 15_000 }).toBeDefined(); // first scan may still be running
    ids.push((await trackIdByTitle(page, title))!);
  }
  await page.goto("about:blank");
  expect((await page.request.put("/api/v1/queue", { data: { track_ids: ids, current_index: 0, position_ms: 0 } })).status()).toBe(200);
  await page.goto("/");
  await expect(page.locator(".mini")).toContainText(titles[0]);
  return ids;
}

async function openNow(page: Page) {
  await page.locator(".mini-info").click();
  const now = page.getByRole("dialog", { name: "正在播放" });
  await expect(now).toBeVisible();
  return now;
}

test("shuffle and repeat buttons cycle and persist across a reload", async ({ page }) => {
  await login(page);
  await queueOf(page, ["甜蜜蜜", "月亮代表我的心", "Faded"]);

  let now = await openNow(page);
  const shuffle = now.getByRole("button", { name: "随机播放", exact: true });
  const repeat = now.locator("button[data-repeat]");
  await expect(shuffle).toHaveAttribute("aria-pressed", "false");
  await expect(repeat).toHaveAccessibleName("循环：关");
  await expect(repeat).toHaveAttribute("aria-pressed", "false");

  // Repeat: off → all → one → off.
  await repeat.click();
  await expect(repeat).toHaveAccessibleName("列表循环");
  await expect(repeat).toHaveAttribute("aria-pressed", "true");
  await repeat.click();
  await expect(repeat).toHaveAccessibleName("单曲循环");
  await expect(repeat).toHaveAttribute("data-repeat", "one");
  await repeat.click();
  await expect(repeat).toHaveAccessibleName("循环：关");
  await expect(repeat).toHaveAttribute("aria-pressed", "false");

  // Shuffle toggles; the current track stays where it is.
  await shuffle.click();
  await expect(shuffle).toHaveAttribute("aria-pressed", "true");
  await shuffle.click();
  await expect(shuffle).toHaveAttribute("aria-pressed", "false");

  // Shuffle on and repeat all, then a reload: both come back.
  await shuffle.click();
  await repeat.click();
  await expect(shuffle).toHaveAttribute("aria-pressed", "true");
  await expect(repeat).toHaveAccessibleName("列表循环");
  await page.reload();
  await expect(page.locator(".mini")).toContainText("甜蜜蜜");
  now = await openNow(page);
  await expect(now.getByRole("button", { name: "随机播放", exact: true })).toHaveAttribute("aria-pressed", "true");
  await expect(now.locator("button[data-repeat]")).toHaveAccessibleName("列表循环");
});

test("repeat one plays the same track again at its end, as a new play", async ({ page }, info) => {
  test.setTimeout(90_000);
  await login(page);
  const title = "Faded"; // 30 s
  const [id] = await queueOf(page, [title, "月亮代表我的心"]);

  // Every play event the page reports for this track, once each (a retried
  // batch carries the same client_event_id).
  const plays = new Map<string, { played_seconds: number; skipped: boolean }>();
  page.on("request", (r) => {
    if (r.method() !== "POST" || !r.url().includes("/api/v1/events/play")) return;
    const body = r.postDataJSON() as { events?: { client_event_id: string; track_id: number; played_seconds: number; skipped: boolean }[] };
    for (const e of body.events ?? []) if (e.track_id === id) plays.set(e.client_event_id, e);
  });

  const now = await openNow(page);
  const repeat = now.locator("button[data-repeat]");
  await repeat.click();
  await repeat.click();
  await expect(repeat).toHaveAccessibleName("单曲循环");

  await verifyPlaybackStarted(page, info, () => now.getByRole("button", { name: "播放", exact: true }).click(), { trackId: id });
  const slider = now.getByRole("slider", { name: "进度" });

  for (let loop = 1; loop <= 2; loop++) {
    // At least a second of listening, so the play counts; then near the end.
    await expect.poll(async () => Number(await slider.inputValue()), { timeout: 15_000 }).toBeGreaterThanOrEqual(2);
    await slider.fill("27");
    await expect.poll(async () => Number(await slider.inputValue()), { timeout: 10_000 }).toBeGreaterThanOrEqual(26);
    // The end: back to the start of the same track, still playing.
    await expect.poll(async () => Number(await slider.inputValue()), { timeout: 15_000 }).toBeLessThan(5);
    await expect(now.getByRole("heading", { name: title })).toBeVisible();
    await expect(page.locator(".mini")).toContainText(title);
    await expect(now.getByRole("button", { name: "暂停" })).toBeVisible();
    await expect.poll(() => plays.size, { timeout: 10_000 }).toBe(loop);
  }
  expect([...plays.values()].every((e) => !e.skipped && e.played_seconds >= 1)).toBe(true);
});
