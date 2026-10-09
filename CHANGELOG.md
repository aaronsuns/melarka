# Changelog

All notable changes to Melarka are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html). The server, the web app and
the iPhone app share one version number.

## [Unreleased]

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

[Unreleased]: https://github.com/aaronsuns/melarka/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/aaronsuns/melarka/releases/tag/v0.1.1
[0.1.0]: https://github.com/aaronsuns/melarka/releases/tag/v0.1.0
