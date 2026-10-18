# Melarka for iPhone: design

Status: describes the app as built (0.2.15, 2026-10-17). Keep it current: a change to the
behaviour below updates this document in the same commit. The Android app
([android-client-design.md](android-client-design.md)) ports this design, so this document is
also its specification.

Code: `ios/` (Swift, XcodeGen `project.yml`; `Sources/{App,Bridge,Player,Cache,API,Settings,Intents}`,
`Tests/`). User docs: [docs/iphone.md](../../iphone.md).

## Intent

The web app is Melarka's one UI. The iPhone app is a native shell around it that adds what a web
page can't do reliably on iOS: playback in the background, on the lock screen and in the car;
an offline cache of favorites; starting playback from the car without opening the app.

## Shape

```
 SwiftUI app
 ├─ ServerURLView (first run)            AppServices (process-wide, built at launch)
 ├─ RootView                             ├─ AuthStore (token in the Keychain)
 │   ├─ WebShell: WKWebView, the web app ├─ LarkAPI (Bearer)
 │   │    window.larkNative ── bridge ──►├─ PlayerEngine ── AVPlayerBackend (AVQueuePlayer)
 │   │    "lark-native" events ◄──────── │      NowPlayingController, RemoteCommands,
 │   ├─ can't-connect screen             │      AudioSessionController, CarLyrics
 │   └─ OfflinePlayerView                ├─ QueueStore, PlayEventQueue, QueueSync
 └─ SettingsView                         ├─ UserCache → CacheStore (per server, per user), FavoritesSync
                                          └─ NetworkStatus (+ Stay Offline switch)
```

The engine is built at launch, independent of the UI, so remote commands work in a background
launch with no window (the car).

## Bridge

- **Injection**: a document-start user script, main frame only, idempotent:
  `window.larkNative = Object.freeze({version:1, platform:"ios", post(msg){ webkit.messageHandlers.lark.postMessage(msg) }})`.
  Handler name `lark`. Messages are accepted only from the main frame and the server's origin
  (scheme, host, effective port). Undecodable messages are dropped silently; bodies are never
  logged. (`Bridge/BridgeController.swift`)
- **Native → web**: `window.dispatchEvent(new CustomEvent("lark-native",{detail:<json>}))`, with
  U+2028, U+2029 and `</` escaped and non-finite numbers sent as strings
  (`Bridge/NativeEvent.swift`). Nothing is sent while the scene is inactive; on activation
  `engine.emitAll()` re-sends both queues and states.
- **Web detection**: `hasNative()` is `!!window.larkNative`; then the web uses
  `NativePlayerProvider`, turns its own offline cache and service worker off, and Settings shows
  the native-settings button (`web/src/native/bridge.ts`).
- **Web view**: inline media, no gesture needed for playback, default data store, UA suffix
  `LarkiOS/<version>` (unused).

### Web → native messages (`Bridge/WebMessage.swift`, web `ToNative` in `bridge.ts`)

| type | fields | behaviour |
|---|---|---|
| `hello` | `onOpen` resume / shuffle_favorites / nothing | `emitAll()`; only the first hello after launch or reset acts on `onOpen` |
| `setQueue` | `kind, items[], index, positionMs?, play, source` | no `positionMs` and same current id: an edit, playback continues; otherwise a new queue; empty items stop that kind |
| `play` / `next` / `prev` | `kind` | on the active kind; on the other kind `play` switches, `next`/`prev` only move its index |
| `pause` | `kind?` | absent or the active kind |
| `seek` / `skip` | `kind, ms` (rounded; `skip` may be negative) | the inactive kind only stores the position |
| `setRate` | `rate` | episodes only |
| `stop` | `kind` | clears that queue; stopping episodes makes music active |
| `setPrefs` | `quality, carLyrics, loudness?` | persisted for cold starts |
| `auth` | `signedIn, userId?` (a `token` field is ignored) | sign-in: switch user, harvest the cookie, sync favorites; sign-out |
| `pauseForWeb` | — | a web preview or video took the session |
| `favoriteChanged` | `trackId, on` | cache flag + favorites sync |
| `lyricsChanged` | `trackId` | refetch car lyrics for the current track |
| `flushEvents` | `id` | end the listen, POST pending events, always answer `flushed{id}` |
| `openSettings` | — | native settings |
| `setModes` | `shuffle, repeat` off/all/one | music only |
| `sleepTimer` | `{minutes 1..1440}` / `{endOfTrack:true}` / `{cancel:true}` | `cancel` wins, then `endOfTrack` |

Items: `{kind, id, title, artist, album, durationMs, meta}`; only `kind` and `id` are required;
`meta` is the web's Track or Episode JSON, echoed back unchanged. Ids: a track's number, or
`yt:<video id>` for a YouTube track (streamed from its preview; never cached, no lyrics, no play
events, not in the server queue); an episode's video id. Queue sources: list, radio, restored,
shuffle, favorites.

### Native → web events

- `state`: `{kind, itemId|null, index, playing, positionMs, durationMs, buffering, error|null,
  rate, shuffle, repeat, sleepRemainingMs|null}`; the nulls are explicit; only the active kind can
  be playing, buffering or in error; on a kind switch the outgoing kind's state goes first.
- `queue`: `{kind, items, index, source}`. `flushed`: `{id}`. `authRequired`. `notice`: `{text}`.
- Compatibility: unknown types are dropped, unknown fields ignored, missing required fields drop
  the message. The web detects features by key presence (`shuffle`/`repeat`, `sleepRemainingMs`).

### Navigation

Same-origin http(s) in the web view; other origins and app schemes open outside; `data:` and
`javascript:` are cancelled in the main frame; subframes (the YouTube embed) may load web URLs;
a main-frame 5xx is a load failure; a web-content crash reloads; JS alerts only for the server's
frames. (`Bridge/WebShell.swift`, `Bridge/PageLoad.swift`)

## Server, sign-in and users

- **Server screen**: https only (http for `localhost` and `*.local`), no userinfo, normalised to
  `scheme://host[:port]/`, checked with `GET /api/v1/info` (10 s).
- **Token**: the server's HttpOnly `lark_token` cookie, harvested from the web view's cookie store
  on every cookie change and on `auth{signedIn:true}`: name, HttpOnly, host domain, Path=/ preferred;
  never re-adopts the last revoked value; a missing cookie never signs out. Stored in the Keychain
  (`AfterFirstUnlock`, so the car works while the phone is locked). Never logged, in a URL or on
  the bridge.
- **401**: signs out only if the request carried the current token; concurrent 401s share one
  sign-out.
- **Sign-out**: flush the listen and events (≤ 5 s), reset the engine (queues and events wiped),
  detach the cache (files kept for that user), clear the token, send `authRequired`, delete the
  cookie.
- **Users**: per-user queues (`queues-<uid>.json`), events and cache; a different user signing in
  resets the engine and restores that user's state.
- **Server change** (Settings): sign out, drop the engine and web view, forget the server and its
  web data; other servers' cache folders are pruned when a new server is set.

## Player engine (`Player/PlayerEngine.swift`)

- **Queues**: music and episodes, one active kind, one backend. An episode resumes at its saved
  position (0 if played, unstarted or within 30 s of the end). Prev restarts after 3 s.
- **Gapless**: the backend holds the current item and one preloaded next item (repeat one: the
  current; repeat all at the end: the first; end-of-track sleep: none; failed and, offline,
  uncached items skipped). A generation counter drops stale callbacks.
- **Lookahead**: once the current track sounds, the next 2 tracks go to the cache.
- **Network failures**: retry the same item at its position after 2, 5, 10 s. Episodes that fail
  save their position and hand over to music. Music offline: a cached item plays and the
  interrupted one is requeued; nothing cached: stop and resume on reconnect. Otherwise the item is
  failed for 30 min and the engine advances.
- **Stall**: a remote track buffering 6 s while playing switches to a cached one (notice
  "Network too slow…") and requeues the stalled track.
- **Never stop**: failed items skipped; local-only mode offline or after 3 failures (on-device
  items, else a random cached favorite not among the last 30 played); refill favorites (20),
  shuffle (20) and radio (10) queues when 2 or fewer playable items are left, excluding up to 300
  recent ids, never under repeat all; offline resume modes replay / advance / shuffle.
- **Modes**: shuffle reorders the upcoming items only and remembers the original order (fixtures
  shared with the web's `modes.ts`); repeat all starts a new pass (reshuffled, never starting with
  the track that just ended); repeat one replays a track that ended by itself.
- **Sleep timer**: minutes (fade the master volume over the last 10 s, pause, restore) or end of
  track (no preload; fade on the remaining time); `sleepRemainingMs` in every state.
- **Loudness**: per-item attenuation `10^(gain_db/20)` (music, `meta.gain_db`), as an audio mix on
  the item; an attenuated item never sounds before its mix is on. Master volume is reserved for
  the sleep fade.
- **Rate**: episodes only, pitch preserved.
- **Episode progress**: `PUT /episodes/{id}/progress` every 15 s while playing and on pause,
  switch, next, prev, end (`played:true` at the end); patched into queued copies' `meta`.
- **Play events**: real playback seconds; ≥ 1 s; `skipped` before 30 s on a user skip; persisted
  per user (≤ 2000); `POST /events/play` after each event when online and every 30 s.
- **Queue sync**: music only, `PUT /queue {track_ids, current_index, position_ms}` 1 s after an
  edit and every 15 s while playing; offline skipped and sent on reconnect; never while the queue
  holds a `yt:` track; the server queue is adopted on the first `hello` with `resume` when local
  is empty or the server's is newer than 5 s and from another device.
- **Queue persistence**: a versioned snapshot (both queues, active kind, modes, savedAt,
  ownDevice) written on every change and every 10 s while playing.
- **Now Playing**: title, artist, album, elapsed, rate (0 while paused or buffering), duration;
  written only when something shown changes; artwork from the cache, then the server
  (`/tracks/{id}/cover`, `/episodes/{id}/thumbnail`, `/videos/{v}/thumbnail` for `yt:` tracks),
  LRU of 20, 404 remembered, errors backed off.
- **Car lyrics**: synced, non-instrumental lyrics only; the line whose `t_ms + offset_ms` has
  passed becomes the title and "song · artist" the artist; blank lines show the song; fetched
  per track (cache first, fresh online), refetched on `lyricsChanged`.
- **Remote commands**: play, pause, toggle, next, previous, position; ±30/15 s for episodes;
  shuffle and repeat for music.
- **Audio session**: playback category, activated only right before sound starts; interruptions
  pause and resume when allowed; an unplugged route pauses; a media-services reset rebuilds the
  player at its position.
- **Cold start from the car**: a play with nothing loaded resumes from disk synchronously (the
  saved music queue, or cached favorites shuffled), and only then reaches for the server.

## Offline cache (`Cache/`)

- Per server and user under `Library/Caches/lark/<serverKey>/<uid>/`: audio at quality high,
  covers, lyrics (LyricsDoc JSON) and an index; empty cover/lyrics files mark "none" for 7 days;
  staging wiped at launch; purges tolerated.
- Cap 1/2/5/10 GB (default 2); evicts non-favorites by LRU first, then favorites; a download needs
  500 MB free; out of space stops the sync.
- Up to 2 downloads at once, deduplicated per track.
- **Favorites sync** (Wi-Fi only: online, not expensive, not Low Data): page through favorites,
  set flags, refresh metadata, download missing favorites up to 90 % of the cap, backfill covers and
  lyrics. Triggers: launch, activation (hourly), sign-in, `favoriteChanged`, Wi-Fi back, Settings,
  background refresh (BGAppRefreshTask, every 6 h).

## Native screens

- **Can't connect**: Retry, Settings, Play Offline Favorites (when cached), Stay Offline; the page
  retries by itself when the network comes back.
- **Stay Offline**: the network counts as offline whatever it is; the page isn't loaded; the
  offline player opens; turning it off resumes everything that waited.
- **Offline player**: header (state, Stay Offline, Retry, Close), now-playing card (cover or drawn
  tile, colour wash, lyrics preview and full lyrics sheet, slider, transport with shuffle and
  repeat), Up Next, Offline Favorites (a tap queues them as `favorites`). It talks to the engine
  only through the same messages and events.
- **Settings**: server (change, with confirm), offline cache (usage, cap, sync now, clear), Stay
  Offline, car autoplay guide and test, about.
- **Shortcuts**: "Shuffle favorites in Melarka" and "Resume in Melarka" (App Intents, no app
  window), used by the user's "Bluetooth connected" automation.

## Build and release

- XcodeGen; `MARKETING_VERSION` bumped by hand per release; the release sets both versions to the
  tag. App and server share one version.
- CI `ios` job (macOS, newest Xcode, newest iPhone simulator): `xcodebuild test`.
- Release: an unsigned IPA (`Melarka-X.Y.Z.ipa`) on the GitHub Release; SideStore source
  (`source.json`) on GitHub Pages via `ios/scripts/make_source.py`.
- Local test builds over the installed app: `ios/scripts/remote.sh` and the private Mac.

## Tests

About 600 cases in `ios/Tests/` grouped as: bridge and protocol (WebMessage, BridgeWebView,
Smoke); auth and wiring (AuthStore, AppServices, ServerURL, StayOffline, SettingsModel,
Localization); API (LarkAPI); engine (PlayerEngine, EngineRobustness, NeverStop, ReconnectEngine,
CarColdStart, PlayModes, SleepTimer, Gain, EpisodeProgress, Gapless, Lookahead); system
(AVPlayerBackend, AudioSession, NowPlaying, RemoteCommandsModes, CarLyrics); sync and persistence
(QueueSync, QueueStore, PlayEvent); cache (CacheStore, CacheWiring, FavoritesSync, OfflineLyrics);
offline UI (OfflinePlayer); intents (Intent). Fakes: backend, API, cache, network, scheduler. The
web's `nativeTransport.test.tsx` and `nativeSwitch.test.tsx` pin the bridge from the web side.
