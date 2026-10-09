import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import SettingsPage from "./SettingsPage";
import { renderWithApp } from "../test/render";
import { usePlayer, type Player } from "../player/PlayerProvider";
import { dictionaries, getLocale, setLocale, t } from "../i18n/i18n";
import type { Track } from "../api/types";

let p!: Player;
function Probe() { p = usePlayer(); return null; }
const tr = { id: 1, title: "甜蜜蜜", artist: "邓丽君", album: "", duration_ms: 200000 } as Track;

test("switching language saves the pref and does not interrupt playback", async () => {
  const { audio, f } = renderWithApp(<><Probe /><SettingsPage /></>, {
    routes: { "PUT /api/v1/me/preferences": (init) => ({ body: JSON.parse(init.body as string) }) },
  });
  act(() => p.playList([tr], 0));
  const src = audio.src;
  audio.pause.mockClear();
  fireEvent.change(await screen.findByLabelText("语言"), { target: { value: "en" } });
  await waitFor(() => expect(getLocale()).toBe("en"));
  expect(await screen.findByLabelText("Language")).toBeInTheDocument();
  const put = f.mock.calls.find((c) => String(c[0]).endsWith("/me/preferences") && c[1]?.method === "PUT")!;
  expect(JSON.parse(put[1]!.body as string)).toEqual({ language: "en", on_open: "resume" });
  expect(audio.pause).not.toHaveBeenCalled();
  expect(audio.src).toBe(src);
});

test("a key missing from the current locale falls back to English, never blank", () => {
  // Uses a real key: delete it from sv for the duration of the test.
  const saved = dictionaries.sv["settings.language"];
  delete dictionaries.sv["settings.language"];
  setLocale("sv");
  expect(t("settings.language")).toBe("Language");
  dictionaries.sv["settings.language"] = saved;
});

test("the When Melarka opens select saves on_open", async () => {
  const { f } = renderWithApp(<SettingsPage />, {
    routes: { "PUT /api/v1/me/preferences": (init) => ({ body: JSON.parse(init.body as string) }) },
  });
  const sel = (await screen.findByLabelText("打开 Melarka 时")) as HTMLSelectElement;
  expect(sel.value).toBe("resume");
  fireEvent.change(sel, { target: { value: "nothing" } });
  await waitFor(() => expect(f.mock.calls.some((c) => String(c[0]).endsWith("/me/preferences") && c[1]?.method === "PUT")).toBe(true));
  const put = f.mock.calls.find((c) => String(c[0]).endsWith("/me/preferences") && c[1]?.method === "PUT")!;
  expect(JSON.parse(put[1]!.body as string)).toEqual({ language: null, on_open: "nothing" });
  await waitFor(() => expect(sel.value).toBe("nothing"));
});

test("car lyrics is on by default, explained, and saves car_lyrics", async () => {
  const { f } = renderWithApp(<SettingsPage />, {
    routes: { "PUT /api/v1/me/preferences": (init) => ({ body: JSON.parse(init.body as string) }) },
  });
  const box = (await screen.findByRole("checkbox", { name: "车载歌词（蓝牙）" })) as HTMLInputElement;
  expect(box.checked).toBe(true);
  expect(screen.getByText(/锁屏/)).toBeInTheDocument();
  fireEvent.click(box);
  await waitFor(() => expect(f.mock.calls.some((c) => String(c[0]).endsWith("/me/preferences") && c[1]?.method === "PUT")).toBe(true));
  const put = f.mock.calls.find((c) => String(c[0]).endsWith("/me/preferences") && c[1]?.method === "PUT")!;
  expect(JSON.parse(put[1]!.body as string)).toEqual({ language: null, on_open: "resume", car_lyrics: false });
  await waitFor(() => expect(box.checked).toBe(false));
});

test("volume normalization is on by default, explained, and saves normalize_loudness", async () => {
  const { f } = renderWithApp(<SettingsPage />, {
    routes: { "PUT /api/v1/me/preferences": (init) => ({ body: JSON.parse(init.body as string) }) },
  });
  const box = (await screen.findByRole("checkbox", { name: "音量均衡" })) as HTMLInputElement;
  expect(box.checked).toBe(true);
  expect(screen.getByText(/不放大安静的歌/)).toBeInTheDocument();
  fireEvent.click(box);
  await waitFor(() => expect(f.mock.calls.some((c) => String(c[0]).endsWith("/me/preferences") && c[1]?.method === "PUT")).toBe(true));
  const put = f.mock.calls.find((c) => String(c[0]).endsWith("/me/preferences") && c[1]?.method === "PUT")!;
  expect(JSON.parse(put[1]!.body as string)).toEqual({ language: null, on_open: "resume", normalize_loudness: false });
  await waitFor(() => expect(box.checked).toBe(false));
});

test("the footer shows the server version and links to the source (AGPL §13)", async () => {
  renderWithApp(<SettingsPage />, {
    routes: { "GET /api/v1/info": () => ({ body: { name: "Melarka", version: "0.1.0", language: "zh-Hans", languages: [] } }) },
  });
  expect(await screen.findByText("版本 0.1.0", { exact: false })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "源代码" })).toHaveAttribute("href", "https://github.com/aaronsuns/melarka");
});

test("no version line when the server reports none", async () => {
  const { f } = renderWithApp(<SettingsPage />, {
    routes: { "GET /api/v1/info": () => ({ body: { name: "Melarka", language: "zh-Hans", languages: [] } }) },
  });
  await waitFor(() => expect(f.mock.calls.some((c) => String(c[0]).endsWith("/api/v1/info"))).toBe(true));
  await screen.findByLabelText("语言");
  expect(screen.queryByText(/版本/)).toBeNull();
});
