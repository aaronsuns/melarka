import { render, screen, waitFor } from "@testing-library/react";
import { createElement } from "react";
import { MemoryRouter } from "react-router";
import App from "../App";
import { ApiError } from "../api/client";
import { mockFetch } from "../test/setup";
import { errorMessage, jobErrorShort, jobErrorText } from "./errors";
import { has, setLocale, t } from "./i18n";

test("known codes are translated, unknown ones show the server text", () => {
  setLocale("en");
  expect(errorMessage(new ApiError(400, "only YouTube links are supported", "youtube_only"), "common.actionFailed")).toBe(t("error.youtube_only"));
  expect(errorMessage(new ApiError(400, "weird", "never_heard_of_it"), "common.actionFailed")).toBe("weird");
  expect(errorMessage("boom", "common.actionFailed")).toBe(t("common.actionFailed"));
  expect(jobErrorText("lark:timeout")).toBe(t("job.error.timeout"));
  expect(jobErrorText("ERROR: Video unavailable")).toBe("ERROR: Video unavailable");
});

test("a failed download's row line is short and translated, never yt-dlp's raw output", () => {
  setLocale("zh-Hans");
  expect(jobErrorShort("ERROR: [youtube] abc: unable to download video data: HTTP Error 403: Forbidden")).toBe("下载失败（YouTube 拒绝，稍后重试）");
  expect(jobErrorShort("ERROR: [youtube] abc: Sign in to confirm you're not a bot")).toBe("下载失败（YouTube 拒绝，稍后重试）");
  expect(jobErrorShort("ERROR: [youtube] abc: Video unavailable")).toBe("下载失败（视频不可用）");
  expect(jobErrorShort("ERROR: unable to download webpage: <urlopen error [Errno 8] nodename nor servname>")).toBe("下载失败（网络问题）");
  expect(jobErrorShort("ERROR: something new")).toBe("下载失败");
  expect(jobErrorShort("lark:timeout")).toBe(t("job.error.timeout"));
});

// error.wrong_credentials must be real Chinese in zh-Hans, not
// the English server text left over from a mock that predates codes (the
// mocks in auth.test.tsx/client.test.ts never send a "code" field, so they
// can't catch a regression here — this one builds the coded ApiError directly).
test("a coded wrong_credentials error renders in Chinese under zh-Hans", () => {
  setLocale("zh-Hans");
  expect(errorMessage(new ApiError(401, "wrong username or password", "wrong_credentials"), "common.actionFailed")).toBe("用户名或密码错误");
});

function mockSignedInMember() {
  return mockFetch({
    "GET /api/v1/me": () => ({ body: { id: 1, username: "u", role: "member" } }),
    "GET /api/v1/me/preferences": () => ({ body: { language: null, on_open: "resume" } }),
    "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] } }),
    "PUT /api/v1/queue": () => ({ body: {} }),
    "GET /api/v1/radio/next": () => ({ body: [] }),
    "GET /api/v1/tracks": () => ({ body: { items: [], next_cursor: "" } }),
    "GET /api/v1/downloads": () => ({ body: [] }),
    "GET /api/v1/episodes/latest": () => ({ body: { items: [], unplayed: 3 } }),
  });
}

describe.each(["en", "zh-Hant", "sv"] as const)("the whole shell renders fully translated in %s", (locale) => {
  test("tab labels follow the locale and no raw i18n key leaks through", async () => {
    mockSignedInMember();
    setLocale(locale);
    render(createElement(MemoryRouter, { initialEntries: ["/"] }, createElement(App)));
    expect(await screen.findByRole("link", { name: t("nav.home") })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: t("nav.search") })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: t("nav.library") })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: t("nav.me") })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: t("nav.channels") })).toBeInTheDocument();
    // A key that fell through untranslated renders as the bare "area.name"
    // string instead of real text — none of that should ever reach the DOM.
    expect(screen.queryByText(/^[a-z]+(\.[a-zA-Z]+)+$/)).toBeNull();
  });
});

test("the 频道 tab shows the unplayed count", async () => {
  mockSignedInMember();
  render(createElement(MemoryRouter, { initialEntries: ["/"] }, createElement(App)));
  const link = await screen.findByRole("link", { name: "频道" });
  await waitFor(() => expect(link.querySelector(".tab-badge")?.textContent).toBe("3"));
  expect(link).toHaveAccessibleDescription("3 个新节目");
});

// Every code the channel, preview, discovery and 视频 handlers send (internal/api) is translated.
test.each([
  "channels_off", "not_a_channel_link", "channel_not_found", "follow_limit", "not_following", "bad_settings",
  "episode_not_ready", "episode_expired", "too_soon", "preview_limit", "preview_not_ready", "preview_failed",
  "preview_no_space", "preview_too_long", "preview_retry", "video_preview_unavailable", "bad_media", "bad_video_id",
  "channel_unknown", "bad_query", "youtube_search_failed", "request_cancelled", "hd_unavailable",
])("error.%s has a translation", (code) => {
  expect(has(`error.${code}`)).toBe(true);
});
