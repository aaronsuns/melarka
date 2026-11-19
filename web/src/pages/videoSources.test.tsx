import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, expect, test, vi } from "vitest";
import App from "../App";
import { SourceSwitch } from "../components/SourceSwitch";
import { mockFetch } from "../test/setup";
import { renderWithApp } from "../test/render";
import { setVideoSources } from "../videoSources";
import SearchPage from "./SearchPage";
import SystemPage from "./admin/SystemPage";

afterEach(() => {
  setVideoSources(["youtube"]);
  try {
    localStorage.removeItem("lark.videoSource");
  } catch {
    /* storage unavailable */
  }
});

const bili = { id: "BV1aaaaaaaa1", title: "晴天", channel: "某UP主", url: "https://www.bilibili.com/video/BV1aaaaaaaa1", thumbnail: "https://i0.hdslb.com/bfs/archive/a.jpg", duration_s: 269 };

test("the source switch shows only with two sources and remembers the choice", async () => {
  const onChange = vi.fn();
  const { rerender } = render(<SourceSwitch sources={["youtube"]} value="youtube" onChange={onChange} />);
  expect(screen.queryByRole("radio")).toBeNull();
  rerender(<SourceSwitch sources={["youtube", "bilibili"]} value="youtube" onChange={onChange} />);
  await userEvent.click(screen.getByRole("radio", { name: "哔哩哔哩" }));
  expect(onChange).toHaveBeenCalledWith("bilibili");
  expect(localStorage.getItem("lark.videoSource")).toBe("bilibili");
});

test("with only Bilibili on, search asks Bilibili and shows no switch", async () => {
  setVideoSources(["bilibili"]);
  const search = vi.fn((_i: RequestInit, url: string) => {
    expect(url).toContain("source=bilibili");
    return { body: { videos: [bili], playlists: [] } };
  });
  renderWithApp(<SearchPage />, {
    routes: {
      "GET /api/v1/search": () => ({ body: { tracks: [], albums: [], artists: [] } }),
      "GET /api/v1/youtube/search": search,
    },
  });
  await userEvent.type(screen.getByRole("searchbox"), "晴天");
  expect(await screen.findByText("晴天", {}, { timeout: 3000 })).toBeInTheDocument();
  expect(screen.getByRole("heading", { name: "哔哩哔哩" })).toBeInTheDocument();
  expect(screen.queryByRole("radio")).toBeNull();
});

test("with both on, the switch moves the search to Bilibili", async () => {
  setVideoSources(["youtube", "bilibili"]);
  const urls: string[] = [];
  renderWithApp(<SearchPage />, {
    routes: {
      "GET /api/v1/search": () => ({ body: { tracks: [], albums: [], artists: [] } }),
      "GET /api/v1/youtube/search": (_i: RequestInit, url: string) => {
        urls.push(url);
        return { body: { videos: url.includes("bilibili") ? [bili] : [], playlists: [] } };
      },
    },
  });
  await userEvent.type(screen.getByRole("searchbox"), "晴天");
  await userEvent.click(await screen.findByRole("radio", { name: "哔哩哔哩" }, { timeout: 3000 }));
  expect(await screen.findByText("晴天", {}, { timeout: 3000 })).toBeInTheDocument();
  expect(urls.some((u) => u.includes("source=bilibili"))).toBe(true);
});

test("with no source on, video search is hidden and says so", async () => {
  setVideoSources([]);
  const search = vi.fn(() => ({ body: { videos: [], playlists: [] } }));
  renderWithApp(<SearchPage />, {
    routes: {
      "GET /api/v1/search": () => ({ body: { tracks: [], albums: [], artists: [] } }),
      "GET /api/v1/youtube/search": search,
    },
  });
  await userEvent.type(screen.getByRole("searchbox"), "晴天");
  expect(await screen.findByText(/没有开启视频来源/, {}, { timeout: 3000 })).toBeInTheDocument();
  expect(search).not.toHaveBeenCalled();
});

test("a pasted link for a source that is off says so", async () => {
  setVideoSources(["bilibili"]);
  renderWithApp(<SearchPage />, { routes: { "GET /api/v1/search": () => ({ body: { tracks: [], albums: [], artists: [] } }) } });
  await userEvent.type(screen.getByRole("searchbox"), "https://www.youtube.com/watch?v=IiFm7AWP9n4");
  expect(await screen.findByText(/已关闭 YouTube/)).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /下载/ })).toBeNull();
});

test("YouTube off hides the 视频 and 频道 tabs", async () => {
  setVideoSources(["bilibili"]);
  mockFetch({
    "GET /api/v1/me": () => ({ body: { id: 1, username: "alice", role: "admin" } }),
    "GET /api/v1/episodes/latest": () => ({ body: { items: [], unplayed: 0 } }),
  });
  render(<MemoryRouter initialEntries={["/library"]}><App /></MemoryRouter>);
  expect(await screen.findByRole("link", { name: "音乐库" })).toBeInTheDocument();
  expect(screen.queryByRole("link", { name: "频道" })).toBeNull();
  expect(screen.queryByRole("link", { name: "视频" })).toBeNull();
});

test("Admin → System saves the video sources", async () => {
  const put = vi.fn((init: RequestInit) => {
    expect(JSON.parse(init.body as string)).toEqual({ sources: ["youtube", "bilibili"] });
    return { body: { sources: ["youtube", "bilibili"], set_by: "admin" } };
  });
  renderWithApp(<SystemPage />, {
    role: "admin",
    routes: {
      "GET /api/v1/admin/video-sources": () => ({ body: { sources: ["youtube"], set_by: "env" } }),
      "PUT /api/v1/admin/video-sources": put,
    },
  });
  const box = await screen.findByRole("checkbox", { name: "哔哩哔哩" });
  expect(screen.getByText(/LARK_VIDEO_SOURCES/)).toBeInTheDocument();
  await userEvent.click(box);
  await waitFor(() => expect(put).toHaveBeenCalled());
  expect(await screen.findByText(/已在这里保存/)).toBeInTheDocument();
});
