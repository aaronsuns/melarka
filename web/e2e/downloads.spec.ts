import { expect, test } from "@playwright/test";
import { login, trackIdByJobTitle, verifyPlaybackStarted } from "./playback";

test("YouTube search → download → play, admin console, shuffle", async ({ page }, info) => {
  await login(page);

  // Search for something nowhere in the local library: the YouTube panel
  // must appear automatically (no "search on YouTube" button to tap first).
  await page.getByRole("link", { name: "搜索" }).click();
  await page.getByRole("searchbox").fill("zzzz-nothing");
  const ytRow = page.locator(".yt-results li", { hasText: "测试歌曲" });
  await expect(ytRow).toBeVisible({ timeout: 15_000 });

  // Tap 下载. The two projects share one server (workers: 1): whichever
  // project runs second finds the video already downloaded.
  await ytRow.getByRole("button", { name: "下载" }).click();
  // The row shows the live status and becomes ▶ 播放 when the download
  // finishes (or at once if the song is already in the library, as for the
  // second project); tapping it plays that very track — verifyPlaybackStarted
  // is scoped to the track's own stream URL.
  const rowPlay = ytRow.getByRole("button", { name: /播放/ });
  await expect(rowPlay).toBeVisible({ timeout: 45_000 });
  const rowTrackId = await trackIdByJobTitle(page, "测试歌曲");
  expect(rowTrackId).toBeTruthy();
  await verifyPlaybackStarted(page, info, () => rowPlay.click(), { trackId: rowTrackId });
  await expect(page.locator(".mini")).toContainText("测试歌曲");

  // 我的 → 下载任务: wait for the job to finish (or find it already done).
  await page.getByRole("link", { name: "我的" }).click();
  await page.getByRole("link", { name: "下载任务" }).click();
  const jobRow = page.locator("li.row", { hasText: "测试歌曲" });
  await expect(jobRow).toBeVisible({ timeout: 15_000 });
  await expect(jobRow.getByText("已完成")).toBeVisible({ timeout: 30_000 });

  // 播放: the mini player must show the *cleaned* title/artist (proving the
  // overrides the download worker sets), not the raw YouTube title — and
  // verifyPlaybackStarted proves real playback (progress bar advancing),
  // not just that play() was called. If this track is already the
  // server-restored "current" one (e.g. the other project played it
  // first), the browser resumes it from cache without firing a new
  // /stream request at all; its fallback is scoped to this track's id and
  // registered before the click, so it can't be satisfied — or blocked —
  // by an unrelated background request.
  const trackId = await trackIdByJobTitle(page, "测试歌曲");
  await verifyPlaybackStarted(page, info, () => jobRow.getByRole("button", { name: "播放" }).click(), { trackId });
  await expect(page.locator(".mini")).toContainText("测试歌曲");
  await expect(page.locator(".mini")).toContainText("假歌手");
  await expect(page.locator(".mini").getByRole("button", { name: "暂停" })).toBeVisible();

  // The finished download was auto-added to the requester's favorites.
  await page.getByRole("link", { name: "音乐库" }).click();
  await page.getByRole("tab", { name: "收藏" }).click();
  await expect(page.locator(".track-main", { hasText: "测试歌曲" })).toBeVisible({ timeout: 10_000 });

  // 我的 → 管理 → 用户: the admin console lists the admin account.
  await page.getByRole("link", { name: "我的" }).click();
  await page.getByRole("link", { name: "管理" }).click();
  await page.getByRole("tab", { name: "用户" }).click();
  await expect(page.locator(".user-row", { hasText: "admin" })).toBeVisible();

  // Home 随机播放: shuffles the whole library and starts playback. The
  // chosen track is random, so there's no id to scope a fallback to ahead
  // of the click (verifyPlaybackStarted falls back to the generic — but
  // still bounded — predicate in that case); also assert the absence of
  // the button's own error message, a real check that shuffle worked, not
  // a trivially-true one (".mini" is already visible from the 播放 step).
  await page.getByRole("link", { name: "首页" }).click();
  await verifyPlaybackStarted(page, info, () => page.getByRole("button", { name: /^🔀 随机播放(?!收藏)/ }).click());
  await expect(page.locator(".mini").getByRole("button", { name: "暂停" })).toBeVisible();
  await expect(page.locator(".error.small", { hasText: "随机播放" })).toHaveCount(0);
});
