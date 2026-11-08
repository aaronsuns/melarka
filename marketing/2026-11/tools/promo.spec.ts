import { devices, expect, test, type Browser, type BrowserContext, type Page } from "./fixtures";
import path from "node:path";
import { login } from "./playback";

// Promo captures for marketing/ (see marketing/README.md). Copy to web/e2e/, then from web/:
//   npm run build && LARK_SCREENSHOTS=1 LARK_PROMO_DIR=/dir npx playwright test promo --project=iphone-webkit
const FEEDS = "http://127.0.0.1:4701";
const FAVORITES = ["清晨的正弦波", "Morning in A", "回家路上", "Middle C Blues", "Cruise Control", "Etude for Middle C",
  "雨天的和弦", "Neon Test Card", "Feedback Loop", "Low Pass Lullaby", "小镇的黄昏", "Harmonic Afternoon"];
const CHANNELS = ["weekend-kitchen", "little-observatory", "bedtime-stories", "piano-practice"];

test("promo captures", async ({ browser }, info) => {
  const dir = process.env.LARK_PROMO_DIR;
  test.skip(process.env.LARK_SCREENSHOTS !== "1" || !dir || info.project.name !== "iphone-webkit", "promo only");
  test.setTimeout(600_000);
  const phone = await newContext(browser, { ...devices["iPhone 13"], viewport: { width: 390, height: 844 }, screen: { width: 390, height: 844 }, deviceScaleFactor: 3 });
  const page = await phone.newPage();
  const shot = async (name: string, full = false) => page.screenshot({ path: path.join(dir!, `${name}.png`), fullPage: full });
  await login(page);
  await setUp(page);
  for (const name of ["爸爸", "妈妈", "爷爷", "奶奶"]) {
    await call(page, "POST", "/users", { username: name, password: "demo-password-123", role: "member" }).catch(() => {});
  }
  const L = {
    "zh-Hans": { home: "首页", lib: "音乐库", fav: "收藏", search: "搜索", ch: "频道", me: "我的", close: "收起", cover: "封面", queue: "队列", tags: "标签" },
    en: { home: "Home", lib: "Library", fav: "Favorites", search: "Search", ch: "Channels", me: "Me", close: "Close", cover: "Cover", queue: "Queue", tags: "Tags" },
  } as const;
  try {
    for (const lang of (process.env.LANGS ?? "zh-Hans,en").split(",") as ("zh-Hans" | "en")[]) {
      const t = L[lang];
      const p = lang === "en" ? "en" : "zh";
      await page.goto("/");
      await setLanguage(page, lang);
      await nowPlayingWithLyrics(page, "清晨的正弦波");
      for (const n of [1, 2, 3, 4, 5, 6]) {
        await seekToLine(page, n);
        await shot(`${p}_lyrics_${n}`);
      }
      const now = page.getByRole("dialog");
      const coverBtn = now.getByRole("button", { name: t.cover });
      if (await coverBtn.isVisible()) { await coverBtn.click(); await coversLoaded(page); await shot(`${p}_cover`); }
      const qb = now.getByRole("button", { name: t.queue });
      if (await qb.isVisible()) { await qb.click(); await page.waitForTimeout(700); await shot(`${p}_queue`); await qb.click(); }
      await now.getByRole("button", { name: t.close }).click();
      await page.getByRole("link", { name: t.home }).click();
      await coversLoaded(page);
      await shot(`${p}_home`, true);
      await page.getByRole("link", { name: t.lib }).click();
      await page.getByRole("tab", { name: t.fav }).click();
      await expect(page.locator(".track").nth(5)).toBeVisible({ timeout: 15_000 });
      await coversLoaded(page);
      await shot(`${p}_favorites`, true);
      await page.getByRole("tab", { name: t.tags }).click().catch(() => {});
      await page.waitForTimeout(1200);
      await shot(`${p}_tags`, true);
      await page.getByRole("link", { name: t.search }).click();
      const ytResult = page.locator(".yt-results li").first();
      const ytButton = page.getByRole("button", { name: /^(Search YouTube for|在 YouTube)/ });
      await expect(async () => {
        await page.getByRole("searchbox").fill("清晨的正弦波");
        await expect(ytResult.or(ytButton)).toBeVisible({ timeout: 5_000 });
      }).toPass({ timeout: 30_000 });
      if (await ytButton.isVisible()) await ytButton.click();
      await expect(ytResult).toBeVisible({ timeout: 30_000 });
      await page.getByRole("searchbox").blur();
      await coversLoaded(page);
      await shot(`${p}_search`, true);
      await page.getByRole("link", { name: t.ch, exact: true }).click();
      await expect(page.locator(".episode-row").first()).toBeVisible({ timeout: 15_000 });
      await coversLoaded(page);
      await shot(`${p}_channels`, true);
      await page.getByRole("link", { name: t.me, exact: true }).click();
      await page.waitForTimeout(1000);
      await shot(`${p}_me`, true);
      await page.goto("/admin/users");
      await page.waitForTimeout(1500);
      await shot(`${p}_users`, true);
    }
  } finally {
    await page.goto("/").catch(() => {});
    await setLanguage(page, null).catch(() => {});
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
  await page.getByRole("link", { name: /^(搜索|Search)$/ }).click();
  const row = page.locator("main").getByRole("button", { name: new RegExp(`^${title} `) }).first(); // not the mini player
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

