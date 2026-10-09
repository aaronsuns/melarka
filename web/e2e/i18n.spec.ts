import { expect, test } from "@playwright/test";

test("language follows the server, then the user's choice", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByLabel("用户名")).toBeVisible(); // server default zh-Hans before sign-in
  await page.getByLabel("用户名").fill("admin");
  await page.getByLabel("密码").fill(process.env.LARK_E2E_PW!);
  await page.getByRole("button", { name: "登录" }).click();
  await page.getByRole("link", { name: "我的" }).click();
  await page.getByLabel("语言").selectOption("en");
  await expect(page.getByRole("link", { name: "Library" })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("link", { name: "Library" })).toBeVisible(); // stored per user on the server
  await page.getByRole("link", { name: "Me", exact: true }).click();
  await page.getByLabel("Language").selectOption("");
  await expect(page.getByRole("link", { name: "音乐库" })).toBeVisible();
});
