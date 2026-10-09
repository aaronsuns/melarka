import { fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import SearchPage from "./SearchPage";
import { renderWithApp } from "../test/render";
import type { Track } from "../api/types";

const empty: { tracks: Track[]; albums: never[]; artists: never[] } = { tracks: [], albums: [], artists: [] };
const tr = (id: number) => ({ id, title: "甜蜜蜜", artist: "邓丽君", album: "精选", duration_ms: 1000, codec: "mp3", bitrate: 320, lossless: false }) as Track;
function setup(history: string[], search = empty) {
  const post = vi.fn(() => ({ status: 204 }));
  const del = vi.fn(() => ({ status: 204 }));
  const r = renderWithApp(<SearchPage />, {
    path: "/search",
    routes: {
      "GET /api/v1/me/search-history": () => ({ body: history }),
      "POST /api/v1/me/search-history": post,
      "DELETE /api/v1/me/search-history": del,
      "GET /api/v1/search": () => ({ body: search }),
      "GET /api/v1/youtube/search": () => ({ body: { videos: [], playlists: [] } }),
    },
  });
  const posted = () => post.mock.calls.map((c) => JSON.parse(String((c as unknown[] as RequestInit[])[0].body)).query);
  return { ...r, post, del, posted };
}

test("an empty box shows recent searches; a chip searches again and moves to the top", async () => {
  const { posted } = setup(["邓丽君", "甜蜜蜜"]);
  expect(await screen.findByRole("heading", { name: "最近搜索" })).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "甜蜜蜜" }));
  expect(screen.getByRole("searchbox")).toHaveValue("甜蜜蜜");
  expect(screen.queryByRole("heading", { name: "最近搜索" })).toBeNull(); // box not empty any more
  expect(posted()).toEqual(["甜蜜蜜"]);
  await userEvent.clear(screen.getByRole("searchbox"));
  const chips = within(screen.getByRole("group", { name: "最近搜索" })).getAllByRole("button");
  expect(chips.map((c) => c.textContent)).toEqual(["甜蜜蜜", "邓丽君", "清除"]);
});

test("a re-typed query dedupes case-insensitively, including Å/å, like the server", async () => {
  setup(["Kent", "Åsa"]);
  await screen.findByRole("heading", { name: "最近搜索" });
  const box = screen.getByRole("searchbox");
  await userEvent.type(box, " åsa ");
  fireEvent.keyDown(box, { key: "Enter" });
  await userEvent.clear(box);
  const chips = within(screen.getByRole("group", { name: "最近搜索" })).getAllByRole("button");
  expect(chips.map((c) => c.textContent)).toEqual(["åsa", "Kent", "清除"]);
});

test("Enter records the search; an IME-confirming Enter and a pasted link don't", async () => {
  const { posted } = setup([]);
  const box = screen.getByRole("searchbox");
  await userEvent.type(box, "dlj");
  fireEvent.keyDown(box, { key: "Enter", isComposing: true });
  expect(posted()).toEqual([]);
  fireEvent.keyDown(box, { key: "Enter" });
  await vi.waitFor(() => expect(posted()).toEqual(["dlj"]));
  await userEvent.clear(box);
  await userEvent.type(box, "https://www.youtube.com/watch?v=fakevideo01");
  fireEvent.keyDown(box, { key: "Enter" });
  expect(posted()).toEqual(["dlj"]);
});

test("opening a result records the query", async () => {
  const { posted } = setup([], { tracks: [tr(1)], albums: [], artists: [] });
  await userEvent.type(screen.getByRole("searchbox"), "tmm");
  await userEvent.click(await screen.findByText("甜蜜蜜"));
  await vi.waitFor(() => expect(posted()).toEqual(["tmm"]));
});

test("清除 empties the history", async () => {
  const { del } = setup(["邓丽君"]);
  await userEvent.click(await screen.findByRole("button", { name: "清除" }));
  await vi.waitFor(() => expect(del).toHaveBeenCalled());
  expect(screen.queryByRole("heading", { name: "最近搜索" })).toBeNull();
});
