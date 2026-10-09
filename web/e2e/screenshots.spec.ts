import { devices, expect, test, type Browser, type BrowserContext, type Page } from "@playwright/test";
import { writeFileSync } from "node:fs";
import path from "node:path";
import { login, loginWith } from "./playback";

// Captures the README screenshots into docs/screenshots/. Skipped in normal
// runs; generate locally with:
//   cd web && npm run build && LARK_SCREENSHOTS=1 npx playwright test screenshots --project=iphone-webkit
// LARK_SCREENSHOTS=1 makes the e2e server start with the made-up demo library
// and channels of e2e/demo/demo.py: invented names, generated tones, generated
// gradient covers. Nothing here touches the network.

const FEEDS = "http://127.0.0.1:4701"; // demo.py serve: channel feeds and thumbnails

const FAVORITES = ["清晨的正弦波", "Morning in A", "回家路上", "Middle C Blues", "Cruise Control", "Etude for Middle C",
  "雨天的和弦", "Neon Test Card", "Feedback Loop", "Low Pass Lullaby", "小镇的黄昏", "Harmonic Afternoon"];
const CHANNELS = ["weekend-kitchen", "little-observatory", "bedtime-stories", "piano-practice"];

test("README screenshots", async ({ browser }, info) => {
  test.skip(process.env.LARK_SCREENSHOTS !== "1" || info.project.name !== "iphone-webkit", "set LARK_SCREENSHOTS=1, iphone-webkit only");
  test.setTimeout(300_000);
  const dir = path.resolve(info.project.testDir, "../../docs/screenshots");
  // Small JPEGs, with the metadata segments the browser adds (Exif, Photoshop) dropped.
  const shot = async (page: Page, name: string) =>
    writeFileSync(path.join(dir, `${name}.jpg`), stripJpegMetadata(await page.screenshot({ type: "jpeg", quality: 72 })));

  // An iPhone-sized window like the app's (390×844 points), at 1.5× to keep the files small.
  const phone = await newContext(browser, { ...devices["iPhone 13"], viewport: { width: 390, height: 844 }, screen: { width: 390, height: 844 }, deviceScaleFactor: 1.5 });
  const page = await phone.newPage();
  await login(page);
  try {
    await setUp(page);

    // 首页 (Chinese): the Melarka title and the shuffle buttons.
    await setLanguage(page, "zh-Hans");
    await nowPlayingWithLyrics(page, "清晨的正弦波");
    await page.getByRole("dialog").getByRole("button", { name: "收起" }).click();
    await page.getByRole("link", { name: "首页" }).click();
    await expect(page.getByRole("heading", { name: "Melarka" })).toBeVisible();
    await coversLoaded(page);
    await shot(page, "phone-home");

    // Now Playing with synced lyrics (Chinese).
    await page.locator(".mini-info").click();
    await seekToLine(page, 4);
    await shot(page, "phone-now-playing");
    await page.getByRole("dialog").getByRole("button", { name: "收起" }).click();

    // 频道 (Chinese).
    await page.getByRole("link", { name: "频道", exact: true }).click();
    await expect(page.locator(".episode-row").first()).toBeVisible({ timeout: 15_000 });
    await coversLoaded(page);
    await shot(page, "phone-channels");

    // English: favorites with shuffle, tags, search with YouTube results.
    await setLanguage(page, "en");
    await page.getByRole("link", { name: "Library" }).click();
    await page.getByRole("tab", { name: "Favorites" }).click();
    await expect(page.getByRole("button", { name: /Shuffle favorites/ })).toBeVisible();
    await expect(page.locator(".track").nth(5)).toBeVisible({ timeout: 15_000 });
    await coversLoaded(page);
    await shot(page, "phone-favorites");

    await page.getByRole("tab", { name: "Tags" }).click();
    await expect(page.getByText("Classics").first()).toBeVisible({ timeout: 15_000 });
    await shot(page, "phone-tags");

    await page.getByRole("link", { name: "Search" }).click();
    const ytResult = page.locator(".yt-results li").first();
    const ytButton = page.getByRole("button", { name: /^Search YouTube for/ });
    // Retried: the search page can still be restoring its last state when the box is first filled.
    await expect(async () => {
      await page.getByRole("searchbox").fill("清晨的正弦波");
      await expect(ytResult.or(ytButton)).toBeVisible({ timeout: 5_000 });
    }).toPass({ timeout: 30_000 });
    if (await ytButton.isVisible()) await ytButton.click();
    await expect(ytResult).toBeVisible({ timeout: 30_000 });
    await page.getByRole("searchbox").blur();
    await coversLoaded(page);
    await shot(page, "phone-search");

    // A desktop browser window (English).
    const desk = await newContext(browser, { viewport: { width: 1280, height: 800 }, deviceScaleFactor: 1 });
    try {
      const d = await desk.newPage();
      await loginWith(d, "admin", process.env.LARK_E2E_PW!); // already English
      await d.getByRole("link", { name: "Library" }).click();
      await d.getByRole("tab", { name: "Albums" }).click();
      await coversLoaded(d);
      await shot(d, "desktop-albums");
    } finally {
      await desk.close();
    }
  } finally {
    await setLanguage(page, null).catch(() => {}); // back to the server default
    await phone.close();
  }
});

/** A context that never leaves the machine: YouTube thumbnails come from demo.py, anything else external is refused. */
async function newContext(browser: Browser, opts: Parameters<Browser["newContext"]>[0]): Promise<BrowserContext> {
  // serviceWorkers: "block", or the app's service worker fetches images past these routes.
  const ctx = await browser.newContext({ ...opts, baseURL: "http://127.0.0.1:4700", serviceWorkers: "block" });
  await ctx.route(/^https?:\/\/(?!127\.0\.0\.1[:/])/, (r) => r.abort());
  await ctx.route(/^https:\/\/i\.ytimg\.com\/vi\/[^/]+\//, async (r) => {
    const id = /\/vi\/([^/]+)\//.exec(r.request().url())![1];
    const res = await fetch(`${FEEDS}/vi/${id}/mqdefault.jpg`);
    if (!res.ok) return r.abort();
    await r.fulfill({ status: 200, contentType: "image/jpeg", body: Buffer.from(await res.arrayBuffer()) });
  });
  return ctx;
}

/** Calls the Melarka API as the signed-in page. */
async function call(page: Page, method: string, url: string, body?: unknown): Promise<unknown> {
  return page.evaluate(async ([method, url, body]) => {
    const r = await fetch(`/api/v1${url}`, {
      method, credentials: "same-origin",
      headers: body === undefined ? {} : { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!r.ok) throw new Error(`${method} ${url}: ${r.status} ${await r.text()}`);
    return r.status === 204 ? null : r.json().catch(() => null);
  }, [method, url, body] as const);
}

async function setUp(page: Page) {
  // The demo library is scanned at start: wait for it, then favorite some songs.
  let items: { id: number; title: string }[] = [];
  await expect.poll(async () => {
    items = ((await call(page, "GET", "/tracks?limit=100")) as { items: { id: number; title: string }[] }).items;
    return items.length;
  }, { timeout: 30_000 }).toBeGreaterThanOrEqual(29) // every demo song;
  for (const title of [...FAVORITES].reverse()) { // newest favorite first in the list
    const t = items.find((x) => x.title === title);
    if (!t) throw new Error(`demo library has no ${title}`);
    await call(page, "PUT", `/favorites/${t.id}`);
  }
  // Follow the demo channels; each follow fetches its newest episodes.
  // Retried: a follow right after start can meet the startup scan's write lock.
  for (const handle of CHANNELS) {
    await expect(async () => {
      await call(page, "POST", "/channels/follow", { url: `https://www.youtube.com/@${handle}` });
    }).toPass({ timeout: 30_000 });
  }
  await expect.poll(async () => {
    const { items } = (await call(page, "GET", "/episodes/latest?limit=50")) as { items: unknown[] };
    return items.length;
  }, { timeout: 60_000 }).toBeGreaterThanOrEqual(6);
}

async function setLanguage(page: Page, language: string | null) {
  await call(page, "PUT", "/me/preferences", { language, on_open: "resume" });
  await page.reload();
  await expect(page.locator("nav.tabs a").first()).toBeVisible();
}

/** Lets the visible covers finish loading and fading in. */
async function coversLoaded(page: Page) {
  // Not "networkidle": the playing song keeps streaming.
  await page.waitForFunction(() => [...document.querySelectorAll("img")].every((i) => i.complete), null, { timeout: 10_000 }).catch(() => {});
  await page.waitForTimeout(800);
}

async function nowPlayingWithLyrics(page: Page, title: string) {
  await page.getByRole("link", { name: "搜索" }).click();
  const row = page.getByRole("button", { name: new RegExp(`^${title} `) }).first();
  await expect(async () => {
    await page.getByRole("searchbox").fill(title);
    await expect(row).toBeVisible({ timeout: 5_000 });
  }).toPass({ timeout: 30_000 });
  await row.click();
  await page.locator(".mini-info").click();
  await seekToLine(page, 4);
}

/** Shows the lyrics in Now Playing and taps line n, so it is the highlighted one. */
async function seekToLine(page: Page, n: number) {
  const now = page.getByRole("dialog");
  await expect(now.locator(".lyrics, .now-art").first()).toBeVisible({ timeout: 10_000 });
  const lyricsButton = now.getByRole("button", { name: /^(歌词|Lyrics)$/ });
  if (await lyricsButton.isVisible()) await lyricsButton.click();
  const lines = now.locator(".lyrics-synced li button");
  await expect(lines.first()).toBeVisible({ timeout: 10_000 });
  await lines.nth(n).click();
  await expect(now.locator('.lyrics-synced [aria-current="true"]')).toHaveCount(1, { timeout: 10_000 });
  await page.waitForTimeout(800); // the lyrics scroll to the line
}

/** Drops a JPEG's APP1–APP15 and comment segments (Exif, XMP, Photoshop…); JFIF (APP0) and the image stay. */
function stripJpegMetadata(jpg: Buffer): Buffer {
  if (jpg[0] !== 0xff || jpg[1] !== 0xd8) throw new Error("not a JPEG");
  const keep: Buffer[] = [jpg.subarray(0, 2)];
  let i = 2;
  while (i + 4 <= jpg.length && jpg[i] === 0xff) {
    const marker = jpg[i + 1];
    if (marker === 0xda) break; // start of scan: the image data follows
    const end = i + 2 + jpg.readUInt16BE(i + 2);
    if (!((marker >= 0xe1 && marker <= 0xef) || marker === 0xfe)) keep.push(jpg.subarray(i, end));
    i = end;
  }
  keep.push(jpg.subarray(i));
  return Buffer.concat(keep);
}
