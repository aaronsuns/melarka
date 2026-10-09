import { defineConfig, devices } from "@playwright/test";
import { randomUUID } from "node:crypto";

// A fresh admin password per run — never commit credentials.
process.env.LARK_E2E_PW ??= randomUUID();

export default defineConfig({
  testDir: "e2e",
  // Runs after webServer is up: pins the admin's on_open to "resume".
  globalSetup: "./e2e/global-setup.ts",
  timeout: 60_000,
  use: { baseURL: "http://127.0.0.1:4700", trace: "retain-on-failure" },
  webServer: {
    command: "bash e2e/start-server.sh",
    url: "http://127.0.0.1:4700/",
    timeout: 120_000,
    reuseExistingServer: false,
    env: { LARK_E2E_PW: process.env.LARK_E2E_PW },
  },
  projects: [
    { name: "iphone-webkit", use: { ...devices["iPhone 13"] } },
    // --mute-audio silences the speakers; media still plays, so playback proofs hold.
    {
      name: "chromium-360",
      use: {
        ...devices["Desktop Chrome"],
        viewport: { width: 360, height: 740 },
        launchOptions: { args: ["--mute-audio"] },
      },
    },
  ],
  workers: 1, // both projects share one server and one admin's queue/favorites
});
