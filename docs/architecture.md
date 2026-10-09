# Architecture

Melarka is one Go server with an embedded React web app, plus an iPhone app that wraps the web
app and adds a native audio engine. Everything lives in this repository; the code uses the
internal name `lark` throughout (see [AGENTS.md](../AGENTS.md#naming)).

```
          browser / home-screen app                 iPhone app (ios/)
          ┌──────────────────────┐          ┌──────────────────────────────────┐
          │  web app (web/)      │          │  WKWebView: the same web app      │
          │  <audio> playback    │          │      │ window.larkNative bridge   │
          │  service-worker cache│          │  native engine: AVPlayer, cache,  │
          └──────────┬───────────┘          │  queue, lock screen, Shortcuts    │
                     │ HTTPS /api/v1        └──────────────┬───────────────────┘
                     └──────────────┬──────────────────────┘
                          ┌─────────▼─────────┐
                          │  lark server (Go) │── ffmpeg / ffprobe
                          │  SQLite lark.db   │── yt-dlp ── YouTube
                          └─────────┬─────────┘── lyrics, covers, Last.fm
                                    │
                     music libraries · channels · previews
```

## The server

`cmd/lark` is the entry point. It loads the configuration, opens the database, starts the
background workers, and serves the HTTP API and the web app on one port. `lark --version` prints
the version, which is set at build time.

Each package under `internal/` has one job:

| Package | Job |
|---|---|
| `config` | environment variables and `config.yaml`, with defaults and validation |
| `db` | the SQLite database (WAL mode) and its append-only migrations |
| `auth` | users, roles, password hashing, device tokens, sign-in throttling |
| `api` | the HTTP API under `/api/v1`: routing, handlers, errors, admin checks |
| `webui` | serves the embedded web app, and the service-worker kill switch |
| `library` | libraries, scanning and watching folders, the full-text search index |
| `media` | reads tags, durations and embedded lyrics with ffprobe |
| `stream` | delivers a track at a quality tier: the original file, or a cached AAC transcode |
| `personal` | favorites, dislikes, playlists, play history, the saved queue |
| `prefs` | per-user preferences such as language and what happens on start |
| `radio` | picks the next tracks of a user's personal radio |
| `tags` | the tag vocabulary and tags from folders, Last.fm, agents and people |
| `lastfm` | Last.fm tags and similar tracks |
| `lyrics` | lyrics providers, ranking, storage, offsets and wrong-lyrics reports |
| `artwork` | covers from files, folders and online sources, with a resized cache |
| `metareview` | the review list behind the names agent skill |
| `trash` | moves deleted tracks to `.lark-trash/` and purges them after 30 days |
| `ytdlp` | the only place that runs yt-dlp: search, playlists, downloads, self-update |
| `download` | the YouTube download queue |
| `recommend` | the nightly "For you" lists |
| `channels` | following channels: feeds, episode downloads, expiry, suggestions |
| `preview` | temporary previews, video search, related videos and the thumbnail proxy |
| `buildinfo` | the version and the User-Agent |
| `fileutil`, `testutil` | small shared helpers |

Design rules that hold everywhere:

- **Audio files are read-only.** Titles, tags and lyrics are stored as overrides in SQLite.
- **External programs and services sit behind interfaces**, so tests use fakes and recorded
  fixtures and never touch the network.
- **Background work is gentle**: ffmpeg and yt-dlp run niced, with small concurrency limits, and
  people's requests go before background jobs.

## The web app

`web/` is a React app built with Vite (TypeScript, Vitest for unit tests, Playwright for
end-to-end tests). The production build is embedded into the server binary. It talks to the API
with a bearer token or the cookie that sign-in sets, and is translated into English, Simplified
and Traditional Chinese, and Swedish (`web/src/i18n/locales/`).

In a browser, the player uses an `<audio>` element and the Media Session API for lock-screen
controls. A service worker (`sw.js`, built separately) provides the offline cache of favorites.

## The native bridge

When the page runs inside the iPhone app, the app injects `window.larkNative`, an object with a
`version` and a `post(message)` function. The web app checks for it at start
(`web/src/native/bridge.ts`):

- **Without it**, the web app plays audio itself.
- **With it**, the web app creates no audio element. It posts messages such as "play this queue",
  "seek" or "favorite changed" to the native side, and the app sends playback events back to the
  page. The page stays the user interface; the app owns the audio.

The app accepts messages only from the configured server's origin.

## The iOS app

`ios/` is a SwiftUI app generated with XcodeGen from `ios/project.yml` (iOS 17 or later).
`ios/README.md` explains building and testing it.

| Directory | Job |
|---|---|
| `Sources/App` | app entry, the first-launch server address screen, app wiring |
| `Sources/Bridge` | the web view, `window.larkNative`, message decoding, the token store |
| `Sources/API` | a small client for the server's API |
| `Sources/Player` | the playback engine: queue, AVPlayer backend with preloading for gapless play, audio session, lock screen and remote commands, play events, lyric line for the car display |
| `Sources/Cache` | the offline cache, favorites sync and background refresh |
| `Sources/Intents` | App Intents and Shortcuts: shuffle favorites, resume |
| `Sources/Settings` | the native settings screen |

Releases build an unsigned IPA. SideStore signs it on the phone (see [iphone.md](iphone.md)).

## The HTTP API

JSON over HTTP under `/api/v1`. Sign in with `POST /auth/login`, then send the returned token as
`Authorization: Bearer <token>`. Errors look like
`{"error": "<English message>", "code": "<stable_code>"}`. `GET /info` needs no sign-in and
returns the name, the version and a few public settings.

The routes are registered in [`internal/api/server.go`](../internal/api/server.go) and the
`*_handlers.go` files next to it, grouped as: auth and devices, browse and search, streaming and
play events, personal data, tags, lyrics, covers, downloads, recommendations, channels and
episodes, previews, video, and admin (users, libraries, scans, trash, tagging, lyrics and
metadata review, yt-dlp). Admin routes require the admin role. The [agent skills](../skills/)
are worked examples of using the API with `curl`.

## Data on disk

| Path (in the container) | Contents |
|---|---|
| `/data/lark.db` | the database; back it up |
| `/data/cache` | transcodes, covers, yt-dlp's cache; disposable |
| `/data/bin/yt-dlp` | the self-updated yt-dlp, preferred over the bundled one |
| `/data/channels` | channel episodes (`channels.root`) |
| `/data/previews` | previews and video thumbnails (`channels.preview_root`) |
| `/music/...` | libraries; read-only except `.lark-trash/` |
