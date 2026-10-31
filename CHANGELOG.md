# Changelog

All notable changes to Melarka are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html). The server, the web app, the iPhone app and the Android app share one version number.

## [Unreleased]

## [0.2.19] - 2026-10-31

### Added

- **Hold to fast-forward and rewind** in the iPhone and Android apps: holding the next or previous
  button on a car's steering wheel or on headphones scans through the song
  (about 10× at first, 30× after 3 seconds) and it plays on from where you let go. A short press
  still skips to the next or previous song. Rewinding stops at the start of the song; fast-forward
  stops just before its end, so the next song starts as usual.
- **Keep the iPhone app signed from a Mac, without SideStore**: `ios/scripts/resign.sh install`
  sets up a background job on a Mac with Xcode. Before the free Apple ID's 7 days run out, and
  while the Mac is unlocked (macOS keeps the Apple ID from Xcode while it is locked), it builds the
  newest release, signs it again and installs it over the app on the phone, by USB or Wi-Fi,
  keeping your data and sign-in. It shows a Mac notification when it needs you. See
  docs/iphone.md, "Re-sign from a Mac".

### Changed

- **Updated dependencies**: newer Go, Node and Alpine base images, Go modules, web libraries
  (React Router 8 among them) and CI actions.

## [0.2.18] - 2026-10-20

### Fixed

- **The iPhone app could start playing by itself** with the phone lying untouched: when iOS woke
  the app in the background to refresh the offline cache, "When Melarka opens: Shuffle my
  favorites" started the music as if you had opened it. That choice now waits until you actually
  open the app. The car's play over Bluetooth still starts your favorites in the background and offline.

## [0.2.17] - 2026-10-18

### Changed

- **The Android app is ready for F-Droid**: the build is reproducible (F-Droid's build matches the
  GitHub APK byte for byte, so both install as the same app and update each other), the store texts
  sit where F-Droid reads them, and the signed APK no longer carries Google's dependency-metadata block.

### Fixed

- **Next could play the same favorite again** (iPhone and Android): after playing one favorite from
  the page, the favorites that follow could start with that same song. They now never do.

## [0.2.16] - 2026-10-18

### Added

- **The Android app**: the web app full screen with a native player (Media3). Background playback
  with lock-screen, notification, headphone and car controls, gapless transitions, an offline cache of
  your favorites, launcher shortcuts (Shuffle Favorites, Resume), car autoplay when the Bluetooth
  connects, and the current lyric line on the lock screen. Every release now has a signed
  `Melarka-X.Y.Z.apk` (Android 8.0 or later); see docs/android.md for installing, the signing
  fingerprint and the battery settings. Automation apps (Tasker and others) can start Shuffle
  Favorites or Resume.

### Changed

- **Traditional Chinese in the iPhone app** is now written in Traditional characters with Taiwan
  wording (it used Simplified characters before).
- The web's settings entry names the app it runs in (iPhone app settings or Android app settings).

## [0.2.15] - 2026-10-17

### Changed

- **保留到音乐 keeps the song playing**: kept while it plays, the song goes on in the music player
  from the same second, and after it 随机播放收藏 (keeping it made it a favorite). Closing a preview
  without keeping it brings back the music that was playing before.

## [0.2.14] - 2026-10-17

### Added

- **下载 asks when the library seems to have the song already**: another upload or version of a
  song you have (same title and singer, simplified or traditional) brings up 库里可能已有这首歌
  with the matching songs, and 仍然下载 or 取消. Downloading the very same video again was
  already recognised (renamed songs and moved files included).

## [0.2.13] - 2026-10-17

### Added

- **Search your favorites**: Library → 收藏 has a search box (title, artist, album, tags,
  keywords, pinyin). Tapping a favorite plays it, then the queue goes on as 随机播放收藏.

## [0.2.12] - 2026-10-17

### Added

- **Make a song's lyrics yourself** (制作歌词, in the lyrics view): paste the words found online,
  then the song plays from the top and you tap as each line starts (撤销 takes a tap back, 重来
  starts over); 保存 makes them the song's lyrics for everyone, timed like any other: they
  align, show in the car, and follow on every device. A pasted .lrc can be saved as it is.

## [0.2.11] - 2026-10-17

### Added

- **Better tagging from YouTube**: a downloaded song keeps what YouTube says about it (the
  uploader's tags, YouTube's categories, the description), read in the same yt-dlp run; songs
  downloaded earlier get it in the background. The tagging agent now sees it, with the video's
  original title and channel, and a song whose details arrive later goes back to the agent.
- **Search keywords**: the agent can give a song free-form keywords (the film or drama it is from,
  the original singer, another name). Search finds songs by them; the Tags tab doesn't list them.

## [0.2.10] - 2026-10-17

### Added

- **Add the playing song to a playlist from Now Playing**: a 添加到歌单 button next to the heart.

## [0.2.9] - 2026-10-17

### Changed

- **A tidier, easier app**: the tab bar has an icon over each label; on a wide window it becomes a
  sidebar and the page a centred column instead of stretching across the screen. The home quick
  actions and every shuffle button use the app's own icons instead of emoji, Continue playing
  comes before For you, song lists show a quality badge only for lossless songs, and 我的 groups
  its settings into cards.

## [0.2.8] - 2026-10-17

### Added

- **Play a whole YouTube playlist without downloading it**: a playlist in the search results has a
  ▶ button that plays its songs straight away, each from its preview, the next one started
  ahead; in the iPhone app it keeps playing in the background, on the lock screen and in the car.
  Now Playing shows the video's thumbnail and a 下载 button that keeps the song; favorites,
  lyrics and the server's queue are for library songs only, so a YouTube queue is not synced.

## [0.2.7] - 2026-10-17

### Added

- **View and like counts on YouTube results**: search results, 视频 cards and For you show
  YouTube's counts, compact in your language ("35.4万次播放 · 4000赞", "354.2K views · 4K likes").
  Searches always have views; likes show once Melarka has read them (For you's top songs).

## [0.2.6] - 2026-10-17

### Fixed

- **Downloading a YouTube search result works again**: 0.2.5's search results carry view counts,
  which the download request refused ("invalid JSON body").

## [0.2.5] - 2026-10-17

### Changed

- **YouTube search results weigh in view counts**: YouTube's order stays the main signal, but a
  far more watched upload (the official one, the version everyone plays) moves up a few places.
  The same goes for the video Melarka picks for a song behind the scenes.
- **For you favours well-made songs**: the top candidates are weighed by YouTube's view count
  and like ratio (fetched in the background and kept for 30 days), so a popular, well-liked song
  beats an obscure upload that matches your taste just as well.

### Fixed

- **Lyrics for songs Last.fm calls instrumental**: Last.fm tags often describe the singer, not
  the song, so its "instrumental" no longer stops the lyrics lookup (folder rules, the agent and
  your own tags still do).

## [0.2.4] - 2026-10-17

### Added

- **Downloads stop before the disk fills**: with less than `downloads.min_free_gb` (10 GB) free
  on the download target, a new YouTube download is refused and a queued one fails with "not
  enough disk space"; retry it once there is room.
- **The cover cache cleans itself**: covers of songs gone from the library, and temp files left
  by a crash, are removed at start and then daily.

### Fixed

- **Many more songs get lyrics**, above all YouTube downloads: a release whose title and singer
  match is now taken even when its duration is up to a minute off (a video's intro or outro;
  the closest version still wins, and the offset fixes the timing). NetEase, QQ Music and Kugou
  look at 10 search results instead of 5 and fetch the closest versions first. Songs that found
  no lyrics before are looked up again.
- **Lyrics of another song with the same title**: two different Western singers no longer count
  as the same song just because the durations agree.

## [0.2.3] - 2026-10-17

### Added

- **One-command install**: `deploy/install.sh` sets up and starts Melarka with Docker Compose
  and prints how to sign in; running it again upgrades. The music folder is optional (without
  one, songs go in `music/` in the install directory). `.env` can now pin the release
  (`MELARKA_VERSION`, otherwise `latest`), open Melarka to the home network (`MELARKA_BIND`) and
  move the port (`MELARKA_PORT`).

### Changed

- **Lyrics can be shifted by up to 5 minutes** either way (was 30 seconds), for songs with a long
  intro or lyrics timed for another version.
- **Newer image base**: Alpine 3.24 with ffmpeg 8.1; Go and web dependencies updated.

### Fixed

- **Car lyrics follow an alignment at once**: shifting or aligning lyrics, or switching to other
  lyrics, in the iPhone app now moves the line shown in the car and on the lock screen too.
  Before, the car kept the old timing until the song was played again, and for cached songs it
  kept it for good. Cached songs also pick up changes made on another device the next time they
  play online.

## [0.2.2] - 2026-10-16

### Added

- **Stay Offline in the iPhone app** (for a flight): a switch in the offline player, on the
  "can't connect" screen and in Settings. While it is on, the app tries nothing on the network
  (no page reload, stream retries, queue refills, queue or play-event uploads, lyrics or covers,
  syncs or prefetches), even on airplane Wi-Fi with no internet, and opens straight into the
  offline player. It stays on until you turn it off; then everything that waited goes.
- **Lyrics offline in the iPhone app**: lyrics are kept with each cached song (and fetched for
  songs cached earlier at the next favorites sync). The offline player shows the line being sung
  and the next one; a tap opens the whole lyrics, following the song, where a tap on a line jumps
  there. The lock screen and car show cached lyrics without the network too.
- **A tidier offline player**: one compact row at the top (title, state, and round Stay Offline,
  retry and close buttons) instead of three blocks of text, and a now-playing card tinted with
  the song's colour.

### Changed

- **For you picks from what you really like**: seeds used to be the most played songs of the
  last 30 days and the newest favorites, which in practice meant recent downloads (they are
  favorited automatically). Now every favorite and every song you listen to has a score: songs
  played to the end again and again count most, songs skipped early count against, and listening
  fades over 90 days. The five best are always seeds, the other five a daily draw from the rest,
  at most two per artist; and up to 20 library songs a day are looked up on YouTube (was 10).

### Fixed

- **A track stuck at 0:00 while "playing"**: in browsers whose media engine can stall with the
  audio already downloaded (WebKit on Linux), the web player now reloads the track where it
  stands after 2.5 s, at most twice per track, instead of showing a frozen track (not while the
  page is hidden or a network retry is pending).
- **Now Playing's background is cheap**: the blurred artwork is painted once into a tiny canvas
  and stretched, instead of a large CSS blur over the whole screen. Same look; about half the
  memory in WebKit (1.1 GB to 0.5 GB on Linux), which matters on older iPhones.

## [0.2.1] - 2026-10-10

### Added

- **YouTube search: show more and open playlists**: "Show more" under the videos loads the next
  ten (up to 50), and a playlist result opens in place to list its tracks, each with Preview and
  Download.
- **⏮ in the mini player**: the bottom bar is now ⏮ ▶ ⏭, as in Now Playing (in the first 3 s
  the previous track, later a restart). The episode bar is ⏮ ▶ ⏭ ✕ the same way; the 15/30 s
  skips stay in the episode's Now Playing.
- **Delete all broken files**: Admin → Library → Broken files has a "Delete all" button. After
  a confirmation with the exact count, every broken file goes to the trash, restorable for 30
  days like a file deleted on its own. The list now shows every broken file, also ones the admin
  disliked. API (admin only): `GET /api/v1/admin/broken-tracks` (`items`, `total`) and
  `DELETE /api/v1/admin/broken-tracks?expect=N`, which moves nothing (409) if the count changed.
- **Offline player in the iPhone app**: when the server can't be reached, "Play Offline
  Favorites" opens a native player with the cover, a progress slider, previous, play/pause and
  next, shuffle and repeat, the queue and the cached favorites. "Retry Connection" goes back to
  the web app once the server answers, and the music plays on. Covers are now cached with the
  songs, so the lock screen and the car show them offline too.

### Changed

- **A new look, players first**: the players' buttons are clean filled glyphs (play and pause
  without a ring, double-arrow previous and next, clock arrows for the 15/30 s skips), with a
  44 px or larger tap target and a press effect. Now Playing shows the artwork blurred and
  darkened behind everything (the cover's own colours when a song has none), a thinner seek bar,
  icons in place of emoji for favorite, not for me, edit, tags, lyrics and delete, and on a wide
  screen one centred column. The mini player is a raised card on a translucent bar with a thin
  progress line. The rest of the app gets one type scale, spacing, corner radii, list separators,
  quieter secondary buttons and switches in Settings. Reduced motion is respected.
- **Swipe to change track**: on the mini player, and on Now Playing's cover and title, swipe
  left for the next track and right for the previous one (the same as ⏮: in the first 3 s the
  previous track, later a restart). The content follows the finger and springs back; vertical
  scrolling, buttons and the seek bar are unaffected. Episodes work the same way.

### Fixed

- "Continue listening" no longer appears after you pause music. It is offered only when a
  channel episode is what played last; once music has played, the bar shows the paused music
  and the episode is resumed from Channels. This holds after a reload and in the iPhone app.

## [0.2.0] - 2026-10-10

### Added

- **Shuffle and repeat** (all, one) in Now Playing and the iPhone app; car head units and Siri
  that support it can change them too.
  With repeat off the queue still never stops: radio and favorites refills carry on as before.
  The choice is kept per device.
- **Sleep timer**: 15, 30, 45 or 60 minutes, or the end of the current track, with a 10-second
  fade (Now Playing → ⋯). The iPhone app runs it natively, so it works while the phone is
  locked.
- **Volume normalization**: the server measures each track's loudness in the background, and
  players turn loud tracks down to about -14 LUFS (never up). On by default; a switch in
  Settings.
- **Queue editing**: "Add to queue", drag to reorder, swipe or ✕ to remove, for music and
  channel episodes.

### Changed

- **Server**: after the upgrade the server measures the loudness of the whole library once, one
  song at a time with ffmpeg at low priority, pausing while it prepares streams; new songs are
  measured within minutes. The `loudness.*` settings in `config.yaml` turn it off or space it
  out (see the configuration guide).

### Fixed

- The lyrics view no longer shows a horizontal scrollbar, or a vertical one on desktop.
- iPhone app: a track handed over with a start position could begin playing from the old
  position before its seek landed.
- Test reliability: the end-to-end tests no longer reach the Internet (local covers and a guard
  that fails any outside request), flaky timing-dependent tests are deterministic, and CI pulls
  its base images from a mirror instead of a rate-limited registry.

### Notes

- In Safari on iPhone (without the app) the browser ignores volume changes, so normalization and
  the fade are inaudible there; the timer still pauses.

## [0.1.1] - 2026-10-09

### Changed

- **iPhone app**: native screens are now English or Chinese, following the phone language. The
  first-launch server screen, the native settings, the car autoplay guide, the "can't connect"
  screen, alerts, and the Shortcuts actions and Siri phrases are in Chinese on a phone set to
  Chinese and in English on any other phone. The web app inside keeps its own language setting.

## [0.1.0] - 2026-10-09

The first public release.

### Added

- **Server**: one Docker image (`ghcr.io/aaronsuns/melarka`, linux/amd64 and linux/arm64) with
  ffmpeg and yt-dlp, configured with `LARK_*` environment variables and `config.yaml`.
- **Streaming** in three tiers (`lossless`, `high`, `saver`), transcoded on the fly and cached.
- **Web app** in English, 简体中文, 繁體中文 and svenska that installs to a phone's home screen,
  with lock-screen controls and an offline cache of favorites (with a server-side kill switch).
- **iPhone app** with a native playback engine, gapless playback, an offline cache, Shortcuts
  actions for starting playback in the car, and the lyric line on the lock screen; distributed as
  an unsigned IPA through a SideStore source.
- **Library**: scanning and watching music folders, full-text search, favorites, playlists,
  personal radio, play history, pending songs that are kept after three plays, and a 30-day trash.
- **YouTube**: search, single and playlist downloads (up to 200 entries), My downloads, and an
  automatically updated yt-dlp.
- **For you**: nightly YouTube suggestions based on each user's plays and favorites, with
  optional Last.fm similar tracks.
- **Channels (频道)**: follow YouTube channels without an account, with automatic episode
  downloads, resume, playback speed, expiry, Keep, and channel suggestions.
- **Previews**: play any YouTube video before keeping it in the music library or in Channels.
- **Video (视频)**: search and watch YouTube videos through the server, with related videos,
  proxied thumbnails, personal history and suggestions; the phone never contacts YouTube directly.
- **Lyrics** from files, `.lrc` files, LRCLIB and optional unofficial providers, with shared
  timing fixes (shift and align to a line), wrong-lyrics reports with undo, and admin tools for
  choosing, searching and restoring lyrics.
- **Covers** from files, folders, iTunes and optional unofficial providers.
- **Song names fixer**: title, artist, album and year overrides, cleaned-up YouTube titles for
  lookups, and a review list for the names agent skill.
- **Tags** from a fixed vocabulary, filled by folder rules, Last.fm, an agent skill or by hand.
- **Agent skills** for tagging, finding missing lyrics and fixing song names.
- **Admin console** for users, pending songs, downloads, trash, libraries and yt-dlp updates.
- **Version reporting** in `lark --version`, `GET /api/v1/info` and the settings pages.

[Unreleased]: https://github.com/aaronsuns/melarka/compare/v0.2.19...HEAD
[0.2.19]: https://github.com/aaronsuns/melarka/compare/v0.2.18...v0.2.19
[0.2.18]: https://github.com/aaronsuns/melarka/compare/v0.2.17...v0.2.18
[0.2.17]: https://github.com/aaronsuns/melarka/compare/v0.2.16...v0.2.17
[0.2.16]: https://github.com/aaronsuns/melarka/compare/v0.2.15...v0.2.16
[0.2.15]: https://github.com/aaronsuns/melarka/compare/v0.2.14...v0.2.15
[0.2.14]: https://github.com/aaronsuns/melarka/compare/v0.2.13...v0.2.14
[0.2.13]: https://github.com/aaronsuns/melarka/compare/v0.2.12...v0.2.13
[0.2.12]: https://github.com/aaronsuns/melarka/compare/v0.2.11...v0.2.12
[0.2.11]: https://github.com/aaronsuns/melarka/compare/v0.2.10...v0.2.11
[0.2.10]: https://github.com/aaronsuns/melarka/compare/v0.2.9...v0.2.10
[0.2.9]: https://github.com/aaronsuns/melarka/compare/v0.2.8...v0.2.9
[0.2.8]: https://github.com/aaronsuns/melarka/compare/v0.2.7...v0.2.8
[0.2.7]: https://github.com/aaronsuns/melarka/compare/v0.2.6...v0.2.7
[0.2.6]: https://github.com/aaronsuns/melarka/compare/v0.2.5...v0.2.6
[0.2.5]: https://github.com/aaronsuns/melarka/compare/v0.2.4...v0.2.5
[0.2.4]: https://github.com/aaronsuns/melarka/compare/v0.2.3...v0.2.4
[0.2.3]: https://github.com/aaronsuns/melarka/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/aaronsuns/melarka/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/aaronsuns/melarka/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/aaronsuns/melarka/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/aaronsuns/melarka/releases/tag/v0.1.1
[0.1.0]: https://github.com/aaronsuns/melarka/releases/tag/v0.1.0
