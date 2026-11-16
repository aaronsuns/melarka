import { expect, test, type Page, type TestInfo } from "./fixtures";

// Quick account switch against the real server: the session cookie moves between two members on one
// browser, survives a reload, and needs a password only the first time. Members only (see video.spec.ts
// for why the shared admin is never opened in a page).
async function member(page: Page, info: TestInfo, who: string): Promise<{ username: string; password: string }> {
  const username = `sw-${who}-${info.project.name}`;
  const password = `switch-e2e-${who}`;
  const r = await page.request.post("/api/v1/users", { data: { username, password, role: "member" } });
  expect([201, 409]).toContain(r.status());
  return { username, password };
}

async function openOnNothing(page: Page) {
  const prefs = await (await page.request.get("/api/v1/me/preferences")).json();
  expect((await page.request.put("/api/v1/me/preferences", { data: { ...prefs, on_open: "nothing" } })).ok()).toBe(true);
}

const signIn = async (page: Page, username: string, password: string) =>
  expect((await page.request.post("/api/v1/auth/login", { data: { username, password, device_name: "e2e" } })).ok()).toBe(true);

test("adds a second account once, then switches back and forth with one tap", async ({ page }, info) => {
  await signIn(page, "admin", process.env.LARK_E2E_PW!);
  const dad = await member(page, info, "dad");
  const kid = await member(page, info, "kid");
  await page.context().clearCookies();
  await signIn(page, kid.username, kid.password);
  await openOnNothing(page);
  await page.context().clearCookies();
  await signIn(page, dad.username, dad.password);
  await openOnNothing(page);

  await page.goto("/settings");
  const signedInAs = (name: string) => page.locator("p.muted", { hasText: name });
  await expect(signedInAs(dad.username)).toBeVisible();

  await page.getByRole("button", { name: "添加账号" }).click();
  await page.getByLabel("用户名").fill(kid.username);
  await page.getByLabel("密码").fill(kid.password);
  await page.getByRole("button", { name: "登录并切换" }).click();
  await expect(signedInAs(kid.username)).toBeVisible();

  await page.goto("/settings");
  await page.getByRole("button", { name: `切换到 ${dad.username}` }).click();
  await expect(signedInAs(dad.username)).toBeVisible();

  // The cookie really moved: a reload stays on dad, and kid is one tap away.
  await page.reload();
  await expect(signedInAs(dad.username)).toBeVisible();
  await page.getByRole("button", { name: `切换到 ${kid.username}` }).click();
  await expect(signedInAs(kid.username)).toBeVisible();
  const me = await (await page.request.get("/api/v1/me")).json();
  expect(me.username).toBe(kid.username);
});
