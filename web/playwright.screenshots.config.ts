import { defineConfig, devices } from "@playwright/test";

// README screenshots against an already running Lark (production): no local server.
// LARK_SHOT_URL / LARK_SHOT_USER / LARK_SHOT_PW come from the caller's environment.
export default defineConfig({
  testDir: "e2e",
  testMatch: "screenshots.spec.ts",
  timeout: 120_000,
  use: { baseURL: process.env.LARK_SHOT_URL, trace: "off" },
  projects: [{ name: "iphone-webkit", use: { ...devices["iPhone 13"] } }],
  workers: 1,
});
