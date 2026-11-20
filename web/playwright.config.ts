import { defineConfig, devices } from "@playwright/test";
import { randomUUID } from "node:crypto";

// A fresh admin password per run — never commit credentials.
process.env.LARK_E2E_PW ??= randomUUID();
// LARK_E2E_PORT / LARK_E2E_FEED_PORT move the throwaway server off 4700/4701,
// e.g. while another checkout's run holds them.
const port = process.env.LARK_E2E_PORT ?? "4700";
const feedPort = process.env.LARK_E2E_FEED_PORT ?? "4701";

export default defineConfig({
  testDir: "e2e",
  // Runs after webServer is up: pins the admin's on_open to "resume".
  globalSetup: "./e2e/global-setup.ts",
  timeout: 60_000,
  use: { baseURL: `http://127.0.0.1:${port}`, trace: "retain-on-failure" },
  webServer: {
    command: "bash e2e/start-server.sh",
    url: `http://127.0.0.1:${port}/`,
    timeout: 120_000,
    reuseExistingServer: false,
    // LARK_SCREENSHOTS=1 starts the made-up demo library instead (e2e/demo/demo.py).
    env: { LARK_E2E_PW: process.env.LARK_E2E_PW, LARK_E2E_PORT: port, LARK_E2E_FEED_PORT: feedPort, LARK_DEMO: process.env.LARK_SCREENSHOTS === "1" ? "1" : "" },
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
