# Changelog

All notable changes to Melarka are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html). The server, the web app and
the iPhone app share one version number.

## [Unreleased]

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
  songs.

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

[Unreleased]: https://github.com/aaronsuns/melarka/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/aaronsuns/melarka/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/aaronsuns/melarka/releases/tag/v0.1.1
[0.1.0]: https://github.com/aaronsuns/melarka/releases/tag/v0.1.0
