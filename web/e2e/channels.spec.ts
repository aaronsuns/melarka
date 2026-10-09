import { expect, test } from "./fixtures";
import { login } from "./playback";

test("频道: paste a channel link, follow, the newest episodes download, ▶ 听 plays one, 保留 keeps it", async ({ page }) => {
  await login(page);
  // exact: the channel card's link ("假频道 …") also contains 频道.
  const tab = page.getByRole("link", { name: "频道", exact: true });
  await tab.click();
  await page.getByRole("link", { name: "添加频道" }).click();
  await page.getByLabel("搜索或粘贴频道链接").fill("https://www.youtube.com/@fakechannel");
  await page.getByRole("button", { name: "查找" }).click();
  const card = page.locator(".channel-card", { hasText: "假频道" });
  await expect(card).toBeVisible({ timeout: 15_000 });
  // Both projects share one server: the second finds the channel already followed.
  // exact: "关注" is also a substring of "已关注", and tapping that would unfollow.
  const follow = card.getByRole("button", { name: "关注", exact: true });
  if (await follow.isVisible()) await follow.click();
  await expect(card.getByRole("button", { name: "已关注", exact: true })).toBeVisible();

  // The follow polls the feed at once; the newest three download, never the Short.
  await tab.click();
  const row = page.locator(".episode-row", { hasText: "假节目 第3集" });
  await expect(async () => {
    await page.reload();
    await expect(row.getByRole("button", { name: "听" })).toBeVisible({ timeout: 2_000 });
  }).toPass({ timeout: 60_000 });
  for (const title of ["假节目 第3集", "假节目 第2集", "假节目 第1集"]) {
    await expect(page.locator(".episode-row", { hasText: title })).toBeVisible();
  }
  await expect(page.locator(".episode-row", { hasText: "假短片" })).toHaveCount(0);

  // ▶ 听: the episode streams and takes over the mini player.
  const stream = page.waitForResponse((r) => r.url().includes("/api/v1/episodes/fakeep00003/stream") && r.status() < 300, { timeout: 20_000 });
  await row.getByRole("button", { name: "听" }).click();
  await stream;
  await expect(page.locator(".episode-mini")).toContainText("假节目 第3集");
  await expect.poll(async () => {
    const style = (await page.locator(".episode-mini .mini-progress").getAttribute("style")) ?? "";
    return parseFloat(/width:\s*([\d.]+)%/.exec(style)?.[1] ?? "0");
  }, { timeout: 20_000 }).toBeGreaterThan(0);

  // The episode page: 保留 ⇄ 已保留, and it shows under 保留.
  await row.getByRole("link").first().click();
  const keep = page.getByRole("button", { name: /^(保留|已保留)$/ });
  if ((await keep.textContent())?.trim() === "保留") await keep.click();
  await expect(page.getByRole("button", { name: "已保留" })).toBeVisible();
  // Close the episode player while it is still mounted: the goto below reloads the app.
  await page.locator(".episode-mini").getByRole("button", { name: "关闭节目播放器" }).click();
  await expect(page.locator(".episode-mini")).toHaveCount(0);
  await page.goto("/channels?tab=kept");
  await expect(page.locator(".episode-row", { hasText: "假节目 第3集" })).toBeVisible();
});

test("频道 queue: 按频道 ▶ 全部播放 plays the channel newest first, the end of one plays the next, ⏭ skips", async ({ page }) => {
  await login(page);
  // Self-contained: follow 假频道 (already followed is fine), wait for its three
  // episodes, and start them unplayed (an earlier run may have played them).
  const ids = ["fakeep00003", "fakeep00002", "fakeep00001"];
  await page.evaluate(() => fetch("/api/v1/channels/follow", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ url: "https://www.youtube.com/@fakechannel" }) }));
  await expect.poll(async () => page.evaluate(async (want) => {
    const r = await (await fetch("/api/v1/episodes/latest", { credentials: "same-origin" })).json();
    return (r.items as { video_id: string; audio: { status: string } | null }[]).filter((e) => want.includes(e.video_id) && e.audio?.status === "done").length;
  }, ids), { timeout: 60_000 }).toBe(3);
  await page.evaluate((list) => Promise.all(list.map((id) => fetch(`/api/v1/episodes/${id}/progress`, {
    method: "PUT", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ position_s: 0, played: false }),
  }))), ids);

  await page.goto("/channels?tab=channels");
  const section = page.locator("section.channel-group", { has: page.getByRole("heading", { name: "假频道" }) });
  await expect(section).toBeVisible({ timeout: 15_000 });
  const first = page.waitForResponse((r) => r.url().includes("/api/v1/episodes/fakeep00003/stream") && r.status() < 300, { timeout: 20_000 });
  await section.getByRole("button", { name: "全部播放" }).click();
  await first;
  const mini = page.locator(".episode-mini");
  await expect(mini).toContainText("假节目 第3集");

  // The 5-second episode ends: the next one in the queue plays by itself.
  await expect(mini).toContainText("假节目 第2集", { timeout: 20_000 });
  await expect.poll(async () => page.evaluate(async () => {
    const r = await (await fetch("/api/v1/episodes/fakeep00003", { credentials: "same-origin" })).json();
    return r.played;
  }), { timeout: 10_000 }).toBe(true);

  // Now Playing: ⏭ goes to the last one; the queue sheet marks it.
  await mini.getByRole("button", { name: /正在播放的节目/ }).click();
  const now = page.getByRole("dialog", { name: "正在播放的节目" });
  await expect(now.getByRole("heading", { name: "假节目 第2集" })).toBeVisible();
  const third = page.waitForResponse((r) => r.url().includes("/api/v1/episodes/fakeep00001/stream") && r.status() < 300, { timeout: 20_000 });
  await now.getByRole("button", { name: "下一集" }).click();
  await third;
  await expect(now.getByRole("heading", { name: "假节目 第1集" })).toBeVisible();
  await now.getByRole("button", { name: "节目队列" }).click();
  await expect(now.locator('[aria-current="true"]')).toContainText("假节目 第1集");
  await now.getByRole("button", { name: "收起" }).click();
  await mini.getByRole("button", { name: "关闭节目播放器" }).click();
  await expect(mini).toHaveCount(0);
});
