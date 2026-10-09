import { test as base, expect } from "@playwright/test";

export { expect } from "@playwright/test";
export type { Page, TestInfo } from "@playwright/test";

// The e2e server and its fakes are all on 127.0.0.1 (start-server.sh points
// LARK_YOUTUBE_THUMB_URL at the local feed server), so no page should ask the
// Internet for anything. A request that would is refused here and fails the
// test: on Linux, WebKit's network process sometimes dies when a navigation
// cancels an external load still in flight, and takes the context's cookies
// with it — the next page load hangs, fails, or opens signed out.
const local = (url: URL) => url.hostname === "127.0.0.1" || url.hostname === "localhost";

export const test = base.extend<{ blockExternal: void }>({
  blockExternal: [
    async ({ context }, use) => {
      const external: string[] = [];
      await context.route(
        (url) => !local(url),
        (route) => {
          external.push(route.request().url());
          return route.abort("blockedbyclient");
        },
      );
      await use();
      expect(external, "requests that would have left the machine").toEqual([]);
    },
    { auto: true },
  ],
});
