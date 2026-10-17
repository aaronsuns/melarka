# Melarka for Android Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An Android app with the iPhone app's features: the server's web app in a WebView plus a
native Media3 playback engine, released as a signed APK on GitHub (then F-Droid).

**Architecture:** A port of `ios/` module by module. The iOS source and tests are the
specification: each task reads the named Swift files and ports their behaviour and their test
cases to Kotlin with the same fakes. Only the platform mechanisms change (WebView bridge, Media3,
MediaSession, Keystore, WorkManager, CompanionDeviceManager).

**Tech Stack:** Kotlin 2.x, Android Gradle Plugin 8.x (Kotlin DSL, version catalog), minSdk 26 /
targetSdk 35, AndroidX WebKit, Media3 (exoplayer, session, datasource), Jetpack Compose +
Material 3, WorkManager, OkHttp (+ MockWebServer in tests), kotlinx.serialization,
kotlinx.coroutines (+ test), JUnit 4, Robolectric, AndroidX Test (instrumented).

**Spec:** [docs/superpowers/specs/android-client-design.md](../specs/android-client-design.md);
parity spec [docs/superpowers/specs/ios-client-design.md](../specs/ios-client-design.md).
Keep this plan and both specs current as the code changes (AGENTS.md).

## Global Constraints

- applicationId `io.github.aaronsuns.melarka`; app name "Melarka"; Kotlin packages and identifiers use `lark` (`lark.app`, `lark.bridge`, …), never "melarka", per AGENTS.md "Naming".
- minSdk 26, targetSdk 35, compileSdk 35.
- versionName = release tag X.Y.Z; versionCode = X*10000 + Y*100 + Z; the version comes from `ios/project.yml` MARKETING_VERSION's value, which is bumped together with Android's (one shared version).
- Every user-visible string in four locales: `values/` (en), `values-zh-rCN`, `values-zh-rTW`, `values-sv`; texts reuse the iOS wording from `ios/Sources/App/Localizable.xcstrings` where the feature is the same.
- Tests never touch the network: fakes, MockWebServer, Robolectric.
- The token: never logged, never in a URL, never on the bridge; `Authorization: Bearer` only.
- Bridge events byte-compatible with iOS: same keys, explicit `null`s, same escaping.
- Development builds run on the private Mac (`aaron@192.168.31.71`) through `android/scripts/remote.sh`; nothing private (keys, passwords) is ever written on the work Mac.
- Commits `type(scope): summary` (scope `android`, `web`, `ci`, `docs`); each task updates `docs/superpowers/specs/android-client-design.md` if it changed a decision there.

## Review Focus

1. A song playing when the screen locks, the app is swiped away, or the WebView renderer dies keeps playing (the media service owns playback) — Task 8 `PlaybackSurvivesActivityDestroyTest`, Task 14 smoke test.
2. A phone call or another app's audio pauses Melarka and a transient loss resumes it; unplugging headphones pauses and stays paused — Task 8 `AudioFocusTest`, `NoisyTest`.
3. A server with a self-signed/private-CA certificate the user installed connects; plain http to a public host does not — Task 4 `ServerUrlTest` + Task 10 `NetworkSecurityConfigTest`.
4. Sign-in in the page is picked up by native even when the cookie appears without a page reload (SPA login) — Task 4 `CookieHarvestTest` (`auth{signedIn:true}` triggers a harvest) and Task 10.
5. A Bluetooth play press with the app not running (cold start) plays cached favorites without waiting for the network — Task 11 `ColdStartTest` (port of `CarColdStartTests`).

---

### Task 1: Toolchain, project skeleton, remote build and CI

**Files:**
- Create: `android/settings.gradle.kts`, `android/build.gradle.kts`, `android/gradle/libs.versions.toml`, `android/gradle.properties`, `android/gradlew` (+ wrapper), `android/app/build.gradle.kts`, `android/app/src/main/AndroidManifest.xml`, `android/app/src/main/kotlin/lark/app/MainActivity.kt` (placeholder), launcher icon from `docs/brand`, `android/app/src/test/kotlin/lark/app/SmokeTest.kt`, `android/scripts/remote.sh`, `android/README.md`
- Modify: `.github/workflows/ci.yml` (new `android` job; path filter `android/**`, `web/src/native/**`), `.gitignore`

**Interfaces:**
- Produces: `android/scripts/remote.sh {sync|test|build|run '<cmd>'}` (rsync `android/` to `~/lark-android-build` on `$LARK_BUILD_HOST`, run `./gradlew` there); version wiring `versionName`/`versionCode` read from `ios/project.yml` MARKETING_VERSION by a small Gradle function `larkVersion()` (release CI overrides with `-PlarkVersion=X.Y.Z`).

- [ ] **Step 1:** On the private Mac, install JDK 17 (`brew install --cask temurin@17`), Android command-line tools (`brew install --cask android-commandlinetools`), then `sdkmanager "platform-tools" "platforms;android-35" "build-tools;35.0.0" "emulator" "system-images;android-35;google_apis;arm64-v8a"`; create AVD `lark_api35`. Verify: `ssh aaron@192.168.31.71 'java -version && sdkmanager --list_installed | grep android-35'` shows JDK 17 and platform 35.
- [ ] **Step 2:** Write `SmokeTest.versionCodeFromVersionName` asserting `versionCode("0.2.15") == 215` and `versionCode("1.10.3") == 11003`.
- [ ] **Step 3:** Create the Gradle project and `larkVersion()`/`versionCode()`; run `LARK_BUILD_HOST=aaron@192.168.31.71 android/scripts/remote.sh test` — PASS; `remote.sh build` produces `app-debug.apk`.
- [ ] **Step 4:** Add the `android` CI job (ubuntu, JDK 17, `./gradlew testDebugUnitTest lintDebug assembleDebug`); push a branch and see it green.
- [ ] **Step 5:** Commit `feat(android): project skeleton, remote build on the private Mac, CI job`.

### Task 2: Bridge protocol (messages, events, items)

Port `ios/Sources/Bridge/{WebMessage,NativeEvent,Item}.swift` and `ios/Tests/WebMessageTests.swift`.

**Files:**
- Create: `android/app/src/main/kotlin/lark/bridge/{WebMessage.kt,NativeEvent.kt,Item.kt,Json.kt}`
- Test: `android/app/src/test/kotlin/lark/bridge/{WebMessageTest.kt,NativeEventTest.kt}`, fixtures `android/app/src/test/resources/bridge/*.json`

**Interfaces:**
- Produces: `sealed interface WebMessage` (one subclass per message type in ios-client-design.md "Web → native messages"), `WebMessage.decode(json: String): WebMessage?` (null = drop); `data class Item(kind: Kind, id: String, title, artist, album: String, durationMs: Long, meta: JsonElement)` with `trackId: Long?` (null for `yt:` and episodes) and `youtubeVideoId: String?`; `sealed interface NativeEvent { State, Queue, Flushed, AuthRequired, Notice }`, `NativeEvent.toJson(): String`, `dispatchScript(event): String` (the `CustomEvent("lark-native")` call with iOS escaping); `enum class QueueSource { list, radio, restored, shuffle, favorites }`.

- [ ] **Step 1:** Port every `WebMessageTests` case to `WebMessageTest` (same inputs, same expected values; e.g. `decode({"type":"sleepTimer","cancel":true,"minutes":5}) == SleepTimer(Cancel)`, unknown type → null, missing `kind` → null, `positionMs: 1500.6` → 1501).
- [ ] **Step 2:** `NativeEventTest`: `State` with null itemId/error/sleepRemainingMs encodes `"itemId":null,…,"sleepRemainingMs":null`; `dispatchScript` escapes U+2028, U+2029, `</` and encodes NaN/Infinity as strings; compare against fixture JSON copied from iOS output (generate once from `NativeEvent.swift` encodings described in its tests).
- [ ] **Step 3:** Run `remote.sh test` — FAIL (no classes).
- [ ] **Step 4:** Implement with kotlinx.serialization (lenient numbers, ignore unknown keys, `explicitNulls = true` for events).
- [ ] **Step 5:** Run — PASS. Commit `feat(android): bridge messages and events, compatible with iOS`.

### Task 3: Time, scheduling, network status, fakes

Port `ios/Sources/Player/NetworkStatus.swift` (incl. SwitchableNetworkStatus), the scheduler abstraction and `ios/Tests/Fakes.swift`/`TestSupport.swift`.

**Files:**
- Create: `lark/core/{Clock.kt,Scheduler.kt,NetworkStatus.kt,AndroidNetworkStatus.kt}`
- Test: `lark/testing/{FakeScheduler.kt,FakeNetwork.kt,FakeClock.kt}` (in `src/test`), `lark/core/StayOfflineNetworkTest.kt`

**Interfaces:**
- Produces: `interface Scheduler { fun after(seconds: Double, fn: () -> Unit): Cancellable; fun every(...) }`, `interface NetworkStatus { val isOnline: Boolean; val isExpensive: Boolean; val answered: Boolean; fun observe(fn: () -> Unit) }`, `class SwitchableNetworkStatus(inner: NetworkStatus, stayOffline: Boolean)`; `AndroidNetworkStatus(context)` (NetworkCallback; expensive = !NOT_METERED || restrictBackground). Fakes mirror the Swift fakes' API names.

- [ ] Steps: port `StayOfflineTests` network parts (stayOffline → offline, answered=true; turning off notifies observers); FAIL; implement; PASS; commit `feat(android): clock, scheduler, network status and test fakes`.

### Task 4: API client, server URL, auth store

Port `ios/Sources/API/{LarkAPI,Models}.swift`, `Bridge/{AuthStore,SecretStore}.swift`, `App/ServerURLView.swift` normalisation, and their tests (`LarkAPITests`, `AuthStoreTests`, `ServerURLTests`).

**Files:**
- Create: `lark/api/{LarkApi.kt,Models.kt,LarkError.kt}`, `lark/auth/{AuthStore.kt,SecretStore.kt,KeystoreSecretStore.kt,CookieSource.kt}`, `lark/app/ServerUrl.kt`
- Test: `lark/api/LarkApiTest.kt` (MockWebServer), `lark/auth/{AuthStoreTest.kt,CookieHarvestTest.kt}`, `lark/app/ServerUrlTest.kt`

**Interfaces:**
- Produces: `interface LarkApiProtocol` with every call in ios-client-design.md "Server-side" list (`info()`, `favorites(cursor)`, `random(...)`, `radio(...)`, `queue()`, `saveQueue(trackIds, index, positionMs)`, `postEvents(events): Int`, `episodeProgress(id, positionS, played)`, `lyrics(trackId): LyricsDoc`, `artwork(item): ByteArray`, `download(trackId, quality, dir): File`, `streamUrl(item, quality): Uri`, `token: String?`); `class AuthStore(secrets: SecretStore, cookies: CookieSource)` with `harvest()`, `clear()`, `token`, `userId`; `ServerUrl.normalize(input: String): Uri?` (https; http only for localhost/*.local and `10.0.2.2` in debug builds).
- `CookieSource.lark_token(url): String?` wraps `CookieManager.getCookie`; harvest verifies a new token with `GET /api/v1/me` before storing (spec "Auth"); never re-adopts the last revoked value; a missing cookie never signs out.

- [ ] Steps: port each listed iOS test (streamUrl for track / episode / `yt:` with exact query strings, Bearer header present and no token in URLs, 401 with current token → one shared sign-out, signedOut throws without a request); add `CookieHarvestTest.verifiesBeforeStoring` (MockWebServer `/api/v1/me` 401 → token not stored); FAIL; implement (KeystoreSecretStore: AES/GCM key in AndroidKeyStore, ciphertext in `filesDir/secrets`; Robolectric uses an in-memory `SecretStore`); PASS; commit `feat(android): API client, server URL rules, auth store`.

### Task 5: Persistence and sync: queue store, play events, queue sync, play modes

Port `Player/{QueueStore,PlayEventQueue,QueueSync,PlayModes}.swift` and tests (`QueueStoreTests`, `PlayEventTests`, `QueueSyncTests`, `PlayModesTests` — the web's fixtures from `web/src/player/modes*.json` read by path).

**Files:**
- Create: `lark/player/{QueueStore.kt,QueueSnapshot.kt,PlayEventQueue.kt,QueueSync.kt,PlayModes.kt}`
- Test: same names with `Test` in `src/test/kotlin/lark/player/`

**Interfaces:**
- Produces: `QueueSnapshot(version=1, music: PlaybackQueue, episodes: PlaybackQueue, active: Kind, savedAt, ownDevice, modes)`, `QueueStore(dir).load()/save()` per user; `PlayEventQueue.add(trackId, startedAt, playedSeconds, skipped, quality)`, `flush()`; `QueueSync` with `snapshot: () -> Snapshot?`, `online: () -> Boolean` (false while a `yt:` item is queued), `shouldAdopt(...)`; `PlayModes` functions with the same names as `modes.ts`.

- [ ] Steps: port tests (incl. `testAQueueWithYouTubeTracksIsNotSent`); FAIL; implement; PASS; commit `feat(android): queue store, play events, queue sync and play modes`.

### Task 6: Media backend (Media3)

Port the `MediaBackend` protocol and `AVPlayerBackend` behaviour (`Player/{MediaBackend,AVPlayerBackend,Gain}.swift`; tests `AVPlayerBackendTests`, `GainTests`, `GaplessTests`).

**Files:**
- Create: `lark/player/{MediaBackend.kt,Media3Backend.kt,GainProcessor.kt,MediaSource.kt}`
- Test: `lark/player/{Media3BackendTest.kt,GainTest.kt}` (Robolectric + Media3 `TestExoPlayerBuilder`), `FakeBackend.kt` in testing

**Interfaces:**
- Produces: `interface MediaBackend { fun load(src: MediaSource, startMs: Long, autoplay: Boolean, rate: Float, gain: Float, generation: Int); fun preload(src: MediaSource?, gain: Float); play(); pause(); seek(ms); setRate(); setMasterVolume(); val positionMs; val durationMs; var delegate: BackendDelegate }`; `sealed class MediaSource { File(path), Remote(uri, token) }`; `Gain.factor(item, enabled): Float` = 10^(gain_db/20) clamped ≤ 1, music only.
- Gain is per item via `GainProcessor` (an `AudioProcessor` whose factor follows the playing item); master volume stays for the sleep fade; an attenuated item must never sound at full volume.

- [ ] Steps: port tests (stale generation callbacks dropped; preloaded next adopted on reload of the same source; gain applied before first sound; episode rate keeps pitch); FAIL; implement; PASS; commit `feat(android): Media3 backend with gapless preload and per-item gain`.

### Task 7: Player engine

Port `Player/{PlayerEngine,NextChooser,PlaybackQueue,CarLyrics}.swift` and tests `PlayerEngineTests`, `EngineRobustnessTests`, `NeverStopTests`, `ReconnectEngineTests`, `SleepTimerTests`, `EpisodeProgressTests`, `LookaheadTests`, `CarLyricsTests`, `PlayModesTests` (engine parts).

**Files:**
- Create: `lark/player/{PlayerEngine.kt,NextChooser.kt,PlaybackQueue.kt,CarLyrics.kt,SleepTimer.kt}`
- Test: one file per ported iOS test file under `src/test/kotlin/lark/player/`

**Interfaces:**
- Consumes: Tasks 2–6.
- Produces: `class PlayerEngine(backend, api, cache: CacheProviding, store, events, network, scheduler, clock, random, nowPlaying: NowPlayingSink?)` with `handle(m: WebMessage)`, `onEvent: (NativeEvent) -> Unit`, `emitAll()`, `tick()`, `reset()`, `resumeFromDisk(): Boolean`, `shuffleFavorites()`, `prepareForPlayback`, `prefs`, `modes`; `interface CacheProviding` (as `Cache/CacheProviding.swift`); `interface NowPlayingSink { fun show(item, positionMs, durationMs, rate, playing, lyricLine) ; fun clear() }`.

Split into commits by test file group, each red→green: (a) PlayerEngine core + lookahead, (b) robustness/never-stop/reconnect, (c) sleep timer, gain wiring, episode progress, car lyrics. Constants exactly as iOS (retries 2/5/10 s, stall 6 s, failed 30 min, refill thresholds 2/20/10, exclude 300, prev restart 3 s, episode resume rule 30 s).

- [ ] Steps per group: port tests; FAIL; implement; PASS; commit `feat(android): player engine (<group>)`.

### Task 8: Media service: session, notification, focus, car lyrics

Port `Player/{NowPlayingController,RemoteCommands,AudioSessionController}.swift` behaviour to Media3 (`NowPlayingTests`, `RemoteCommandsModesTests`, `AudioSessionTests`).

**Files:**
- Create: `lark/service/{PlaybackService.kt (MediaSessionService),SessionPlayer.kt (ForwardingPlayer exposing the engine),NowPlayingController.kt,ArtworkLoader.kt}`; manifest service with `foregroundServiceType="mediaPlayback"`
- Test: `lark/service/{NowPlayingTest.kt,RemoteCommandsTest.kt,AudioFocusTest.kt,NoisyTest.kt,PlaybackSurvivesActivityDestroyTest.kt}` (Robolectric `ServiceController`)

**Interfaces:**
- Consumes: `PlayerEngine`, `NowPlayingSink` (implemented by `NowPlayingController`).
- Produces: the MediaSession with commands play/pause/next/prev/seek, custom commands `SKIP_FORWARD_30`, `SKIP_BACK_15` (episodes), `SHUFFLE`, `REPEAT` (music); metadata title = car-lyrics line when present (artist "title · artist"); artwork LRU 20 with 404 memory and 30→600 s backoff; `AudioAttributes(USAGE_MEDIA, CONTENT_TYPE_MUSIC)` with `handleAudioFocus=true`; `setHandleAudioBecomingNoisy(true)`.

- [ ] Steps: tests first — metadata written only when shown values change; lyric line becomes the title; transient focus loss pauses and resumes, permanent loss pauses; noisy pauses; destroying the Activity doesn't stop playback; FAIL; implement; PASS; commit `feat(android): media session service, notification, focus and car lyrics`.

### Task 9: Offline cache and favorites sync

Port `Cache/{CacheStore,UserCache,FavoritesSync,CacheProviding}.swift`, `Cache/BackgroundRefresh.swift` and tests `CacheStoreTests`, `CacheWiringTests`, `FavoritesSyncTests`, `OfflineLyricsTests`.

**Files:**
- Create: `lark/cache/{CacheStore.kt,UserCache.kt,FavoritesSync.kt,CacheIndex.kt,FavoritesRefreshWorker.kt}`
- Test: matching `Test` files

**Interfaces:**
- Produces: `CacheStore(root: File, api, network, clock)` implementing `CacheProviding`; layout `filesDir/lark/<serverKey>/<uid>/{files,staging,covers,lyrics,index.json}`; `serverKey` = first 8 bytes hex of SHA-256 of the normalised base URL; caps 1/2/5/10 GB (default 2); 500 MB free required (`StorageManager.getAllocatableBytes`); 2 concurrent downloads; Wi-Fi only sync; `FavoritesRefreshWorker` every 6 h on unmetered network.

- [ ] Steps: port tests; FAIL; implement; PASS; commit `feat(android): offline cache and favorites sync`.

### Task 10: App services and the WebView shell

Port `App/AppServices.swift`, `Bridge/{BridgeController,WebShell,PageLoad}.swift` (`AppServicesTests`, `BridgeWebViewTests`, `CacheWiringTests` wiring parts, `StayOfflineTests`).

**Files:**
- Create: `lark/app/{LarkApplication.kt,AppServices.kt,MainActivity.kt (real)}`, `lark/bridge/{BridgeController.kt,WebShell.kt,NavigationPolicy.kt,PageLoad.kt}`, `res/xml/network_security_config.xml`
- Test: `lark/app/{AppServicesTest.kt,NetworkSecurityConfigTest.kt}`, `lark/bridge/{NavigationPolicyTest.kt,BridgeControllerTest.kt}`

**Interfaces:**
- Consumes: Tasks 2–9.
- Produces: `AppServices` (application-scoped: auth, api, engine, cache, network, settings) with `handle(WebMessage)`, `signOut(flush)`, `switchUser(uid)`, `changeServer()`, `stayOffline`, `pageFailed`; document-start script defining `window.larkNative = Object.freeze({version:1, platform:"android", post(m){ lark.postMessage(JSON.stringify(m)) }})` and `WebViewCompat.addWebMessageListener(view, "lark", setOf(serverOrigin))` accepting main-frame messages only; events via `evaluateJavascript(dispatchScript(e))`, suppressed while the Activity is stopped, `emitAll()` on start; network security config: system + user CAs, cleartext only for `localhost`, `*.local`, and `10.0.2.2` in debug.

- [ ] Steps: tests first (policy table from ios-client-design.md "Navigation"; messages from another origin or a subframe dropped; sign-out sequence order; user switch restores that user's queues; server change prunes other servers' caches; Stay Offline opens the offline player and skips loading); FAIL; implement; PASS; commit `feat(android): app services and the WebView shell with the native bridge`.

### Task 11: Cold start, shortcuts and car autoplay

Port `Intents/*` and the cold-start path (`CarColdStartTests`, `IntentTests`).

**Files:**
- Create: `lark/car/{IntentRun.kt,ShortcutActivity.kt (no UI),CarAutoplay.kt (CompanionDeviceManager association + callback),CarAutoplaySettings.kt}`, `res/xml/shortcuts.xml`, manifest `CompanionDeviceService` (API 31+) / fallback receiver
- Test: `lark/car/{ColdStartTest.kt,IntentRunTest.kt,CarAutoplayTest.kt}`

**Interfaces:**
- Produces: `IntentRun.shuffleFavorites()` / `resume()` with results `played | alreadyPlaying | notSetUp | notSignedIn | offlineNothingCached | nothingToPlay(reason)`; `MediaSession.Callback.onPlaybackResumption` → `engine.resumeFromDisk()` synchronously; `CarAutoplay.associate(activity, device)`; on the companion device appearing → `IntentRun.shuffleFavorites()` (does nothing if already playing).

- [ ] Steps: port tests (resume from disk without network; offline plays cached favorites; already playing → no-op); `CarAutoplayTest.appearingDeviceShufflesOnce`; FAIL; implement; PASS; commit `feat(android): cold start, launcher shortcuts and car autoplay`.

### Task 12: Native screens and strings

Port `App/{ServerURLView,RootView,OfflinePlayerView}.swift`, `Player/OfflinePlayerModel.swift`, `Settings/*` (`OfflinePlayerTests`, `SettingsModelTests`, `LocalizationTests`).

**Files:**
- Create: `lark/ui/{ServerScreen.kt,CantConnectScreen.kt,OfflinePlayerScreen.kt,OfflinePlayerModel.kt,SettingsScreen.kt,SettingsModel.kt,BatteryGuide.kt,Theme.kt}`, `res/values*/strings.xml` (4 locales)
- Test: `lark/ui/{OfflinePlayerModelTest.kt,SettingsModelTest.kt,LocalizationTest.kt}` (every key in all four locales, same placeholders)

**Interfaces:**
- Consumes: `AppServices`, `PlayerEngine` events/messages only (the offline player never talks to the network).
- Produces: Settings with server (change + confirm), offline cache (usage, cap, sync now, clear), Stay Offline, car autoplay (pick car, test "Shuffle favorites"), battery guide (button → `ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS`, plain text "If music stops when the screen is off…", link to docs/android.md), about (version).

- [ ] Steps: port model tests; FAIL; implement models and Compose screens (dark theme from the web's colour tokens in `web/src/styles.css`); PASS; commit `feat(android): native screens and strings`.

### Task 13: Web side

**Files:**
- Modify: `web/src/native/bridge.ts` (type `platform?: "ios" | "android"`), `web/src/pages/SettingsPage.tsx` (label `settings.nativeApp` → per-platform keys `settings.nativeAppIos` / `settings.nativeAppAndroid`, neutral fallback), locales, `ios/Sources/Bridge/BridgeController.swift` (add `platform:"ios"`), comments saying "iPhone app" where both apply
- Test: `web/src/pages/settings.test.tsx` (Android label shown when `larkNative.platform==="android"`), iOS `BridgeWebViewTests` (platform key present)

- [ ] Steps: tests; FAIL; implement; PASS (web + iOS CI); commit `feat(web): name the native app per platform`.

### Task 14: Emulator smoke test and CI emulator job

**Files:**
- Create: `android/app/src/androidTest/kotlin/lark/SmokeFlowTest.kt`, `android/scripts/emulator-smoke.sh` (starts `web/e2e/start-server.sh` in demo mode on the private Mac, boots AVD `lark_api35`, runs `connectedDebugAndroidTest`)
- Modify: `.github/workflows/ci.yml` (`android-emulator` job: ubuntu, KVM, `reactivecircus/android-emulator-runner`, x86_64 API 35, runs on `android/**` changes)

**Interfaces:**
- Consumes: the e2e server at `http://10.0.2.2:<port>` with demo data.

- [ ] Steps: `SmokeFlowTest` — enter server URL, sign in (e2e credentials), play a favorite, `MediaController` reports playing; press Home → still playing after 10 s; pause/next through the controller; disable networking (`svc wifi disable` via UiAutomation) → offline favorites play from cache; run on the private Mac emulator — PASS; then in CI — PASS; commit `test(android): emulator smoke test, locally and in CI`.

### Task 15: Signing, release job, docs

**Files:**
- Create: `docs/android.md` (install via GitHub/Obtainium, connect, settings, car autoplay setup, battery/vendor guide with dontkillmyapp.com links, updates, troubleshooting), `android/fastlane/metadata/android/{en-US,zh-CN,zh-TW,sv-SE}/{title.txt,short_description.txt,full_description.txt,changelogs/<versionCode>.txt,images/…}`
- Modify: `.github/workflows/release.yml` (`apk` job: build release with `-PlarkVersion=$VERSION`, sign with secrets `ANDROID_KEYSTORE_B64`, `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS`, `ANDROID_KEY_PASSWORD`; `apksigner verify --print-certs` matches the stored certificate SHA-256; `aapt2 dump badging` shows package, versionName, versionCode; attach `Melarka-X.Y.Z.apk`), `README.md` (Android section, screenshots row), `CHANGELOG.md`, `docs/superpowers/specs/android-client-design.md` (as built)

- [ ] **Step 1:** On the private Mac: `keytool -genkeypair -v -keystore ~/melarka-release/melarka-release.jks -alias melarka -keyalg RSA -keysize 4096 -validity 36500` with generated passwords; record the certificate SHA-256 in `android/signing-cert.sha256` (public). Set the four GitHub secrets from the private Mac with `gh secret set`. Hand the owner the passwords on the private Mac for Apple Passwords, and ask them to email the `.jks` (without the passwords) to themselves. Verify: `gh secret list` shows the four names.
- [ ] **Step 2:** Add a `workflow_dispatch` input `dry_run` to `release.yml` that builds and signs the APK without publishing; run it; download the artifact; on the emulator, `adb install` it fresh (debug and release keys differ, so uninstall any debug build first) and check it launches and connects to the e2e server. Expected: install succeeds, `apksigner` certificate SHA-256 equals `android/signing-cert.sha256`.
- [ ] **Step 3:** Write docs; commit `feat(android): signed release APK, docs and store metadata`.

### Task 16: First release and F-Droid

- [ ] **Step 1:** Release the next version (both versions bumped) through the gated script; check the APK on the GitHub Release installs on the emulator and connects to the live server.
- [ ] **Step 2:** Make the release build reproducible (pinned JDK/build-tools in the F-Droid recipe; no timestamps); verify by building twice on the private Mac and comparing with `apksigcopier compare`.
- [ ] **Step 3:** Open the fdroiddata merge request (`metadata/io.github.aaronsuns.melarka.yml` with `Binaries:`, `AllowedAPKSigningKeys:`, `AntiFeatures: NonFreeNet`); record the link in `docs/android.md`; commit `docs(android): F-Droid`.
