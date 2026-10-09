import { test as base } from "@playwright/test";

export { expect } from "@playwright/test";
export type { Page, TestInfo } from "@playwright/test";

// The e2e server and its fakes are all on 127.0.0.1, but the pages it serves
// still name some images on the Internet (YouTube thumbnails, in episode rows,
// search results and recommendations). Every such request is refused here,
// before it leaves the browser: the tests never depend on the network, and a
// navigation never has to cancel an external load still in flight — on Linux,
// WebKit sometimes stalls on that and loses the context's cookies, so the next
// page load hangs, fails, or opens signed out.
const local = (url: URL) => url.hostname === "127.0.0.1" || url.hostname === "localhost" || url.protocol === "data:" || url.protocol === "blob:";

export const test = base.extend<{ blockExternal: void }>({
  blockExternal: [
    async ({ context }, use) => {
      await context.route((url) => !local(url), (route) => route.abort("blockedbyclient"));
      await use();
    },
    { auto: true },
  ],
});
