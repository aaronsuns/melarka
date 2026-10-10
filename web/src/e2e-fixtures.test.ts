// Every e2e spec must run on the network-sealed `test` of e2e/fixtures.ts,
// which fails a test whose page asks anything but 127.0.0.1. A spec that
// imports Playwright's own `test` would silently skip that guard, so no file
// under e2e/ may import "@playwright/test" except fixtures.ts (which wraps it)
// and global-setup.ts (no page: it only calls the API before the run).
const files = import.meta.glob(["../e2e/**/*.ts"], {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

const name = (path: string) => path.replace(/^\.\.\/e2e\//, "");
const exempt = new Set(["fixtures.ts", "global-setup.ts"]);
const importsFrom = (src: string, mod: string) =>
  new RegExp(`(?:\\bfrom|\\bimport)\\s*\\(?\\s*["']${mod.replace(/[./]/g, "\\$&")}["']`).test(src);

test("the e2e files are found", () => {
  expect(Object.keys(files).map(name)).toContain("fixtures.ts");
  expect(Object.keys(files).map(name).filter((f) => f.endsWith(".spec.ts")).length).toBeGreaterThan(5);
});

test("no e2e file but fixtures.ts imports @playwright/test", () => {
  const offenders = Object.entries(files)
    .filter(([path]) => !exempt.has(name(path)))
    .filter(([, src]) => importsFrom(src, "@playwright/test"))
    .map(([path]) => name(path));
  expect(offenders).toEqual([]);
});

test("every e2e spec imports from ./fixtures", () => {
  const offenders = Object.entries(files)
    .filter(([path]) => path.endsWith(".spec.ts"))
    .filter(([, src]) => !importsFrom(src, "./fixtures"))
    .map(([path]) => name(path));
  expect(offenders).toEqual([]);
});

test("the import check sees the forms a spec could use", () => {
  expect(importsFrom(`import { test } from "@playwright/test";`, "@playwright/test")).toBe(true);
  expect(importsFrom(`import type { Locator } from '@playwright/test';`, "@playwright/test")).toBe(true);
  expect(importsFrom(`const pw = await import("@playwright/test");`, "@playwright/test")).toBe(true);
  expect(importsFrom(`import { expect, test } from "./fixtures";`, "./fixtures")).toBe(true);
  expect(importsFrom(`import { expect, test } from "./fixturesX";`, "./fixtures")).toBe(false);
});
