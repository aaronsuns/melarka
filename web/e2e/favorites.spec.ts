import { expect, test, type Page } from "@playwright/test";
import { login, trackIdByTitle, verifyPlaybackStarted } from "./playback";

// Puts the shared admin back on "resume", which every other spec expects
// (see global-setup.ts) — keeping whatever language is set.
async function restoreResume(page: Page) {
  const prefs = await (await page.request.get("/api/v1/me/preferences")).json();
  expect((await page.request.put("/api/v1/me/preferences", { data: { ...prefs, on_open: "resume" } })).ok()).toBe(true);
}

// Playwright's browsers allow autoplay (Chromium's headless shell ignores
// --autoplay-policy), which would leave the refused path (Review focus 1)
// to unit tests only. In Chromium, emulate iOS Safari: play() is refused
// with NotAllowedError unless it runs inside a user gesture, until one such
// play() has succeeded (iOS unlocks the element from then on). WebKit keeps
// covering the allowed path.
async function requireGestureForAutoplay(page: Page) {
  await page.addInitScript(() => {
    // Not navigator.userActivation: Playwright's own evaluate() calls count
    // as activation. Only a real (trusted) tap or click does here, and only
    // while its handlers run.
    let inGesture = false;
    for (const type of ["click", "touchend"]) {
      window.addEventListener(
        type,
        (e) => {
          if (!e.isTrusted) return;
          inGesture = true;
          setTimeout(() => (inGesture = false), 0);
        },
        true,
      );
    }
    const realPlay = HTMLMediaElement.prototype.play;
    let unlocked = false;
    HTMLMediaElement.prototype.play = function (this: HTMLMediaElement) {
      if (!unlocked && !inGesture) return Promise.reject(new DOMException("play() needs a user gesture", "NotAllowedError"));
      unlocked = true;
      return realPlay.call(this);
    };
  });
}

test("opening Melarka starts a shuffled favorites queue", async ({ page, browserName }, info) => {
  await login(page);
  try {
    const id = await trackIdByTitle(page, "甜蜜蜜");
    expect((await page.request.put(`/api/v1/favorites/${id}`)).status()).toBe(204);
    await page.getByRole("link", { name: "我的" }).click();
    await page.getByLabel("打开 Melarka 时").selectOption("shuffle_favorites");
    await page.waitForTimeout(500);
    // "Opening Melarka": a new tab of the same session, carrying no user
    // activation from the taps above.
    await page.close();
    const app = await page.context().newPage();
    if (browserName === "chromium") await requireGestureForAutoplay(app);
    await app.goto("/settings");
    await expect(app.locator(".mini")).toBeVisible({ timeout: 15_000 });

    // Either the browser allowed the autoplay (it's already playing) or it
    // refused and the mini player asks for a tap — wait for one of the two.
    const pauseBtn = app.locator(".mini").getByRole("button", { name: "暂停" });
    const tap = app.getByRole("button", { name: /点一下播放你的收藏/ });
    await expect(pauseBtn.or(tap)).toBeVisible({ timeout: 15_000 });
    const refused = await tap.isVisible();
    if (browserName === "chromium") expect(refused, "Chromium (gesture required) must refuse the autoplay").toBe(true);
    info.annotations.push({ type: "autoplay", description: refused ? "refused: started by a tap" : "allowed" });
    const title = (await app.locator(".mini-info .ellipsis").first().textContent()) ?? "";
    const trackId = await trackIdByTitle(app, title);
    await verifyPlaybackStarted(
      app,
      info,
      async () => {
        if (refused) await app.locator(".page-title, h1").first().click(); // any tap counts
      },
      { trackId },
    );
    await expect(pauseBtn).toBeVisible();
    await expect(tap).toHaveCount(0);
    await app.getByLabel("打开 Melarka 时").selectOption("resume");
  } finally {
    await restoreResume(page); // also when the test failed half-way
  }
});
