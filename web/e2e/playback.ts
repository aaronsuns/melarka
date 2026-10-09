import { expect, type Page, type TestInfo } from "./fixtures";

// Reads the mini player's progress bar as a percentage. It's driven by
// `position / duration` from real `timeupdate` events on the <audio>
// element, so a nonzero value can only happen once the browser has
// genuinely started decoding and advancing playback — not just that
// `.play()` was called (a "playing" DOM event can fire even if decode
// stalls right after).
async function miniProgressPct(page: Page): Promise<number> {
  const style = (await page.locator(".mini-progress").getAttribute("style")) ?? "";
  const m = /width:\s*([\d.]+)%/.exec(style);
  return m ? parseFloat(m[1]) : 0;
}

// PLAYBACK_BUDGET_MS is the one shared budget both proof layers race
// against, starting from the same instant (just before the triggering
// click) — not 10s for one followed by whatever's left of a separate 15s
// for the other. Neither layer gets a head start over the other.
const PLAYBACK_BUDGET_MS = 20_000;

function describeError(e: unknown): string {
  if (e == null) return "(no error captured)";
  if (e instanceof Error) return e.message;
  return String(e);
}

/**
 * Verifies a track genuinely started playing, not just that something
 * called play(). Two proof layers are registered *before* the triggering
 * click and race each other from one shared budget (first one to actually
 * succeed wins):
 *
 * - Progress: the mini player's progress bar moves past 0% — proof the
 *   audio element's position is really ticking (driven by real
 *   `timeupdate` events). Network-independent: works even if the browser
 *   resumed an already-buffered resource without a new HTTP request.
 * - Stream response: the server actually streamed real bytes for this
 *   track — a `waitForResponse` filtered to `trackId` when known. Scoping
 *   to the specific track id is what makes this safe to use at all — a
 *   generic "/stream?quality=" predicate can be satisfied by an unrelated
 *   background request (an earlier restored or prefetched track), which is
 *   exactly the bug this file replaces. When the track id isn't known
 *   ahead of the click (e.g. shuffle picks one at random), this falls back
 *   to the generic predicate — weaker, but still bounded by the same
 *   shared budget, so it can only time out, never hang indefinitely.
 *
 * If *both* layers fail to resolve within the budget, the thrown error
 * keeps each layer's own reason (its real error, or "timeout" from
 * Playwright's own timeout message) rather than collapsing to a generic
 * failure. Records which layer actually won as a test annotation, so a
 * run's report shows which approach each project ended up using.
 */
export async function verifyPlaybackStarted(
  page: Page,
  testInfo: TestInfo,
  click: () => Promise<void>,
  opts: { trackId?: number } = {},
): Promise<void> {
  const urlNeedle = opts.trackId != null ? `/tracks/${opts.trackId}/stream` : "/stream?quality=";

  // Both created synchronously, back to back, before the click — so both
  // budgets start ticking from the same instant.
  const progressProbe = expect
    .poll(() => miniProgressPct(page), { timeout: PLAYBACK_BUDGET_MS, intervals: [300] })
    .toBeGreaterThan(0)
    .then(() => "progress-advance" as const);
  const streamProbe = page
    .waitForResponse((r) => r.url().includes(urlNeedle) && r.status() < 300, { timeout: PLAYBACK_BUDGET_MS })
    .then(() => "stream-response" as const);

  await click();

  let winner: "progress-advance" | "stream-response";
  try {
    winner = await Promise.any([progressProbe, streamProbe]);
  } catch (e) {
    const errors = e instanceof AggregateError ? e.errors : [e];
    const [progressErr, streamErr] = errors;
    throw new Error(
      `playback never verified within ${PLAYBACK_BUDGET_MS}ms — ` +
        `progress check: ${describeError(progressErr)} | ` +
        `stream response check: ${describeError(streamErr)}`,
    );
  }
  testInfo.annotations.push({ type: "playback-verified-via", description: winner });
}

/** Signs in as the e2e admin and waits for the signed-in shell. */
export async function loginWith(page: Page, username: string, password: string): Promise<void> {
  await page.goto("/");
  await page.getByLabel("用户名").fill(username);
  await page.getByLabel("密码").fill(password);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.locator("nav.tabs a").first()).toBeVisible();
}

export async function login(page: Page): Promise<void> {
  await loginWith(page, "admin", process.env.LARK_E2E_PW!);
  await expect(page.getByRole("link", { name: "音乐库" })).toBeVisible();
}

/** Looks up a local-library track's id by exact title, via the same-origin API. */
export async function trackIdByTitle(page: Page, title: string): Promise<number | undefined> {
  const { tracks } = await page.evaluate(
    async (q) => (await fetch(`/api/v1/search?q=${encodeURIComponent(q)}`, { credentials: "same-origin" })).json(),
    title,
  );
  return (tracks as { id: number; title: string }[]).find((t) => t.title === title)?.id;
}

/** Looks up a download job's resulting track id by (substring of) its raw title. */
export async function trackIdByJobTitle(page: Page, titleSubstring: string): Promise<number | undefined> {
  const jobs = (await page.evaluate(
    async () => (await fetch("/api/v1/downloads", { credentials: "same-origin" })).json(),
  )) as { title: string; track_id: number | null }[];
  return jobs.find((j) => j.title.includes(titleSubstring))?.track_id ?? undefined;
}
