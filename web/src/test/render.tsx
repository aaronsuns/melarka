import { render } from "@testing-library/react";
import type { ReactElement } from "react";
import { MemoryRouter } from "react-router";
import { AuthProvider } from "../auth/AuthProvider";
import { EpisodesProvider } from "../channels/EpisodesProvider";
import { PreviewProvider } from "../channels/PreviewProvider";
import { DownloadsProvider } from "../downloads/DownloadsProvider";
import { PlayerProvider } from "../player/PlayerProvider";
import { SleepTimerProvider } from "../player/SleepTimerProvider";
import { PrefsProvider, usePrefs } from "../prefs/PrefsProvider";
import type { OnOpen, Role } from "../api/types";
import { FakeAudio, mockFetch } from "./setup";

type MockRoutes = Parameters<typeof mockFetch>[0];

// Like App: the live car-lyrics and loudness preferences reach the player;
// the sleep timer sits inside both players.
function PlayerWithPrefs({ audio, episodeAudio, previewAudio, onOpen, children }: { audio: HTMLAudioElement; episodeAudio: HTMLAudioElement; previewAudio: HTMLAudioElement; onOpen?: OnOpen; children: ReactElement }) {
  const { prefs } = usePrefs();
  return (
    <PlayerProvider audio={audio} userId={1} onOpen={onOpen} carLyrics={prefs.car_lyrics !== false} loudness={prefs.normalize_loudness !== false}>
      <EpisodesProvider audio={episodeAudio}>
        <SleepTimerProvider>
          <DownloadsProvider><PreviewProvider audio={previewAudio}>{children}</PreviewProvider></DownloadsProvider>
        </SleepTimerProvider>
      </EpisodesProvider>
    </PlayerProvider>
  );
}

// A path (with its query) and, for pages opened from a link with state, that state.
function entry(path: string, state: unknown) {
  const [pathname, search = ""] = path.split("?");
  return { pathname, search: search ? `?${search}` : "", state };
}

export function renderWithApp(ui: ReactElement, opts: { role?: Role; routes?: MockRoutes; path?: string; state?: unknown; onOpen?: OnOpen; tweak?: (a: FakeAudio) => void } = {}) {
  const audio = new FakeAudio();
  const episodeAudio = new FakeAudio();
  const previewAudio = new FakeAudio();
  opts.tweak?.(audio);
  const f = mockFetch({
    "GET /api/v1/me": () => ({ body: { id: 1, username: "u", role: opts.role ?? "member" } }),
    "GET /api/v1/me/preferences": () => ({ body: { language: null, on_open: "resume" } }),
    "GET /api/v1/queue": () => ({ body: { queue: { track_ids: [], current_index: 0, position_ms: 0, version: 0, updated_by: "", updated_at: 0 }, tracks: [] } }),
    "GET /api/v1/radio/next": () => ({ body: [] }),
    "PUT /api/v1/queue": () => ({ body: {} }),
    ...opts.routes,
  });
  render(
    <MemoryRouter initialEntries={[entry(opts.path ?? "/", opts.state)]}>
      <AuthProvider>
        <PrefsProvider>
          <PlayerWithPrefs audio={audio as unknown as HTMLAudioElement} episodeAudio={episodeAudio as unknown as HTMLAudioElement} previewAudio={previewAudio as unknown as HTMLAudioElement} onOpen={opts.onOpen}>{ui}</PlayerWithPrefs>
        </PrefsProvider>
      </AuthProvider>
    </MemoryRouter>,
  );
  return { audio, episodeAudio, previewAudio, f };
}
