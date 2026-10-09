import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, expect, test } from "vitest";
import App from "../App";
import { mockFetch } from "../test/setup";
import { setChannelsEnabled } from "./channelsSwitch";

afterEach(() => setChannelsEnabled(true));

const signedIn = () =>
  mockFetch({
    "GET /api/v1/me": () => ({ body: { id: 1, username: "alice", role: "admin" } }),
    "GET /api/v1/episodes/latest": () => ({ body: { items: [], unplayed: 3 } }),
  });
const latestCalls = (f: ReturnType<typeof mockFetch>) => f.mock.calls.filter(([u]) => String(u).includes("/episodes/latest")).length;

test("channels on: the 频道 tab shows with its badge", async () => {
  const f = signedIn();
  render(<MemoryRouter initialEntries={["/library"]}><App /></MemoryRouter>);
  expect(await screen.findByRole("link", { name: "频道" })).toBeInTheDocument();
  expect(latestCalls(f)).toBeGreaterThan(0);
});

// /info channels:false (channels.enabled off) hides the tab and asks nothing.
test("channels off: no 频道 tab and the unplayed count is never read", async () => {
  setChannelsEnabled(false);
  const f = signedIn();
  render(<MemoryRouter initialEntries={["/library"]}><App /></MemoryRouter>);
  expect(await screen.findByRole("link", { name: "音乐库" })).toBeInTheDocument();
  expect(screen.queryByRole("link", { name: "频道" })).toBeNull();
  expect(latestCalls(f)).toBe(0);
});

// 视频: the 6th tab, there for every signed-in user while Channels is on.
const member = () =>
  mockFetch({
    "GET /api/v1/me": () => ({ body: { id: 2, username: "bob", role: "member" } }),
    "GET /api/v1/episodes/latest": () => ({ body: { items: [], unplayed: 0 } }),
  });

test("channels on: a member sees the 视频 tab after 频道, and /video and /watch/:id are pages", async () => {
  member();
  render(<MemoryRouter initialEntries={["/video"]}><App /></MemoryRouter>);
  const tab = await screen.findByRole("link", { name: "视频" });
  expect(tab).toHaveAttribute("href", "/video");
  const names = [...tab.closest("nav")!.querySelectorAll("a")].map((a) => a.textContent);
  expect(names).toEqual(["首页", "搜索", "音乐库", "频道", "视频", "我的"]);
  expect(tab.closest("nav")).toHaveClass("tabs", "tabs-6");
  expect(screen.getByRole("heading", { name: "视频" })).toBeInTheDocument();
  expect(screen.queryByText("页面不存在")).toBeNull();
});

test("/watch/:id is a page, not 页面不存在", async () => {
  member();
  render(<MemoryRouter initialEntries={["/watch/dQw4w9WgXcQ"]}><App /></MemoryRouter>);
  expect(await screen.findByRole("link", { name: "视频" })).toBeInTheDocument();
  expect(screen.queryByText("页面不存在")).toBeNull();
});

test("channels off: no 视频 tab, five columns are not six", async () => {
  setChannelsEnabled(false);
  member();
  render(<MemoryRouter initialEntries={["/library"]}><App /></MemoryRouter>);
  const lib = await screen.findByRole("link", { name: "音乐库" });
  expect(screen.queryByRole("link", { name: "视频" })).toBeNull();
  expect(lib.closest("nav")).not.toHaveClass("tabs-6");
});
