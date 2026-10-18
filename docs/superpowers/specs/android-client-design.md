# Melarka for Android: design

Status: approved by the owner (2026-10-18). Once built, this document describes the Android app as it
is; a change to its behaviour updates it in the same commit. Its specification is the iPhone
design, [ios-client-design.md](ios-client-design.md).

## Intent

Melarka already has a web app and an iPhone app. The iPhone app is a native shell: the web app
runs inside a web view and a native engine plays the audio, so music keeps playing in the
background, on the lock screen and in the car, and favorites play offline. Android users today
only have the web app (Add to Home Screen), which stops when the screen locks and has no offline
favorites worth the name.

Melarka is open source and meant for anyone who runs their own Melarka server. The Android app
is for all of them, not just one family. It should feel like the iPhone app on Android: the same
pages (they come from the server), the same reliable playback.

## Goals

1. **Feature parity with the iPhone app, in the first release.** The owner asked for "everything"
   the iPhone app does. The parity list is [ios-client-design.md](ios-client-design.md); each item
   there is a goal here.
2. **One web app for both platforms.** The Android shell loads the server's web app exactly as the
   iPhone app does and speaks the same bridge protocol (`window.larkNative`, `lark-native`
   events), so web improvements reach Android without Android changes.
3. **Reliable playback** on Android: background, lock screen, notification, Bluetooth (AVRCP)
   with the current lyric line as the title, headphone unplug, phone calls, offline favorites.
4. **Easy to install for anyone**: a signed APK on every GitHub Release from the first version;
   F-Droid afterwards.
5. **Tested without an Android phone**: unit tests plus an emulator run, locally on the owner's
   private Mac and in CI.

## Non-goals

- **Google Play.** Play's policy forbids apps that download YouTube content, and Melarka's
  YouTube search, previews, downloads and channels are part of the app. Not attempted.
- **Android Auto (the car's own Melarka screen with browsing).** The iPhone app has no CarPlay
  screen either; it shows Now Playing through the system. Bluetooth (AVRCP) metadata and controls
  give parity. Android Auto can come later.
- **A native Android UI for the music pages.** Everything but the offline player, the server
  screen, the error screen and native settings is the web app.
- **Tablets, foldables, TV, Wear OS** beyond "the phone layout works".
- **Server changes**, except small additive ones the web needs (see "Web side").

## Anti-goals (a failure even if it works)

- **Playback that stops** when the screen locks, the app is swiped away from recents while playing,
  or the system kills the web view. Music must be owned by a foreground media service, not the
  web view.
- **A second code path for the pages.** Re-implementing music pages natively would split the
  product; the web app stays the one UI.
- **Leaking the session token**: never in a URL, a log, the bridge, or a world-readable file.
- **An APK that can't be updated**: the release signing key must never be lost or rotated by
  accident. Losing it means every user has to uninstall to update.
- **Diverging protocol**: the Android bridge must emit exactly the iOS event shapes (explicit
  `null`s included), so the web's feature detection keeps working.

## Constraints

- **Development host: the owner's private Mac** (`aaron@192.168.31.71`, Apple silicon, 16 GB,
  ~140 GB free), reached over SSH like the iOS builds. It may be asleep; work that needs it waits.
  Install there: JDK 17, Android command-line tools, platform 35, build tools, an arm64 emulator
  system image. Gradle runs there (or in CI); the work Mac only edits the code.
- **No physical Android device.** Emulator only; Bluetooth/car behaviour is verified by first
  users (the owner's classmates) after release.
- **Repo conventions** (AGENTS.md): Melarka in user-visible text, `lark` in code identifiers;
  tests never touch the network; English, 简体中文, 繁體中文, svenska for every user-visible string;
  `type(scope): summary` commits; app and server share one version number.
- **Same server API** as the iPhone app; the token travels as `Authorization: Bearer`.

## Decisions taken with the owner

- Open source, for every Melarka user.
- First release has everything the iPhone app has.
- Tested on emulators; then released; classmates test on real phones afterwards.
- Distribution: signed APK on GitHub Releases first; F-Droid as the "proper store"; no Google Play.
- Android tooling lives on the private Mac.
- **Car autoplay through a companion device.** The user picks the car in Melarka's settings; Android's
  CompanionDeviceManager shows its own pairing confirmation once; from then on the car's Bluetooth
  connecting starts "Shuffle favorites" even while Melarka is in the background (the companion
  association, with its install-time permission, is what lets an app start playback from the background on Android 12+). It does
  nothing if music is already playing, like the iOS intent. Documented in `docs/android.md` and in
  the setup screen's text.
- **Standard Android first.** Target stock Android and the phones used outside China (where
  YouTube works): Pixel, Samsung, Motorola, Nokia, OnePlus and the like. No special builds for
  Huawei/HarmonyOS (HarmonyOS NEXT can't install APKs at all) or other vendor forks. Where a
  vendor's power management can stop playback (Xiaomi, Samsung, OnePlus, …), there is no code
  workaround beyond asking the system: the app shows a short, plain guide ("If music stops when the
  screen is off…") with a button that opens the battery-optimisation exemption request and the
  vendor's own settings where Android allows it, and `docs/android.md` explains the per-vendor
  steps (linking dontkillmyapp.com).
- **Signing key backups**: the keystore is generated on the private Mac (never on the work Mac, which
  is the owner's employer's machine and holds nothing private). The keystore file lives in the
  GitHub Actions secret, on the private Mac (outside the repo) and in the owner's email; its
  passwords in the GitHub secret and Apple's Passwords app (entered on the private Mac) — never the
  file and the password in the same place outside GitHub.

## The builder's calls (made here; the owner can override)

- **Language and stack**: Kotlin; AndroidX; Media3 (ExoPlayer, MediaSessionService,
  MediaSession); Android WebView (AndroidX WebKit for `addWebMessageListener` and
  `addDocumentStartJavaScript`); WorkManager; OkHttp for API calls and (through Media3's
  `datasource-okhttp`) for streams; kotlinx.serialization for JSON; JUnit + Robolectric for unit
  tests; an instrumented smoke test on the emulator.
- **minSdk 26 (Android 8.0), targetSdk/compileSdk 35.** Covers practically every phone in use and
  keeps foreground-service and notification-channel code single-path. Media3 is pinned to 1.9.4,
  the newest release that compiles against SDK 35 (1.10 and later need compileSdk 36 and a newer
  AGP); raise both together when the build moves to SDK 36.
- **Package name** `io.github.aaronsuns.melarka` (matches the iOS bundle id); app name "Melarka";
  the icon from `docs/brand`.
- **Project location**: `android/` at the repo root, Gradle (Kotlin DSL) with a version catalog.
- **Version**: `versionName` = the release tag (X.Y.Z); `versionCode` = X×10000 + Y×100 + Z
  (monotonic, readable). The source of truth is the same hand-bumped version as `ios/project.yml`.
- **Signing** (as built): one release keystore (PKCS12, alias `melarka`, RSA 4096), generated once on the
  owner's private Mac. Its certificate SHA-256 is public in `android/signing-cert.sha256` and in `docs/android.md`.
  The keystore and its passwords exist only in the owner's keeping (a backup outside the repo) and as four GitHub
  Actions secrets: `ANDROID_KEYSTORE_B64`, `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS`, `ANDROID_KEY_PASSWORD`.
  `app/build.gradle.kts` reads signing only from the environment (`ANDROID_KEYSTORE_FILE` plus those three); without
  `ANDROID_KEYSTORE_FILE` the release build is unsigned and never uses the debug key.
- **Release job** (as built): the `apk` job in `release.yml` runs in the GitHub environment `release`, where the
  four secrets live as environment secrets (not repository secrets). It checks all four are non-empty, decodes the
  keystore to a file under `$RUNNER_TEMP`, builds `assembleRelease --no-daemon -PlarkVersion=<tag>` (no Gradle daemon
  keeps the passwords), removes the file (an `if: always()` step; the build step's trap too), then fails unless
  `apksigner verify --print-certs` shows exactly one signer whose SHA-256 is the one in `android/signing-cert.sha256`
  and `aapt2 dump badging` shows package `io.github.aaronsuns.melarka`, the tag as versionName, X*10000+Y*100+Z as
  versionCode and the label Melarka. The `release` job attaches `Melarka-X.Y.Z.apk` next to the IPA, and only after
  the gate, the image, the IPA and the APK all succeeded. A manual run (`workflow_dispatch`, no inputs) runs only the
  `apk` job on the version in `ios/project.yml`, keeps the APK as a workflow artifact and never publishes.
  **Owner's one-time setup**: create the environment `release` (Settings > Environments) with deployment branches
  and tags limited to the tag pattern `v*` and the branch `main` (so a manual run works from `main` only), then, on
  the private Mac:
  `gh secret set --env release ANDROID_KEYSTORE_B64 < <(base64 -i ~/melarka-release/melarka-release.jks)`, and
  `gh secret set --env release ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS` (`melarka`) and
  `ANDROID_KEY_PASSWORD` (prompted, never on a command line), and delete any repository-level copies.
  Verified by hand on the build Mac with the real key: the signed APK matches the fingerprint and badging. The
  release APK was installed on the emulator (no debug build was installed, so no uninstall was needed), launched to
  the server screen and connected to a demo server by `http://localhost:4700` through `adb reverse`, because release
  builds refuse cleartext to the emulator host `10.0.2.2` (debug only).
- **Store metadata** (as built): `android/fastlane/metadata/android/{en-US,zh-CN,zh-TW,sv-SE}` with title, short and
  full description, `changelogs/<versionCode>.txt` (the next release's code) and five phone screenshots (under en-US only; F-Droid falls back to it for the other locales).
- **F-Droid**: prepare fastlane metadata (`android/fastlane/metadata/android/…`: title,
  descriptions in four languages, screenshots, changelogs) and submit the merge request to
  fdroiddata after the first GitHub release works. Aim for a **reproducible build signed with our
  key** (F-Droid then ships our APK, so GitHub and F-Droid installs update each other): pinned JDK
  and build tools, no build timestamps or git-derived values in the APK, `AllowedAPKSigningKeys`
  in the F-Droid metadata. If F-Droid can't reproduce it, fall back to F-Droid's own signature and
  say in the docs that the two installs can't replace each other. The "NonFreeNet" anti-feature
  (YouTube) is declared; that is acceptable.
- **Shortcuts instead of Siri**: launcher shortcuts "Shuffle favorites" and "Resume" (the iOS
  ResumeIntent: resume, or shuffle favorites when the queue is empty), both running without
  showing the page. Voice phrases are dropped (Google Assistant App Actions need Google Play).
- **Updates**: users update by installing the newer APK over the old one, through Obtainium
  (pointed at the GitHub repo) or F-Droid. No SideStore-style source is needed.
- **User-installed CA certificates are trusted** (`<certificates src="user"/>` in the network
  security config), as the iPhone app trusts a root the user installed: self-hosters behind a
  private CA or a LAN proxy must be able to connect. System CAs stay trusted; nothing else is.

## How it works

### Shape

```
 Activity (single)                         MediaSessionService (foreground while playing)
 ├─ WebView: the server's web app          ├─ MediaSession (lock screen, notification, AVRCP,
 │    window.larkNative  ── bridge ──────► │   car lyrics line as title, playback resumption)
 │    "lark-native" events ◄────────────── └─ shows PlaybackHost (process-wide):
 ├─ Server URL screen (native)                ExoPlayer (gapless playlist, per-item gain),
 ├─ Can't-connect screen (native)             PlayerEngine (port of ios/Sources/Player)
 ├─ Offline player (native, Compose)
 └─ Settings (native, Compose)           AppServices: API client, AuthStore, UserCache,
                                           QueueStore, PlayEventQueue, QueueSync, FavoritesSync
```

The engine and everything it needs live outside the Activity and the service (in the
process-wide `PlaybackHost` and the application-scoped `AppServices`); the media service only
shows them, so playback, the car and lock-screen controls work with no UI, after the Activity is
destroyed, and on a cold start from a Bluetooth play button. The stream `OkHttpClient` comes from
`Playback.httpClient`, which `AppServices` sets to the client that trusts user-installed CAs.

### Port, don't redesign

The iOS engine is the specification: `ios/Sources/Player`, `Cache`, `API`, `Bridge`, and their
tests (about 500 test methods across 36 files). Port the logic module by module with the same
behaviour and the same test cases (re-expressed in Kotlin, with the same fakes: backend, API,
cache, network, scheduler). Where Android needs a different mechanism, keep the behaviour and
change only the mechanism (table below). [ios-client-design.md](ios-client-design.md) lists every
behaviour; the builder works through it as a checklist.

### Bridge

- A document-start script defines `window.larkNative = {version:1, platform:"android",
  post(msg)}` exactly as iOS does, with `post` sending `JSON.stringify(msg)` over
  `WebViewCompat.addWebMessageListener("lark", allowedOrigins=[server origin])`. Messages are
  accepted only from the main frame and the server's exact origin (scheme, host, port), checked again
  in `BridgeController.onMessage`; anything else is dropped silently. The script defines
  `larkNative` once, in the top frame only, non-writable and frozen.
- Before decoding, a message body over 8 M characters or nested deeper than 32 objects/arrays is
  dropped (a 5,000-track `setQueue`, every item with its track JSON, is over 2 M characters, so a
  64 K cap would break real queues).
- Native → web: `evaluateJavascript` of `window.dispatchEvent(new CustomEvent("lark-native",
  {detail:…}))`, with the iOS escaping (U+2028/U+2029, `</`, non-finite numbers as strings) and the
  same event shapes: `state`, `queue`, `flushed`, `authRequired`, `notice`.
- Every web→native message type and rule from `ios/Sources/Bridge/WebMessage.swift` (unknown types
  dropped, unknown fields ignored, required fields enforced, lenient numbers).
- Events are not sent while the Activity is stopped; on resume, `emitAll()` re-sends both queues
  and states (as iOS on scene activation).
- Navigation policy as iOS (`BridgeController`/`WebShell`): same-origin in the web view, other web
  origins open in the browser, `data:`/`javascript:` blocked in the main frame, the YouTube embed
  allowed in subframes, a main-frame 5xx counts as a load failure (`net::ERR_ABORTED`, a load
  replaced by another, does not), a renderer crash gets a new web view on the server's page.
  `target=_blank`/`window.open` reach `onCreateWindow`; the new window's first URL is caught in a
  throwaway web view and decided as a new window (destroyed after 10 s if it never navigates, on
  `window.close()`, or with the shell). Local schemes (`file:`, `content:`, `chrome:`,
  `chrome-extension:`, `android.resource:`, `view-source:`, `filesystem:`) are cancelled in every frame.
  Opening outside is always a plain `ACTION_VIEW` + `CATEGORY_BROWSABLE` of the URL (an `intent:` URL is
  never parsed into an Intent), and nothing it throws escapes. Events are dispatched only while the web
  view shows the server's own origin. Settings: no file or content access (file-URL access off too), no
  mixed content, Safe Browsing on, new windows only on a tap. `alert`/`confirm` show native dialogs only
  for the server's own frames and are answered (false) if the Activity goes; `prompt` is refused. The user agent gains `LarkAndroid/<version>` (unused, as
  iOS's `LarkiOS/<version>`).
- The web view belongs to the Activity (made in `onCreate`, destroyed in `onDestroy`; the Activity is
  not recreated on rotation or resizing), unlike iOS, where the app services keep the `WKWebView`:
  an app-scoped web view would hold an Activity context. The bridge and everything behind it are the
  app's; the page reloads only when the Activity is recreated. Edge to edge (Android 15), the page is
  padded inside the system bars, the cutout and the keyboard.

### Auth

- Sign-in happens in the web page; the server sets the HttpOnly `lark_token` cookie. Native reads
  it with `CookieManager.getCookie(serverURL)` after page loads and on `auth{signedIn:true}`.
- Android returns cookie values without attributes, so the iOS HttpOnly/Path checks can't be
  copied. **The builder's call:** accept the cookie by name for the server URL, never re-adopt the
  last revoked value, and verify the token with `GET /api/v1/me` (or an equivalent authenticated
  call) before storing it.
- Harvesting is a suspend call (`AuthStore.harvest(serverUrl, userId)`): a cookie that is new and not the
  revoked value is sent to `GET /api/v1/me` as a Bearer token; only a 200 stores it (the user id comes from the
  answer), a 401, an error or an unreadable answer stores nothing. With the name twice in the jar, the last
  (shortest Path, the server's `Path=/`) wins. A missing or empty cookie never signs out.
- Accepted residual risk: Android's `CookieManager` hides HttpOnly and Path, so native adopts the last `lark_token`
  cookie and relies on the `/me` check; a same-origin script could plant its own valid token (this needs XSS on
  the user's own server).
- Server URL: https only; plain http for `localhost`, `*.local`, and `10.0.2.2` (the emulator's host) in debug
  builds only; no user info; normalised to `scheme://host[:port]/`.
- Store the token and user id with the Android Keystore (an AES key in the Keystore encrypting a
  small file in credential-encrypted storage), not in plain SharedPreferences.
- Sign-out, user switching, server change and 401 handling exactly as `AppServices.swift`.

### App services

- `AppServices` (port of iOS `AppServices.swift`) is application-scoped, built by `LarkApplication` on first
  use (the Activity, the media service's first start, or the background work), and runs on the main thread:
  auth, the API (its 401 hook clears the token at once, then runs the rest of sign-out on the main thread),
  the engine (handed to the process's `PlaybackHost`), the signed-in user's cache, the network (behind the
  Stay Offline switch) and the settings (server, cache cap, the web's last `setPrefs`, Stay Offline) in
  `SharedPreferences`. The Activity only shows it.
- Sign-out: flush (≤ 5 s, real time), reset, cache set aside, token cleared, background sync cancelled,
  `authRequired`, session cookie deleted (written again expired through `CookieManager`, for the host and
  for its domain). A user switch keeps the previous user's cache folder for their next sign-in (as iOS);
  leaving a server signs out, drops the engine (`detach()`: its network observer goes inert), deletes the
  server's cookies and `WebStorage` origin (the HTTP cache goes with the web view), and the next server's
  `updateCache` prunes other servers' folders.
- **Starting playback in the background**: when the network returns for a player stopped offline, the engine
  asks `canAutoStart` first. In the foreground, or with the media service running (its notification is up),
  it plays at once. In the background with no service, Android 8+ refuses to start the service, so the
  engine does not try: it stays paused by the system (no error shown) and resumes when the Activity starts
  again, or on a play (`AppServices.mayStartPlayback`; unit-tested: the emulator cannot stop the media service while
  the engine waits for the network, so Task 14 could not reproduce it there). Car autoplay is the one
  background start that asks for the foreground itself (the companion exemption, below).
- The background favorites sync (`FavoritesRefresh`) is scheduled after a sign-in and cancelled on sign-out;
  its `provider` is the services' `syncRun()` (null while signed out).

### Playback (Media3)

| iOS mechanism | Android mechanism |
|---|---|
| AVQueuePlayer + preload | ExoPlayer playlist (current + next MediaItem; gapless built in) |
| AVAudioMix per-item gain | decided: a custom `AudioProcessor` (`GainProcessor`, first in the sink's chain). Each `MediaItem` carries its factor as a `GainCell` tag; a custom audio renderer resolves the item of each stream from the timeline and switches the processor in `onOutputStreamOffsetUsChanged`, i.e. on the audio timeline at the item boundary (after the last buffer of the old item, before the first of the new), not on the main-thread transition callback. Nothing to wait for: an item never sounds before its gain is on. `player.volume` is the master volume, reserved for the sleep fade |
| Bearer header | decided: `OkHttpDataSource.Factory(client).setDefaultRequestProperties` built per item from `MediaSource.Remote(uri, token)`; the token is in no URL, `MediaItem`, tag or log, and a stream's `MediaSource` is built when the item is loaded or preloaded, with the token current then |
| Pitch-preserving rate (episodes) | `PlaybackParameters(speed, 1f)` (Sonic keeps pitch) |
| `rebuild()` after a media services reset | none: Android has no such reset; a dead audio output surfaces as a playback error (`backendFailed`) and the engine's retry loads again |
| `@MainActor`, `Task`, `TaskBag` | decided: the engine and everything it owns run on the main thread; async work is coroutines in one application-scoped `CoroutineScope` on `Dispatchers.Main` (passed in, so tests run it on a test dispatcher), the engine's own jobs in a `TaskBag` that sign-out cancels; timers on the `Scheduler`. `PlayerEngine.mediaServicesReset` is not ported (see the row above) |
| `String(localized:)` engine notices | `EngineTexts.from(context)`: the engine's notices and errors from string resources in the four locales (the iOS wording; Swedish added) |
| `CacheProviding.localURL` | `CacheProviding.localFile(trackId): File?` (the player is handed a file); the rest of the protocol keeps the iOS names |
| AVAudioSession interruptions | decided: audio focus handled by the player (`setAudioAttributes(…, handleAudioFocus=true)`): a transient loss (a call) pauses (`backendPaused`, a system pause) and ExoPlayer resumes by itself when it ends, the session meanwhile showing it as meant to play (suppressed) so the service stays in the foreground; a permanent loss is the user's pause (`backendPausedForGood`, as iOS's interruption end without `.shouldResume`); a pause by the user in between is not undone. `USAGE_MEDIA` always; the content type follows the active kind (`PlaybackHost`): `CONTENT_TYPE_MUSIC` for music (ducks under a navigation prompt), `CONTENT_TYPE_SPEECH` for episodes (pauses instead of ducking) |
| AVAudioSession activation (`willStartPlayback`) | decided: the engine's `willStartPlayback` starts the media service (`PlaybackService.start`) right before sound starts, so playback the page starts has its service and notification from the first second; launching the app or building the session never takes audio focus |
| route `oldDeviceUnavailable` | decided: `setHandleAudioBecomingNoisy(true)`; the backend reports "becoming noisy" as `backendPausedForGood`, which the engine takes as the user's pause (as iOS: `pausedBySystem` cleared, nothing resumes it) |
| AVPlayer's own wake behaviour | decided: wake mode per source, set by each `load`: `WAKE_MODE_NETWORK` (CPU and Wi-Fi) only while a stream is loaded, `WAKE_MODE_LOCAL` for a file on the phone |
| (no iOS equivalent) encoded passthrough | decided: disabled (the sink's output provider has no context, so it assumes PCM-only capabilities): an AC-3/E-AC-3 item is decoded like any other and never bypasses its gain |
| MPNowPlayingInfo + remote commands | decided: a Media3 `MediaSessionService` (`PlaybackService`) whose session player is `SessionPlayer`, a `ForwardingSimpleBasePlayer` over the app-wide ExoPlayer that shows the engine's view: one item (the current one, from `NowPlayingController`), the engine's position and `playing`, no player errors (the engine handles them). Its commands go to `RemoteCommands` (port of iOS `RemoteCommands`): play/pause, seek, next, previous always; seek back 15 s / forward 30 s for episodes; shuffle and repeat for music; never set items, volume (the sleep fade's) or speed. The notification's own buttons are custom commands: `SKIP_BACK_15`/`SKIP_FORWARD_30` in place of previous/next for episodes, `SHUFFLE`/`REPEAT` (showing their state) for music. Releasing the session player only detaches it: the ExoPlayer and the engine live in the process-wide `PlaybackHost`, so the service ending (swiped away while paused) stops nothing |
| MPMediaItemArtwork | decided: `MediaMetadata.artworkData` (bytes from `LarkApiProtocol.artwork`, the token only in its header), never an artwork URI; kept for the last 20 items, the same array on every write, "no cover" (404) remembered, a failed fetch asked again after 30 s doubling up to 10 min |
| car lyrics as title | the same: the current line as the MediaSession title, "song · artist" as the artist, updated only when the line changes (the session item's own metadata keeps the song, so only the shown metadata changes) |
| cold start from the car (remote play) | decided: a session play with nothing current reaches `RemoteCommands.handlePlay` (the iOS steps: play, else `resumeFromDisk`, else prepare and shuffle favorites); the session player takes no media items, so Media3 never asks for resumption on a play. `MediaSession.Callback.onPlaybackResumption` answers at once from disk: for a play the host's hook (`resumeFromDisk`, no network; with no engine yet, the services build it first, as for a command), then the engine's active queue as media items with its index and position. `MediaButtonReceiver` in the manifest; the start's foreground rule: see "Cold start, shortcuts and car autoplay" |
| Shortcuts "car connects → shuffle favorites" | decided: car autoplay through a CompanionDeviceManager association (below); launcher shortcuts "Shuffle Favorites" and "Resume" (`res/xml/shortcuts.xml` → `ShortcutActivity`); both run `IntentRun`, the port of iOS `Intents/*` |
| BGAppRefreshTask | decided: WorkManager periodic work (`FavoritesRefresh`, unique name `io.github.aaronsuns.melarka.refresh`, 6 h, `NetworkType.UNMETERED`, kept across launches) for the favorites sync. The worker takes the signed-in user's sync from a small app-level seam (`FavoritesRefresh.provider`, set by the app services; null while signed out or before they are up, then the work ends at once); stopping the work cancels the sync. The sync itself still requires Wi-Fi (`!isExpensive`: metered or Data Saver) |
| NWPathMonitor (+ expensive / Low Data) | `ConnectivityManager.NetworkCallback` (NOT_METERED; `isRestrictBackgroundStatus`), keeping the "answered" semantics; online = INTERNET capability only (not VALIDATED), so a LAN-only self-hosted server stays reachable, as with `NWPath.satisfied` |
| Library/Caches (purgeable) | decided: `filesDir/lark/<serverKey>/<userId>/{files,staging,covers,lyrics,index.json}` (`serverKey` = first 8 bytes, hex, of the SHA-256 of the normalised base URL), internal storage, not purgeable by the system and not backed up or carried to a new phone (`allowBackup=false`, and `dataExtractionRules`/`fullBackupContent` excluding every domain: Android 12+'s device-to-device transfer ignores `allowBackup`, and would carry the WebView's session cookie and the secrets file); queues and events in `filesDir`. Free space via `StorageManager.getAllocatableBytes` behind `FreeSpace` (500 MB must stay free). `index.json` and every file write go through a temporary file and a rename; an unreadable index is an empty cache, `staging/` is emptied at every start, and nothing outside the user's folder is ever deleted. `CacheStore.wipe()` deletes a user's whole folder (sign-out for good, another server); `CacheLayout.pruneOtherServers` keeps only the current server's folder. The index write delay runs on the `Scheduler` |
| ATS http exception (localhost, *.local) | decided: `networkSecurityConfig`: https everywhere with the system's and the user's CAs; cleartext for `localhost` and `*.local` only, plus `10.0.2.2` in the debug build's copy (`src/debug/res/xml`), the same hosts `ServerUrl` takes. One `OkHttpClient` (`LarkHttp.client`) for the API, the `/me` check, downloads, covers and the player's streams (`Playback.httpClient`); its trust is the platform's, which follows the config, as the WebView's does. Certificate validation is never replaced |
| xcstrings | `values/`, `values-zh-rCN`, `values-zh-rTW`, `values-sv` |

Everything else (queues, kinds, shuffle/repeat with the web's fixtures, sleep timer with fade,
retries 2/5/10 s, stall switch after 6 s, failed items skipped 30 min, never-stop local-only mode,
refill, offline resume modes, play events, queue sync incl. the `yt:` rule, episode progress,
queue persistence, lookahead, offline cache with cap and eviction, favorites sync, offline lyrics)
ports as is.

### Cold start, shortcuts and car autoplay

- **`IntentRun`** (`lark.car`, port of iOS `ShuffleFavoritesIntent.swift`): `shuffleFavorites(player)` and `resume(player)`
  on an `IntentPlayer` (the engine through `EngineIntentPlayer`; `AppServices.intentPlayer`, replaceable in tests), with
  the iOS steps: shuffle while playing → `alreadyPlaying` and nothing else; no engine (no server) → `notSetUp`; the
  engine's `intentBlocker` (`notSignedIn`, `offlineNothingCached`) before anything starts; then `prepareForPlayback`
  before the first suspension, the shuffle (or resume, else shuffle), and `played` or `nothingToPlay(reason)`. One
  sealed `IntentResult` carries iOS's `IntentOutcome` and `LarkIntentError`. A permanent focus loss is the user's pause
  (the engine clears `pausedBySystem`), so it never counts as playing: the shuffle starts afresh.
- **Launcher shortcuts**: static (`android.app.shortcuts` on the launcher activity), targeting `ShortcutActivity`
  (`Theme.NoDisplay`, not exported: the system starts shortcuts as the app). It runs the intent on the services'
  main-thread scope, synchronously up to the first suspension, and starts the media service into the foreground
  (`PlaybackService.startInForeground`, as car autoplay does) while the activity is still visible, then finishes: the
  cold-start notification holds the foreground until playback starts, so a shuffle from the server that answers after
  the activity is gone still gets its media notification. The activity's own task (`taskAffinity=""`) goes as it
  finishes, and Android tells the app's started services (`onTaskRemoved`): `PlaybackService` ignores it for that task
  (Media3's default would pause the shuffle that is just starting and stop the service; any other task, Melarka's own
  swiped away, keeps Media3's default). The result (iOS's Shortcuts dialog) shows as a toast while the activity is
  still showing (a Resume that played says nothing); with no server or signed out, Melarka opens too. A result that
  comes after the activity finished (a shuffle the server answered) cannot be a toast: Android 13+ drops a toast from
  an app in the background whose notifications are off, and Melarka asks for no notification permission (the owner's
  call, Task 14). So a failure then brings Melarka forward (`MainActivity`, from the background within Android's grace
  period after the shortcut's activity) with the reason in a dialog (`AppServices.notice`; if Android refuses the
  start, the dialog shows when Melarka is next opened); a success or "already playing" says nothing
  (`ShortcutActivity.reply`).
- **The cold start's foreground**: a media button with the app not running reaches `MediaButtonReceiver`, which starts
  the service with `startForegroundService`; car autoplay does the same (`PlaybackService.startInForeground`). Android
  requires `startForeground` within 5 s even when nothing turns out to be playable (no server, signed out, offline with
  nothing cached), and Media3 only does it once something plays. So `PlaybackService.onStartCommand` goes into the
  foreground at once with a plain "Starting playback…" notification on Media3's channel and id (Media3's media
  notification replaces it), keeps it while the engine plays, a shuffle from the server runs (a play command's, or a
  shortcut's or car autoplay's `AppServices.runIntent` run: `intentRunning`, through `PlaybackHost.intentStarting`;
  Settings' server change runs as `runAppTask` and does not count),
  or a cold start waits for the network's first answer (at most 30 s), and otherwise removes it and stops the service.
  A cold start offline with nothing cached therefore ends with the service stopped; when the network comes back later in the background, the
  background-start rule (App services) applies (the engine waits for the user). Only the first start after the
  service's `onCreate` is a cold start: later `MEDIA_BUTTON` starts reach a running service, most of them from Media3's
  own notification buttons (Android 8-12 sends those as service starts), and go to Media3 alone, so a paused
  notification's Previous or Play neither swaps the notification nor stops the service.
- **Car autoplay** (`CarAutoplay`, `CarAutoplaySettings`): Settings turns it on; only then is the Nearby devices
  permission asked (`BLUETOOTH_CONNECT`, Android 12+), to list the paired cars. The user picks one and
  `CarAutoplay.associate(activity, address)` asks CompanionDeviceManager (a `BluetoothDeviceFilter` for that address);
  the system confirms once, the answer comes to `MainActivity.onActivityResult`, and on Android 12+ the car's presence
  is observed (`startObservingDevicePresence`, `REQUEST_OBSERVE_COMPANION_DEVICE_PRESENCE`, an install-time
  permission). When the car connects, the system binds `CarCompanionService` (`onDeviceAppeared`), which runs
  `IntentRun.shuffleFavorites` once per appearance: nothing again until `onDeviceDisappeared`, nothing at all if music
  is already playing (the car's own play resumed the queue first). The media service is started into the foreground
  first; Android 12+ allows that from the background only through the documented exemption for companion apps holding
  `REQUEST_COMPANION_START_FOREGROUND_SERVICES_FROM_BACKGROUND` (install-time; presence observation by itself grants
  no exemption). A refusal is logged and nothing plays, never a crash ("Android didn't allow Melarka to start playing
  in the background", four locales, for Settings). Picking another car lets the previous one go first (observation
  stopped, association removed: one car at a time). Turning it off stops the observation, removes the association and
  forgets the car and its presence. The system calls go through `CompanionOps` (`SystemCompanion`; a fake in tests).
- **Below Android 12** (no presence observation): the manifest receiver `CarConnectionReceiver` hears the associated
  car's `ACL_CONNECTED`/`ACL_DISCONNECTED` (exempt from the implicit-broadcast limits; `BLUETOOTH`, maxSdk 30) and runs
  the same path; Android 8–11 allow the foreground start from a receiver. What is lost: the system does not bind or
  wake Melarka for the car, so nothing happens after a force stop or where the phone maker blocks broadcasts to a
  stopped app (the vendor's "autostart"); any ACL link counts as the car connecting (not only audio); Settings says
  so (`car_autoplay_old_android`). The service is enabled from API 31 (`@bool/car_companion_service`); the receiver is
  disabled in the manifest and enabled below API 31 only while car autoplay is on, so no Bluetooth link wakes Melarka
  otherwise.
- **Phone makers** (outside China first, one standard approach): no vendor code paths. Settings shows Android's
  battery exemption (Native screens, Background playback) and, for the makers whose power management goes further, one line of guidance from
  `CarAutoplay.vendorHint(Build.MANUFACTURER)` (four locales): Xiaomi/Redmi/POCO (Autostart, battery saver "No
  restrictions"), Huawei/Honor (App launch → manage manually), OPPO/realme/OnePlus (auto-launch, background activity),
  vivo/iQOO (Autostart, high background power use), Samsung (Never sleeping apps). Stock-like phones (Pixel, Motorola,
  Nokia, Sony, …) get only the generic battery text. Car autoplay and playback with the screen off can still fail on
  those phones until the user follows the guidance; `docs/android.md` gives the steps (dontkillmyapp.com).
- Checked on the emulator (Task 14, `ShortcutTest`): the shortcut's foreground start for a server shuffle that returns
  after the activity finished (it plays, with Media3's notification, no screen opened; this found the two holes fixed
  above: the hold let go while the shuffle asked the server, and the shortcut's task removal paused it). Left to first
  users: CDM presence for a classic-Bluetooth car on Android 12/12L (the emulator pairs no car; its picker and the
  Nearby devices prompt were seen), and the vendor menu paths.

### Native screens

Server URL, can't-connect (Retry, Settings, Play Offline Favorites, Stay Offline), the offline
player (header, now-playing card with lyrics preview and sheet, Up Next, Offline Favorites) and
Settings (server, offline cache cap 1/2/5/10 GB, sync now, clear, Stay Offline, car autoplay
setup and test, background playback, about). Jetpack Compose, Material 3, dark theme matching the web app's colours.

- **Structure** (`lark.ui`): `MainActivity` is a `ComponentActivity` whose root holds the web view and, above it, one
  `ComposeView` with the native screens. The view is shown only while a native screen is up (no server, a failed page,
  the offline player, Settings), so the page gets every touch otherwise. `AppServices` is not Compose state: the
  Activity copies what the screens show on each `observe` change, cache changes included (`CacheStore.onChange` →
  `AppServices.cacheChanges`, so Settings' figures and Play Offline Favorites follow a sync). Back goes through the
  `OnBackPressedDispatcher`: full screen, Settings, the offline player, the page's history, then the system's. The models (`OfflinePlayerModel`, `SettingsModel`,
  ports of iOS) hold Compose state; `AppServices.makeOfflinePlayer` builds the offline player's model on the engine
  (`EngineControl`: `WebMessage`s in, `NativeEvent`s out, no network). The theme takes `web/src/styles.css`'s tokens;
  the few icons are Material paths in code (no icon library).
- **Server screen**: `ServerUrl.normalize`, then `ServerUrl.check` (`GET /api/v1/info` answers 200) before
  `setServerUrl`. Changing the server later asks for confirmation, then runs `AppServices.changeServer`, which first
  closes the old page's bridge (`BridgeController.close`: what the page posts during the sign-out flush, its shell still
  on screen, reaches nothing, and no event goes to it).
- **Car autoplay in Settings**: the switch asks for Nearby devices only when turned on, then lists the paired cars
  (or the system's search nearby) and calls `CarAutoplay.associate`; the car's name, another car, Test: Shuffle
  Favorites (the shortcut's `IntentRun`), what the last connection did (`lastResult`), and below Android 12 the
  `car_autoplay_old_android` note.
- **Background playback**: the plain guide ("If music stops when the screen is off…"), a button for Android's
  exemption request (`ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS`; the manifest declares
  `REQUEST_IGNORE_BATTERY_OPTIMIZATIONS`, an accepted use for a media player, and F-Droid accepts it for media apps;
  where a phone has no such dialog, the battery-optimisation list), or that it is done; the phone maker's line
  (`vendorHint`); and a link to `docs/android.md` on GitHub.
- **Languages**: four locales; `zh-rTW` is written in Traditional characters with Taiwan wording (設定, 應用程式,
  網路, 快取), unlike the iOS zh-Hant entries; a test checks every key and placeholder in all four, and that no
  Simplified-only character is in `zh-rTW`. `res/xml/locales_config.xml` offers them to Android 13's per-app
  language setting. Hong Kong and Macau phones get the Traditional text (Android's script-aware fallback).

### Web side (small, additive)

- `larkNative.platform` ("ios" from the iPhone app too) so the web can say "Android app settings"
  instead of "iPhone app settings" (`settings.nativeAppAndroid` / `settings.nativeAppIos`); an iPhone app older
  than this sends none, and the web then shows the neutral "Melarka app settings" (`settings.nativeApp`).
- Comments and the Settings label in the web; `docs/android.md` linked from the README.

## Testing

- **Unit tests** (JVM + Robolectric): the ported equivalents of every iOS test file, with the same
  fakes; the web's play-mode fixtures shared by path.
- **Instrumented tests on the emulator** (`app/src/androidTest`, run by `android/scripts/emulator-smoke.sh`, which
  starts the e2e server from `web/e2e` in demo mode on the host's `127.0.0.1:4700`, reachable from the emulator at
  `10.0.2.2:4700`, with a made-up admin password passed as an instrumentation argument; it boots the AVD `lark_api35`
  headless unless a device is attached, and runs `connectedDebugAndroidTest`; `remote.sh smoke` runs it on the build
  Mac). The Android Test Orchestrator gives every test its own process and wiped app data (the app services are
  process-wide). `SmokeFlowTest`: the server screen, sign-in in the page, a favorite played from the page, the session
  through a `MediaController` (playing, still playing 10 s after Home, pause, play, next), the page's renderer killed
  with playback going on and a new web view signed in again, `window.larkNative` frozen in the real page and its
  `post` reaching native, then Wi-Fi and mobile data off and the next cached favorite playing from the phone. Beside
  it: `KeystoreSecretStoreTest` (the real Keystore: restart, deleted or damaged file, lost key, the delete fallback),
  `GainBoundaryTest` (an MP3 then the preloaded AAC at different gains through the real decoders and sink: every
  buffer at its own item's level, `gainMisses` 0), `BackTest` (Back with nothing to close, started from the launcher:
  the task moves back, the Activity is not finished), `ColdStartOfflineTest` (a media-button play offline with nothing
  cached: the service stops, no notification left) and `ShortcutTest` (Shuffle Favorites from the shortcut with the
  app not running). A failure leaves a screenshot and the page's text in the test outputs, copied to
  `app/build/smoke` with the server log and logcat.
- **Bridge contract**: the web's `nativeTransport.test.tsx` stays green; an Android test feeds the
  same message fixtures into the decoder and checks event JSON byte-for-byte against iOS fixtures.
- **CI**: an `android` job (path filter `android/**`, `web/src/native/**`) on Ubuntu: unit tests,
  lint, a debug build; an `android-emulator` job (the same paths plus the e2e server files the smoke test starts,
  45 min) on Ubuntu with KVM enabled: Go, Node (the web build), ffmpeg (the demo library), then
  `reactivecircus/android-emulator-runner` (pinned, v2.38.0) boots an x86_64 API 35 `google_apis` emulator and runs
  `android/scripts/emulator-smoke.sh`; a failure uploads `app/build/smoke` and the test report.
- **Release**: a `apk` job in `release.yml` builds and signs `Melarka-X.Y.Z.apk`, verifies
  `versionName`/`versionCode`/package name and the signing certificate, and attaches it to the
  GitHub Release.

## Distribution and docs

- GitHub Releases: `Melarka-X.Y.Z.apk` on every release.
- `docs/android.md` (same sections as `docs/iphone.md`): install (allow "unknown apps", or
  Obtainium with the repo URL), connect, settings, car autoplay, updates, troubleshooting. README
  gains an Android section and links.
- F-Droid: metadata in the repo; the fdroiddata merge request after the first release; the
  NonFreeNet anti-feature declared.

## What's left to the builder

Module boundaries inside `android/`, exact library versions, whether per-item gain is an
`AudioProcessor` or volume switching (decided in Task 6: `AudioProcessor`), the Compose structure of the native screens, the emulator test's exact steps, and
the CI emulator action (both decided in Task 14: Testing, above).

## Questions settled with the owner (2026-10-17)

1. Car autoplay: the companion-device approach, documented for users.
2. Phones: standard Android first, outside China; vendor workarounds as in-app guidance and docs,
   no vendor-specific builds.
3. Signing key backups: GitHub secret, the private Mac, the owner's email (file); passwords in the
   GitHub secret and Apple Passwords (on the private Mac). Generated on the private Mac.
