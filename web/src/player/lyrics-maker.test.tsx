import { act, fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MiniPlayer } from "./MiniPlayer";
import { usePlayer, type Player } from "./PlayerProvider";
import { forgetLyrics } from "./lyricsCache";
import { lrcTime, toLRC } from "./LyricsMaker";
import { renderWithApp } from "../test/render";
import type { Track } from "../api/types";

let p!: Player;
function Probe() { p = usePlayer(); return null; }
const tr = { id: 1, title: "格桑花", artist: "梁红", album: "A", duration_ms: 297000, codec: "mp3", lossless: false, bitrate: 320, favorite: false, disliked: false, broken: false, broken_reason: "" } as Track;

beforeEach(() => forgetLyrics(1));

test("LRC time stamps and text", () => {
  expect(lrcTime(0)).toBe("[00:00.00]");
  expect(lrcTime(75.456)).toBe("[01:15.46]");
  expect(toLRC(["第一句", "第二句"], [1, 2.5])).toBe("[00:01.00]第一句\n[00:02.50]第二句");
});

test("制作歌词: paste the words, tap each line as it starts, save — they show for this song", async () => {
  const saved: string[] = [];
  const { audio } = renderWithApp(<><Probe /><MiniPlayer /></>, {
    routes: {
      "GET /api/v1/tracks/1/lyrics": () => ({ body: { found: false, synced: false, offset_ms: 0 } }),
      "PUT /api/v1/tracks/1/lyrics/own": (init) => {
        const text = JSON.parse(String(init.body)).text as string;
        saved.push(text);
        return { body: { found: true, id: 5, source: "user", synced: true, offset_ms: 0, lines: [{ t_ms: 1000, text: "第一句" }, { t_ms: 2500, text: "第二句" }, { t_ms: 4000, text: "第三句" }] } };
      },
    },
  });
  act(() => p.playList([tr], 0));
  fireEvent.click(await screen.findByRole("button", { name: /格桑花/ }));
  const now = await screen.findByRole("dialog", { name: "正在播放" });
  await userEvent.click(within(now).getByRole("button", { name: "歌词" }));
  await userEvent.click(await within(now).findByRole("button", { name: "制作歌词" }));
  const maker = await screen.findByRole("dialog", { name: "制作歌词" });
  await userEvent.type(within(maker).getByRole("textbox"), "第一句{enter}{enter}第二句{enter}第三句");
  await userEvent.click(within(maker).getByRole("button", { name: "开始打点" }));
  expect(audio.currentTime).toBe(0); // from the top
  const tapAt = async (s: number) => {
    audio.currentTime = s;
    act(() => audio.fire("timeupdate"));
    await userEvent.click(within(maker).getByRole("button", { name: /^打点/ }));
  };
  await tapAt(1);
  await tapAt(9); // a wrong tap…
  await userEvent.click(within(maker).getByRole("button", { name: "撤销" })); // …taken back
  await tapAt(2.5);
  await tapAt(4);
  await userEvent.click(within(maker).getByRole("button", { name: "保存" }));
  expect(saved).toEqual(["[00:01.00]第一句\n[00:02.50]第二句\n[00:04.00]第三句"]); // the blank line dropped
  expect(screen.queryByRole("dialog", { name: "制作歌词" })).toBeNull();
  expect(await within(now).findByText("第二句")).toBeInTheDocument();
});
