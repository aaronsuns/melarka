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
  association is what lets an app start playback from the background on Android 12+). It does
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
  passwords in the GitHub secret, Apple's Passwords app (entered on the private Mac) and Bitwarden —
  never the file and the password in the same place outside GitHub.

## The builder's calls (made here; the owner can override)

- **Language and stack**: Kotlin; AndroidX; Media3 (ExoPlayer, MediaSessionService,
  MediaSession); Android WebView (AndroidX WebKit for `addWebMessageListener` and
  `addDocumentStartJavaScript`); WorkManager; OkHttp for API calls (or Media3's
  DefaultHttpDataSource for streams); kotlinx.serialization for JSON; JUnit + Robolectric for unit
  tests; an instrumented smoke test on the emulator.
- **minSdk 26 (Android 8.0), targetSdk/compileSdk 35.** Covers practically every phone in use and
  keeps foreground-service and notification-channel code single-path.
- **Package name** `io.github.aaronsuns.melarka` (matches the iOS bundle id); app name "Melarka";
  the icon from `docs/brand`.
- **Project location**: `android/` at the repo root, Gradle (Kotlin DSL) with a version catalog.
- **Version**: `versionName` = the release tag (X.Y.Z); `versionCode` = X×10000 + Y×100 + Z
  (monotonic, readable). The source of truth is the same hand-bumped version as `ios/project.yml`.
- **Signing**: one release keystore, generated once, stored as a GitHub Actions secret (base64
  plus passwords); CI signs the release APK. A backup copy of the keystore is kept outside the repo
  (location: the owner's call, see Open questions).
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
 ├─ WebView: the server's web app          ├─ ExoPlayer (gapless playlist, per-item gain)
 │    window.larkNative  ── bridge ──────► ├─ PlayerEngine (port of ios/Sources/Player)
 │    "lark-native" events ◄────────────── ├─ MediaSession (lock screen, notification, AVRCP,
 ├─ Server URL screen (native)             │   car lyrics line as title, playback resumption)
 ├─ Can't-connect screen (native)          └─ owned by AppServices (process-wide)
 ├─ Offline player (native, Compose)
 └─ Settings (native, Compose)           AppServices: API client, AuthStore, UserCache,
                                           QueueStore, PlayEventQueue, QueueSync, FavoritesSync
```

The engine and everything it needs live outside the Activity (in an application-scoped
`AppServices` and the media service), so playback, the car and lock-screen controls work with no
UI, after the Activity is destroyed, and on a cold start from a Bluetooth play button.

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
  accepted only from the main frame and the server's origin.
- Native → web: `evaluateJavascript` of `window.dispatchEvent(new CustomEvent("lark-native",
  {detail:…}))`, with the iOS escaping (U+2028/U+2029, `</`, non-finite numbers as strings) and the
  same event shapes: `state`, `queue`, `flushed`, `authRequired`, `notice`.
- Every web→native message type and rule from `ios/Sources/Bridge/WebMessage.swift` (unknown types
  dropped, unknown fields ignored, required fields enforced, lenient numbers).
- Events are not sent while the Activity is stopped; on resume, `emitAll()` re-sends both queues
  and states (as iOS on scene activation).
- Navigation policy as iOS (`BridgeController`/`WebShell`): same-origin in the web view, other web
  origins open in the browser, `data:`/`javascript:` blocked in the main frame, the YouTube embed
  allowed in subframes, a main-frame 5xx counts as a load failure, a renderer crash reloads.

### Auth

- Sign-in happens in the web page; the server sets the HttpOnly `lark_token` cookie. Native reads
  it with `CookieManager.getCookie(serverURL)` after page loads and on `auth{signedIn:true}`.
- Android returns cookie values without attributes, so the iOS HttpOnly/Path checks can't be
  copied. **The builder's call:** accept the cookie by name for the server URL, never re-adopt the
  last revoked value, and verify the token with `GET /api/v1/me` (or an equivalent authenticated
  call) before storing it.
- Store the token and user id with the Android Keystore (an AES key in the Keystore encrypting a
  small file in credential-encrypted storage), not in plain SharedPreferences.
- Sign-out, user switching, server change and 401 handling exactly as `AppServices.swift`.

### Playback (Media3)

| iOS mechanism | Android mechanism |
|---|---|
| AVQueuePlayer + preload | ExoPlayer playlist (current + next MediaItem; gapless built in) |
| AVAudioMix per-item gain | per-item volume via a custom `AudioProcessor` (or `player.volume` set on transitions); master volume reserved for the sleep fade |
| Bearer header | `DefaultHttpDataSource.Factory.setDefaultRequestProperties` (token from AuthStore, refreshed on change) |
| Pitch-preserving rate (episodes) | `PlaybackParameters(speed)` (Sonic keeps pitch) |
| AVAudioSession interruptions | audio focus via `AudioAttributes` + `handleAudioFocus=true`; transient loss pauses and resumes |
| route `oldDeviceUnavailable` | `setHandleAudioBecomingNoisy(true)` |
| MPNowPlayingInfo + remote commands | MediaSession metadata/state + commands; custom commands for skip ±, shuffle, repeat |
| car lyrics as title | the same: the current line as the MediaSession title, "song · artist" as the artist, updated only when the line changes |
| cold start from the car (remote play) | `MediaSession.Callback.onPlaybackResumption` + media button receiver; the synchronous resume-from-disk logic of `PlayerEngine.resumeFromDisk` |
| Shortcuts "car connects → shuffle favorites" | CompanionDeviceManager association with the car (picked in Settings; the system confirms once); its connect callback runs the same path as the iOS ShuffleFavorites intent; launcher shortcuts "Shuffle favorites" and "Resume" |
| BGAppRefreshTask | WorkManager periodic work (6 h, unmetered network) for the favorites sync |
| NWPathMonitor (+ expensive / Low Data) | `ConnectivityManager.NetworkCallback` (NOT_METERED; `isRestrictBackgroundStatus`), keeping the "answered" semantics |
| Library/Caches (purgeable) | `filesDir` (or app-specific external files) for audio; queues and events in `filesDir`; free space via `StorageManager.getAllocatableBytes` |
| ATS http exception (localhost, *.local) | `networkSecurityConfig` cleartext for those domains only |
| xcstrings | `values/`, `values-zh-rCN`, `values-zh-rTW`, `values-sv` |

Everything else (queues, kinds, shuffle/repeat with the web's fixtures, sleep timer with fade,
retries 2/5/10 s, stall switch after 6 s, failed items skipped 30 min, never-stop local-only mode,
refill, offline resume modes, play events, queue sync incl. the `yt:` rule, episode progress,
queue persistence, lookahead, offline cache with cap and eviction, favorites sync, offline lyrics)
ports as is.

### Native screens

Server URL, can't-connect (Retry, Settings, Play Offline Favorites, Stay Offline), the offline
player (header, now-playing card with lyrics preview and sheet, Up Next, Offline Favorites) and
Settings (server, offline cache cap 1/2/5/10 GB, sync now, clear, Stay Offline, car autoplay
setup and test, about). Jetpack Compose, Material 3, dark theme matching the web app's colours.

### Web side (small, additive)

- `larkNative.platform` ("ios" from the iPhone app too) so the web can say "Android app settings"
  instead of "iPhone app settings"; a platform-neutral fallback label when absent.
- Comments and the Settings label in the web; `docs/android.md` linked from the README.

## Testing

- **Unit tests** (JVM + Robolectric): the ported equivalents of every iOS test file, with the same
  fakes; the web's play-mode fixtures shared by path.
- **Instrumented smoke test on the emulator**: launch against a local fake server (the e2e test
  server from `web/e2e` started in demo mode, reachable from the emulator at `10.0.2.2`), sign in,
  play a favorite, background the app and check it keeps playing (MediaSession state), lock-screen
  controls through `MediaController`, offline favorites with networking turned off.
- **Bridge contract**: the web's `nativeTransport.test.tsx` stays green; an Android test feeds the
  same message fixtures into the decoder and checks event JSON byte-for-byte against iOS fixtures.
- **CI**: an `android` job (path filter `android/**`, `web/src/native/**`) on Ubuntu: unit tests,
  lint, a debug build; the emulator test via an emulator action (x86_64 with KVM).
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
`AudioProcessor` or volume switching (behaviour must match: attenuated items never sound
unattenuated), the Compose structure of the native screens, the emulator test's exact steps, and
the CI emulator action.

## Questions settled with the owner (2026-10-17)

1. Car autoplay: the companion-device approach, documented for users.
2. Phones: standard Android first, outside China; vendor workarounds as in-app guidance and docs,
   no vendor-specific builds.
3. Signing key backups: GitHub secret, the private Mac, the owner's email (file); passwords in the
   GitHub secret, Apple Passwords (on the private Mac) and Bitwarden. Generated on the private Mac.
