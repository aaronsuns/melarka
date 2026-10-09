import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import SettingsPage from "./SettingsPage";
import { renderWithApp } from "../test/render";
import { FakeCaches } from "../test/offline";
import { OfflineCache, type ManagerDeps } from "../offline/manager";
import { setOfflineForTests } from "../offline";
import type { Track } from "../api/types";

const fav = (id: number) => ({ id, title: `歌${id}`, duration_ms: 1, bitrate: 256, favorite: true }) as Track;

function offline(favorites: Track[]) {
  const persist = vi.fn(async () => true);
  let release: (() => void) | null = null;
  const gate = { hold: false };
  const deps: ManagerDeps = {
    caches: new FakeCaches() as unknown as ManagerDeps["caches"],
    fetch: async (url) => {
      if (url.includes("/stream")) {
        if (gate.hold) await new Promise<void>((r) => (release = r));
        return new Response(new Uint8Array(3 * 1024 * 1024), { headers: { "Content-Type": "audio/mp4" } });
      }
      return new Response("", { status: 404 });
    },
    storage: localStorage,
    listFavorites: async () => favorites,
    online: () => true,
    busy: () => false,
    now: () => Date.now(),
    notifyWorker: () => {},
    persist,
    estimate: async () => ({ usage: 5 * 1024 * 1024, quota: 1024 * 1024 * 1024 }),
  };
  const m = new OfflineCache(deps);
  setOfflineForTests(m);
  return { m, persist, gate, release: () => release?.() };
}

afterEach(() => setOfflineForTests(undefined));

test("the offline section: usage against the cap, the cap picker, mobile data off by default, an honest hint", async () => {
  offline([]);
  renderWithApp(<SettingsPage />);
  expect(await screen.findByRole("heading", { name: "离线缓存" })).toBeInTheDocument();
  expect(screen.getByText("已用 0 B / 1 GB")).toBeInTheDocument();
  expect(screen.getByText("已缓存 0 首")).toBeInTheDocument();
  const cap = screen.getByLabelText("缓存上限") as HTMLSelectElement;
  expect([...cap.options].map((o) => o.textContent)).toEqual(["500 MB", "1 GB", "2 GB", "5 GB"]);
  expect(cap.value).toBe(String(1024 * 1024 * 1024));
  const mobile = screen.getByLabelText("允许使用移动数据") as HTMLInputElement;
  expect(mobile.checked).toBe(false);
  expect(screen.getByText(/分不清 Wi-Fi 和移动数据/)).toBeInTheDocument();
  expect(screen.getByText(/添加到主屏幕/)).toBeInTheDocument();
  fireEvent.change(cap, { target: { value: String(2048 * 1024 * 1024) } });
  await waitFor(() => expect(localStorage.getItem("lark.offline.cap")).toBe(String(2048 * 1024 * 1024)));
  fireEvent.click(mobile);
  expect(localStorage.getItem("lark.offline.mobileData")).toBe("1");
});

test("Cache favorites now shows n/m and the current song, then the count; persistent storage is asked for", async () => {
  const o = offline([fav(1), fav(2)]);
  o.gate.hold = true;
  renderWithApp(<SettingsPage />);
  fireEvent.click(await screen.findByRole("button", { name: "立即缓存收藏" }));
  expect(await screen.findByText("正在缓存 0/2：歌1")).toBeInTheDocument();
  expect(o.persist).toHaveBeenCalled();
  o.gate.hold = false;
  act(() => o.release());
  expect(await screen.findByText("已缓存 2 首")).toBeInTheDocument();
  expect(screen.getByText("已用 6 MB / 1 GB")).toBeInTheDocument();
});

test("Clear cache asks inline first", async () => {
  const o = offline([fav(1)]);
  renderWithApp(<SettingsPage />);
  fireEvent.click(await screen.findByRole("button", { name: "立即缓存收藏" }));
  expect(await screen.findByText("已缓存 1 首")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "清除缓存" }));
  expect(o.m.isCached(1)).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  fireEvent.click(screen.getByRole("button", { name: "清除缓存" }));
  fireEvent.click(screen.getByRole("button", { name: "删除全部缓存" }));
  expect(await screen.findByText("已缓存 0 首")).toBeInTheDocument();
  expect(o.m.isCached(1)).toBe(false);
});

test("where offline caching can't work, the section says so", async () => {
  setOfflineForTests(null);
  renderWithApp(<SettingsPage />);
  expect(await screen.findByRole("heading", { name: "离线缓存" })).toBeInTheDocument();
  expect(screen.getByText(/需要 HTTPS/)).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "立即缓存收藏" })).toBeNull();
});

test("a full device storage is shown", async () => {
  const o = offline([fav(1)]);
  renderWithApp(<SettingsPage />);
  const caches = (o.m as unknown as { deps: ManagerDeps }).deps.caches as unknown as FakeCaches;
  (await caches.open("lark-offline-v1")).failPut = (k) => k.includes("/stream");
  fireEvent.click(await screen.findByRole("button", { name: "立即缓存收藏" }));
  expect(await screen.findByText(/设备存储已满/)).toBeInTheDocument();
});
