import { request, type FullConfig } from "@playwright/test";

// A fresh server opens every user with a shuffle of their favorites (the B2
// default). The specs were written against "resume" — the queue a spec left
// behind is what the next page load shows — so pin the shared admin to it
// once the server is up. favorites.spec.ts switches to shuffle_favorites
// itself and puts "resume" back when it's done.
export default async function globalSetup(config: FullConfig) {
  const baseURL = config.projects[0].use.baseURL!;
  const ctx = await request.newContext({ baseURL });
  try {
    const login = await ctx.post("/api/v1/auth/login", {
      data: { username: "admin", password: process.env.LARK_E2E_PW, device_name: "e2e-setup" },
    });
    if (!login.ok()) throw new Error(`e2e setup: login failed: ${login.status()} ${await login.text()}`);
    const put = await ctx.put("/api/v1/me/preferences", { data: { language: null, on_open: "resume" } });
    if (!put.ok()) throw new Error(`e2e setup: pinning on_open=resume failed: ${put.status()} ${await put.text()}`);
  } finally {
    await ctx.dispose();
  }
}
