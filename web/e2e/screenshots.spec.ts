import { expect, test, type Page } from "@playwright/test";
import path from "node:path";
import { login, loginWith } from "./playback";

// Captures the README screenshots into docs/screenshots/. Skipped in normal
// runs; generate locally with:
//   cd web && npm run build && LARK_SCREENSHOTS=1 npx playwright test screenshots --project=iphone-webkit
// or against a deployed Lark (LARK_SHOT_URL/USER/PW, a member whose language is
// already "en") with playwright.screenshots.config.ts.
const prod = !!process.env.LARK_SHOT_URL;

test("README screenshots", async ({ page }, info) => {
  test.skip(process.env.LARK_SCREENSHOTS !== "1" || info.project.name !== "iphone-webkit", "set LARK_SCREENSHOTS=1, iphone-webkit only");
  const dir = path.resolve(info.project.testDir, "../../docs/screenshots");
  const shot = (name: string) => page.screenshot({ path: path.join(dir, `${name}.png`) });

  if (prod) {
    await loginWith(page, process.env.LARK_SHOT_USER!, process.env.LARK_SHOT_PW!);
  } else {
    await login(page);
    await page.getByRole("link", { name: "我的" }).click();
    await page.getByLabel("语言").selectOption("en");
  }
  await expect(page.getByRole("link", { name: "Library" })).toBeVisible();
  try {
    await page.getByRole("link", { name: "Home" }).click();
    await expect(page.getByRole("heading", { name: "Recently added" })).toBeVisible({ timeout: 15_000 });
    await expect(page.locator("img.cover-img.loaded").first()).toBeVisible({ timeout: 20_000 });
    await page.waitForTimeout(1_500); // the other covers finish their fade-in
    await shot("home");

    await page.getByRole("link", { name: "Library" }).click();
    await page.getByRole("tab", { name: "Tags" }).click();
    await expect(page.getByText("Classics").first()).toBeVisible({ timeout: 15_000 });
    await shot("library-tags");

    await nowPlayingWithLyrics(page);
    await shot("now-playing-lyrics");
    await page.getByRole("dialog", { name: "Now playing" }).getByRole("button", { name: "Close" }).click();

    await page.getByRole("link", { name: "Search" }).click();
    await page.getByRole("searchbox").fill("Teresa Teng");
    // With local matches (a real library) YouTube is searched only on request.
    const ytResult = page.locator(".yt-results li").first();
    const ytButton = page.getByRole("button", { name: /^Search YouTube for/ });
    await expect(ytResult.or(ytButton)).toBeVisible({ timeout: 15_000 });
    if (await ytButton.isVisible()) await ytButton.click();
    await expect(ytResult).toBeVisible({ timeout: 30_000 });
    // Long local results push YouTube down: bring its section to the top.
    await page.getByRole("heading", { name: "YouTube", exact: true }).evaluate((el) => el.scrollIntoView({ block: "start" }));
    await page.waitForTimeout(1_000); // thumbnails
    await shot("search-youtube");
  } finally {
    if (!prod) {
      await page.getByRole("link", { name: "Me", exact: true }).click();
      await page.getByLabel("Language").selectOption(""); // back to the server default
      await expect(page.getByRole("link", { name: "音乐库" })).toBeVisible();
    }
  }
});

async function nowPlayingWithLyrics(page: Page) {
  await page.getByRole("link", { name: "Search" }).click();
  await page.getByRole("searchbox").fill("甜蜜蜜");
  // A real library has many 甜蜜蜜 albums and copies: pick the song itself, by 邓丽君.
  const row = page.getByRole("button", { name: /^甜蜜蜜 邓丽君 · / }).first();
  await expect(row).toBeVisible({ timeout: 15_000 });
  await row.click();
  await page.locator(".mini-info").click();
  const now = page.getByRole("dialog", { name: "Now playing" });
  // Now Playing opens on the lyrics when the song has some; on the cover only
  // if this browser last chose the cover.
  const cover = now.getByRole("button", { name: "Lyrics", exact: true });
  await expect(now.locator(".lyrics, .now-art").first()).toBeVisible({ timeout: 10_000 });
  if (await cover.isVisible()) await cover.click();
  // Production songs may only have plain (unsynced) lyrics, and synced ones may
  // open with a long intro: tap a line to seek there so one is highlighted.
  await expect(now.locator(".lyrics-synced li, .lyrics-plain").first()).toBeVisible({ timeout: 10_000 });
  if ((await now.locator(".lyrics-synced").count()) > 0) {
    const lines = now.locator(".lyrics-synced li button");
    await lines.nth(Math.min(8, (await lines.count()) - 1)).click(); // past any credit lines
    await expect(now.locator('.lyrics-synced [aria-current="true"]')).toHaveCount(1, { timeout: 10_000 });
  }
}
