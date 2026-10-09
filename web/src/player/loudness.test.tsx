import { act, waitFor } from "@testing-library/react";
import { expect, test } from "vitest";
import type { Track } from "../api/types";
import { renderWithApp } from "../test/render";
import { usePlayer, type Player } from "./PlayerProvider";

const tr = (id: number, extra: Partial<Track> = {}) => ({ id, title: `t${id}`, artist: "a", album: "b", duration_ms: 200000, ...extra }) as Track;

let p!: Player;
function Probe() {
  p = usePlayer();
  return null;
}

test("music gain never touches the episode or preview elements", async () => {
  const { audio, episodeAudio, previewAudio } = renderWithApp(<Probe />);
  await waitFor(() => expect(p).toBeTruthy());
  act(() => p.playList([tr(1, { gain_db: -6 })], 0));
  await waitFor(() => expect(audio.volume).toBeCloseTo(0.501, 3));
  expect(episodeAudio.volume).toBe(1);
  expect(previewAudio.volume).toBe(1);
});

test("the user's saved preference reaches the player: normalize_loudness false plays at unity", async () => {
  const { audio } = renderWithApp(<Probe />, {
    routes: { "GET /api/v1/me/preferences": () => ({ body: { language: null, on_open: "resume", normalize_loudness: false } }) },
  });
  await waitFor(() => expect(p).toBeTruthy());
  // Whether the preferences arrive before or after the play, the loaded
  // track ends at unity; without the wiring it would stay at 0.501.
  act(() => p.playList([tr(1, { gain_db: -6 })], 0));
  await waitFor(() => expect(audio.volume).toBe(1));
  expect(p.current?.id).toBe(1);
});
