# Bilibili as a second video source: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an admin enable YouTube, Bilibili, both or neither as video sources, and make search, playlist import, download, preview and playback work for Bilibili videos.

**Architecture:** `internal/ytdlp` learns a source from the video id's shape (YouTube 11 chars, Bilibili `BV` + 10). A new `internal/videosrc` service stores which sources are on (env default, admin override in SQLite). Handlers and background workers check it; the web app reads it from `/info`.

**Tech Stack:** Go (chi, SQLite, yt-dlp behind `ytdlp.Runner`), React/TypeScript (Vitest, Playwright), Swift and Kotlin clients.

**Spec:** `docs/superpowers/specs/2026-11-19-bilibili-source-design.md`

## Global Constraints

- Only `internal/ytdlp` runs yt-dlp; every URL passes `ValidURL` first.
- Tests never touch the network: fakes, `httptest`, fixtures under `testdata/`.
- Default `video_sources` is `youtube`: an upgraded install behaves exactly as before.
- No `source` column and no id rewrite; the source is read from the id.
- Disabled source: no outbound request, API answers `409` code `source_disabled`.
- Existing library items, files, subscriptions and episodes stay visible and playable when a source is turned off.
- New `LARK_*` variable: default in `internal/config`, a line in `deploy/config.example.yaml` (or env-only per repo rule), a row in `docs/configuration.md` (`internal/config/docs_test.go` enforces).
- UI text keys go to `en.json` first, then `zh-Hans`, `zh-Hant`, `sv`; new API error codes get an `error.<code>` key; new admin routes go in `TestAdminRoutesForbiddenForMembers`.
- Migrations append-only: the next number is `0033`.
- Commits: `type(scope): summary`. Before each commit run `go vet ./... && go test ./...`; for web tasks also `cd web && npm test && npm run build`.
- No email addresses or real tokens in code, fixtures or docs.

## Deviation from the spec

The spec says to widen `IsVideoID` and list its callers. This plan instead **keeps `IsVideoID` YouTube-only** and adds `IsAnyVideoID` for the shared pipeline (download, preview, thumbnails, adopt, watch page). Channels, recommendations and discovery then reject Bilibili ids with no edits, which is the spec's intent ("those code paths never see a Bilibili id"). Task 9 updates the spec to match.

## Review Focus

- A Bilibili id sent to a YouTube-only route (follow channel, episode, recommendation) is rejected, not stored. Pinned in Task 4.
- Hostile hosts: `bilibili.com.evil.test`, an explicit port, userinfo, a leading `-`. Pinned in Task 1.
- A pasted Bilibili link with `?p=2` or `&list=` queues video part 1 only. Pinned in Task 1.
- A direct API call for a disabled source runs yt-dlp zero times. Pinned in Task 4.
- `LARK_VIDEO_SOURCES` junk, `none`, duplicates, spaces. Pinned in Task 3.
- Bilibili HTTP 412 shows a retry message and is not cached. Pinned in Task 2 and Task 4.

---

### Task 1: Source identity and URLs (`internal/ytdlp/urls.go`)

**Files:**
- Modify: `internal/ytdlp/urls.go`, `internal/ytdlp/ytdlp.go` (`allowedHosts`)
- Test: `internal/ytdlp/urls_test.go`, `internal/ytdlp/ytdlp_test.go`

**Interfaces:**
- Produces:
  - `type Source string`; `const SourceYouTube Source = "youtube"`, `SourceBilibili Source = "bilibili"`.
  - `func IsBilibiliID(id string) bool` (`^BV[0-9A-Za-z]{10}$`).
  - `func IsAnyVideoID(id string) bool` (`IsVideoID(id) || IsBilibiliID(id)`).
  - `func SourceOf(id string) (Source, bool)`; `func SourceOfURL(rawURL string) (Source, bool)` (by host, after `ValidURL`).
  - `WatchURL(id)` returns `https://www.bilibili.com/video/<id>` for a Bilibili id; `ThumbnailURL(id)` returns `""` for a Bilibili id (its cover is not derivable).
  - `VideoIDFromURL` also reads `bilibili.com/video/BV…` and `m.bilibili.com/video/BV…`, ignoring the query (part 1 only).
  - `func IsBilibiliListURL(rawURL string) bool` (host `space.bilibili.com`, or path starting `/medialist/` or `/list/`, or a `collectiondetail`/`favlist`/`seriesdetail` page).
- `IsVideoID` is unchanged and stays YouTube-only.

- [ ] **Step 1: Write failing tests.** `TestIsAnyVideoID` (`dQw4w9WgXcQ` yes; `BV1GJ411x7h7` yes; an 11-char id starting `BV` is YouTube, `SourceOf` says youtube; 12-char non-BV no), `TestSourceOf`, `TestWatchURLBilibili`, `TestThumbnailURLBilibiliEmpty`, `TestVideoIDFromURLBilibili` (with `?p=2` and `?spm_id_from=x` both give the bare BV id), `TestValidURLBilibiliHosts` (accepts `bilibili.com`, `www.`, `m.`, `space.`; rejects `bilibili.com.evil.test`, `www.bilibili.com:8080`, `-https://www.bilibili.com/video/BV1GJ411x7h7`, `ftp://…`; userinfo is stripped), `TestIsBilibiliListURL`.
- [ ] **Step 2: Run** `go test ./internal/ytdlp -run 'AnyVideoID|SourceOf|Bilibili'`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement** the interface above. Add `bilibili.com`, `www.bilibili.com`, `m.bilibili.com`, `space.bilibili.com` to `allowedHosts`. `b23.tv` is **not** added (the spec's rule: only if yt-dlp resolves it itself; v1 leaves it out and the web message asks for the full link).
- [ ] **Step 4: Run** `go test ./internal/ytdlp`. Expected: PASS (existing YouTube tests unchanged).
- [ ] **Step 5: Commit** `feat(ytdlp): Bilibili source identity, URLs and allow-list`.

### Task 2: Bilibili search, resolve and details (`internal/ytdlp`)

**Files:**
- Modify: `internal/ytdlp/ytdlp.go` (`entryToVideo`, new `SearchSource`), `internal/ytdlp/channels.go` (`Info`, `infoTemplate`, `VideoInfo`), `internal/ytdlp/errors.go`
- Create: `internal/ytdlp/testdata/bilisearch.json`, `internal/ytdlp/testdata/bilifavlist.json`
- Test: `internal/ytdlp/ytdlp_test.go`, `internal/ytdlp/errors_test.go`

**Interfaces:**
- Consumes: Task 1 (`IsAnyVideoID`, `Source`, `WatchURL`).
- Produces:
  - `func (c *Client) SearchSource(ctx context.Context, src Source, q string, n int) (videos []Video, more bool, err error)`: for `SourceYouTube` it is `SearchMore`; for `SourceBilibili` it runs `bilisearch<n>:<clean>` with the same flat-playlist args and no playlist search.
  - `entryToVideo` accepts any id for which `IsAnyVideoID` holds; for a Bilibili id `Video.Thumbnail` is the entry's own cover (may be empty), `ChannelID` is empty.
  - `Info` gains `Thumbnail string`; `infoTemplate` adds `thumbnail`; `VideoInfo` accepts `IsAnyVideoID`.
  - `var ErrBilibiliThrottled = errors.New("ytdlp: bilibili is throttling (HTTP 412)")` returned by a new `ClassifyBilibili(err error) error` that maps stderr containing `HTTP Error 412` to it, and region/removal messages to the existing unavailable class. `PushBack(err)` returns true for it.
  - `ResolveList` works for Bilibili list URLs (no `--no-playlist` unless `VideoIDFromURL(u) != ""`, which already holds).
- [ ] **Step 1: Record fixtures.** Hand-write small fixtures in yt-dlp `-J` shape: `bilisearch.json` (a playlist with 3 `ie_key: BiliBili` entries, ids `BV…`, one with a `thumbnails` array, one without) and `bilifavlist.json` (a playlist with `id`, `title`, 2 entries). Fake ids only.
- [ ] **Step 2: Write failing tests** with the existing fake Runner: `TestSearchSourceBilibiliArgv` (argv contains `bilisearch10:<q>` and `--flat-playlist`, no `ytsearch`), `TestSearchSourceBilibiliParses` (3 videos, URLs are `https://www.bilibili.com/video/BV…`, empty `ChannelID`), `TestSearchSourceYouTubeDelegates`, `TestResolveListBilibiliFavlist`, `TestVideoInfoBilibili` (returns the `Thumbnail`), `TestClassifyBilibili412` (stderr `HTTP Error 412: Precondition Failed` → `ErrBilibiliThrottled`, and `PushBack` is true).
- [ ] **Step 3: Run** `go test ./internal/ytdlp`. Expected: new tests FAIL.
- [ ] **Step 4: Implement** the interface above. Apply `ClassifyBilibili` inside `SearchSource` and `ResolveList` for the Bilibili branch only.
- [ ] **Step 5: Run** `go test ./internal/ytdlp`. Expected: PASS.
- [ ] **Step 6: Commit** `feat(ytdlp): Bilibili search, list resolve, details and throttling error`.

### Task 3: Which sources are on (`internal/videosrc`, config, admin API, `/info`)

**Files:**
- Create: `internal/videosrc/videosrc.go`, `internal/videosrc/videosrc_test.go`, `internal/db/migrations/0033_video_sources.sql`, `internal/api/video_sources.go`
- Modify: `internal/config/config.go`, `deploy/config.example.yaml`, `docs/configuration.md`, `internal/api/server.go` (field `VideoSources *videosrc.Service`, route registration), `internal/api/admin_handlers.go` (route), `internal/api/prefs_handlers.go` (`info`), `cmd/lark/main.go` (construct and pass), `internal/api/admin_handlers_test.go`
- Test: `internal/config/config_test.go`, `internal/api/video_sources_test.go`

**Interfaces:**
- Consumes: `ytdlp.Source`.
- Produces:
  - `func Parse(s string) ([]ytdlp.Source, error)`: comma-separated, trimmed, lower-cased, de-duplicated, order kept; `"none"` gives an empty list; `""` or an unknown name is an error.
  - `type Service struct { DB *sql.DB; Default []ytdlp.Source }`
  - `func (s *Service) List(ctx context.Context) (sources []ytdlp.Source, setBy string, err error)` (`setBy` is `"admin"` when the row exists, else `"env"`).
  - `func (s *Service) Enabled(ctx context.Context, src ytdlp.Source) bool` (a DB error counts as the default).
  - `func (s *Service) Set(ctx context.Context, srcs []ytdlp.Source) error` (replaces the row; empty list allowed).
  - `config.Config.VideoSources []string` read from `LARK_VIDEO_SOURCES`, default `["youtube"]`; a bad value is a startup error naming the variable.
  - Routes: `GET /admin/video-sources` returns `{"sources":[…],"set_by":"env"|"admin"}`; `PUT` takes `{"sources":[…]}`, `400 bad_request` for an unknown name, answers the same shape.
  - `GET /info` gains `"video_sources": [...]` (always an array, never null).
- [ ] **Step 1: Write failing tests.** `TestParse` (table: `youtube`, `Bilibili, youtube`, `youtube,youtube`, `none`, ``, `vimeo`, `youtube;bilibili`), `TestServiceDefaultThenAdminOverride`, `TestServiceEmptyListIsValid`, config test for the env default and error, API tests: admin GET/PUT round trip, member gets 403 on both (add both routes to `TestAdminRoutesForbiddenForMembers`), `PUT {"sources":["vimeo"]}` is 400, `/info` lists `["youtube"]` by default and `[]` after `PUT {"sources":[]}`.
- [ ] **Step 2: Run** `go test ./internal/videosrc ./internal/config ./internal/api -run 'Parse|VideoSources|Info'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Migration: `CREATE TABLE video_sources (id INTEGER PRIMARY KEY CHECK (id = 1), sources TEXT NOT NULL)` (comma-separated, empty string for none). Add the config row and example line.
- [ ] **Step 4: Run** `go vet ./... && go test ./...`. Expected: PASS, including `internal/config/docs_test.go`.
- [ ] **Step 5: Commit** `feat(videosrc): admin-chosen video sources, LARK_VIDEO_SOURCES and /info`.

### Task 4: Gate and extend the API

**Files:**
- Modify: `internal/api/download_handlers.go`, `internal/api/preview_handlers.go`, `internal/api/video_handlers.go`, `internal/api/channel_handlers.go` (no change to its `IsVideoID` checks; add tests only), `internal/api/errors.go` (or wherever `writeCoded` codes live), `internal/download/store.go`, `internal/download/adopt.go`, `internal/download/lists.go`, `internal/preview/preview.go`, `internal/preview/thumbs.go`, `internal/ytdlp` (YT interface in `internal/api`)
- Test: `internal/api/api_test.go` and the matching `*_handlers_test.go` files, `internal/download/*_test.go`, `internal/preview/*_test.go`

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces:
  - `func (s *Server) requireSource(w http.ResponseWriter, r *http.Request, src ytdlp.Source) bool` (writes `409 source_disabled` and returns false; `s.VideoSources == nil` means YouTube only).
  - `GET /youtube/search?source=youtube|bilibili`: default is the first enabled source; none enabled gives `409 source_disabled`; unknown value `400 bad_request`. The cache key becomes `<source>:<query>`; the Bilibili branch calls `SearchSource` and returns `Playlists: []`.
  - `/youtube/playlist` and `/youtube/playlist/entries`, `POST /downloads` (URL form and video form), previews create, `/youtube/in-library`: source from the URL (`SourceOfURL`) or the id (`SourceOf`), then `requireSource`.
  - Error codes (each with `error.<code>` text in the web locales in Task 7): `source_disabled` (409), `bilibili_search_failed` (502), `bilibili_throttled` (503, message "Bilibili is busy, try again in a minute"), `bilibili_unavailable` (404, removed or region-locked).
  - Shared-pipeline id checks switch to `IsAnyVideoID`: `internal/download/adopt.go:23`, `internal/preview/preview.go` (the two checks), `internal/preview/thumbs.go:115`, `internal/api/video_handlers.go` (the two checks), and `createDownload`'s id comparison. Channel, episode, recommendation, discovery and mix code keeps `IsVideoID`.
  - Covers: `download.Service.insertOrExisting` no longer overwrites a Bilibili cover with `ThumbnailURL` (keep `v.Thumbnail` when the id is Bilibili); when it is empty, the download worker and the preview creator fill it from `VideoInfo(...).Thumbnail` once and store it. `internal/download/lists.go`'s list check also accepts `IsBilibiliListURL(u)` (the list id is yt-dlp's own id).
  - `episodeThumbnail` and similar `ThumbnailURL` redirects: unchanged (YouTube-only routes).
  - Preview thumbnails (`preview.Thumbs`): for a Bilibili id, fetch the stored cover URL (looked up from the downloads or previews row, else `VideoInfo`), accepted only if its host is `hdslb.com` or `*.hdslb.com` over https; cached on disk like YouTube's.
- [ ] **Step 1: Write failing tests** (fakes, `httptest`): `TestSearchBilibiliSource` (fake YT returns two videos; response has `Playlists: []`), `TestSearchDefaultSourceFollowsSettings`, `TestSearchSourceDisabledRunsNoYtdlp` (YouTube off, fake counts calls, expect 409 and 0 calls; same for Bilibili off), `TestSearchBilibili412NotCachedAndRetryCode`, `TestCreateDownloadBilibiliVideo` (stores the posted cover, URL canonical `https://www.bilibili.com/video/BV…`), `TestCreateDownloadBilibiliPart2LinkQueuesPart1`, `TestCreateDownloadDisabledSource409`, `TestBilibiliListURLCreatesDownloadList`, `TestPreviewBilibiliFillsCoverOnce`, `TestThumbProxyBilibiliHostAllowList` (a cover on `evil.test` is refused), `TestChannelRoutesRejectBilibiliID` (follow-by-video, episode stream, episode thumbnail all 400, nothing stored), `TestRecommendAndDiscoveryIgnoreBilibiliID`.
- [ ] **Step 2: Run** `go test ./internal/api ./internal/download ./internal/preview`. Expected: new tests FAIL.
- [ ] **Step 3: Implement** the gating helper first, then the search/playlist/download/preview/thumbnail changes, then the id-check swaps. Make the narrowest change at each call site named above.
- [ ] **Step 4: Run** `go vet ./... && go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(api): Bilibili search, download, preview and source gating`.

### Task 5: Workers idle when a source is off

**Files:**
- Modify: `internal/download/service.go` (worker), `internal/channels/service.go` and its poll/refresh loops, `internal/recommend/refresh.go`, `cmd/lark/main.go` (the tag back-fill at about line 217 and the wiring), `internal/api` (error text)
- Test: `internal/download/download_test.go`, `internal/channels/*_test.go`, `internal/recommend/*_test.go`

**Interfaces:**
- Consumes: `videosrc.Service.Enabled`.
- Produces: each of these takes `Sources interface{ Enabled(context.Context, ytdlp.Source) bool }` (nil means enabled, so existing tests keep passing): `download.Service`, `channels.Service`, `recommend` refresher, the tag back-fill. A cycle begins with `if !Sources.Enabled(ctx, ytdlp.SourceYouTube) { return }` (channels, recommendations, back-fill). The download worker checks `SourceOf(job.VideoID)` before it runs a job; a disabled source marks the job `failed` with code `source_disabled` and no yt-dlp call (retryable by the existing retry route). A job already running finishes.
- [ ] **Step 1: Write failing tests:** `TestDownloadWorkerFailsJobWhenSourceOff` (0 runner calls, status failed, error code `source_disabled`), `TestRetryAfterSourceBackOn`, `TestChannelsPollIdlesWhenYouTubeOff`, `TestRecommendRefreshIdlesWhenYouTubeOff`, `TestExistingEpisodesStillServedWhenYouTubeOff` (stream and thumbnail routes still answer 200).
- [ ] **Step 2: Run** the three packages' tests. Expected: new tests FAIL.
- [ ] **Step 3: Implement** and wire `VideoSources` into each in `main.go`.
- [ ] **Step 4: Run** `go vet ./... && go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(workers): idle and fail cleanly when a video source is off`.

### Task 6: Web: link parser, API client and types

**Files:**
- Create: `web/src/videoLink.ts`, `web/src/videoLink.test.ts`
- Modify: `web/src/youtubeLink.ts` (kept; the YouTube rules stay there), every importer of `parseYouTubeLink` (`web/src/pages/SearchPage.tsx` and others found with `grep -rn parseYouTubeLink web/src`), `web/src/api/client.ts`, `web/src/api/types.ts`

**Interfaces:**
- Produces:
  - `export interface VideoLink { source: "youtube" | "bilibili"; videoId: string | null; listUrl: string | null; isMix: boolean }`
  - `export function parseVideoLink(raw: string): VideoLink | null` (YouTube via `parseYouTubeLink`; Bilibili hosts `bilibili.com`, `www.`, `m.`, `space.` with the same rules as the server; `?p=` ignored; `b23.tv` returns null).
  - `export const bilibiliWatchUrl = (id: string) => string`.
  - `api.info()` type gains `video_sources?: ("youtube" | "bilibili")[]`; `api.youtubeSearch(q, n?, source?)` sends `source`; `api.adminVideoSources()` and `api.setAdminVideoSources(sources)`.
  - `YTVideo` gains an optional `source`, derived from the id by a helper `sourceOfId(id)` (`BV` + 10 chars is bilibili).
- [ ] **Step 1: Write failing tests** `videoLink.test.ts`: YouTube links unchanged; `https://www.bilibili.com/video/BV1GJ411x7h7?p=2` gives the bare id; a favourites URL gives a `listUrl`; `https://b23.tv/x` is null; `https://bilibili.com.evil.test/video/BV1GJ411x7h7` is null; client test that `youtubeSearch("x", undefined, "bilibili")` requests `source=bilibili`.
- [ ] **Step 2: Run** `cd web && npx vitest run src/videoLink.test.ts src/api/client.test.ts`. Expected: FAIL.
- [ ] **Step 3: Implement** the interface and switch the importers to `parseVideoLink`.
- [ ] **Step 4: Run** `cd web && npm test && npm run build`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): Bilibili links and API client`.

### Task 7: Web: search switch, hidden states, watch page, Admin card, text, e2e

**Files:**
- Create: `web/src/components/SourceSwitch.tsx`, `web/src/pages/admin/VideoSourcesCard.tsx`, `web/e2e/bilibili.spec.ts`
- Modify: `web/src/pages/SearchPage.tsx`, `web/src/components/YouTubeResults.tsx` (takes `source`), `web/src/pages/video/WatchPage.tsx` and `VideoPage.tsx`, the navigation and screens for channels, "For you" and related videos (wherever `info.channels` is read), `web/src/pages/admin/SystemPage.tsx`, `web/src/i18n/locales/{en,zh-Hans,zh-Hant,sv}.json`, `web/src/i18n/errors.ts`, `web/src/styles.css`

**Interfaces:**
- Consumes: Task 6 (`parseVideoLink`, `api.info`, `api.youtubeSearch`, `api.adminVideoSources`, `api.setAdminVideoSources`).
- Produces:
  - `SourceSwitch({ sources, value, onChange })`: renders only when `sources.length > 1`; the choice is remembered in `localStorage` key `lark.videoSource` (try/catch around storage).
  - Search: with one source enabled, no switch and that source is used; with none, the video results and the pasted-link box are hidden and a short note says video sources are turned off.
  - A pasted link for a disabled source shows `search.sourceOff` naming the source.
  - Watch page: a Bilibili id plays from the server preview video (`api.createPreview` with media `video`), no iframe; YouTube keeps its embed.
  - Channels, "For you", related videos: hidden when `video_sources` lacks `youtube` (same place `channels === false` hides 频道).
  - Admin → System card with two checkboxes and a note that YouTube is blocked in mainland China; shows whether the value is from the environment or saved by an admin.
  - Locale keys (all four languages): `search.sourceYouTube`, `search.sourceBilibili`, `search.sourceOff`, `search.noSources`, `admin.videoSources.title`, `admin.videoSources.help`, `admin.videoSources.fromEnv`, `admin.videoSources.fromAdmin`, `error.source_disabled`, `error.bilibili_search_failed`, `error.bilibili_throttled`, `error.bilibili_unavailable`. Never translate music metadata.
- [ ] **Step 1: Write failing tests:** `SourceSwitch` renders for two sources only and stores the choice; Search with `video_sources: ["bilibili"]` calls `youtubeSearch(..., "bilibili")` and shows no switch; with `[]` shows `search.noSources`; a YouTube link with YouTube off shows `search.sourceOff`; channels nav hidden when YouTube is off; Admin card saves via `setAdminVideoSources`; the locale test passes with the new keys.
- [ ] **Step 2: Run** `cd web && npm test`. Expected: new tests FAIL.
- [ ] **Step 3: Implement** the components and wiring, locale keys in `en` first.
- [ ] **Step 4: Playwright:** `bilibili.spec.ts` serves a Bilibili fixture through the same local fixture server `LARK_YOUTUBE_COVER_URL` uses, enables both sources through the admin API, searches, sees the switch, switches to Bilibili, sees results. Run `cd web && npm run build && npm run e2e`. Expected: PASS.
- [ ] **Step 5: Run** `cd web && npm test && npm run build`. Expected: PASS.
- [ ] **Step 6: Commit** `feat(web): Bilibili search switch, Admin video sources and hidden states`.

### Task 8: Native clients accept Bilibili ids

**Files:**
- Modify: `android/app/src/main/kotlin/lark/bridge/Item.kt` (`youtubeVideoId`), `android/app/src/main/kotlin/lark/api/LarkApi.kt` (id check near line 86); `ios/Sources/Player/PlaybackQueue.swift` (`youtubeVideoID`), `ios/Sources/API/LarkAPI.swift` (id check near line 53)
- Test: `android/app/src/test/kotlin/lark/bridge/WebMessageTest.kt` or the existing Item/LarkApi tests, `ios/Tests/` matching tests

**Interfaces:**
- Produces: the id regex on both platforms accepts `^[A-Za-z0-9_-]{11}$` or `^BV[0-9A-Za-z]{10}$`. The `yt:<id>` queue prefix, the "never cached, no lyrics, no play events" rules and `NoUnpromptedPlaybackTest` are unchanged.
- [ ] **Step 1: Write failing tests** on both platforms: `yt:BV1GJ411x7h7` yields the video id; `yt:BV1GJ411x7h7/../x` and a 13-char id yield null/nil; a Bilibili preview URL path is built for a valid id only.
- [ ] **Step 2: Run** the Android tests with `LARK_BUILD_HOST=user@host android/scripts/remote.sh test` and the iOS tests as `ios/README.md` describes. Expected: new tests FAIL. (If no build host is available, say so in the commit message and the final report; do not claim they passed.)
- [ ] **Step 3: Implement** the regex change.
- [ ] **Step 4: Run** the same commands. Expected: PASS.
- [ ] **Step 5: Commit** `feat(apps): accept Bilibili video ids in the native clients`.

### Task 9: Docs, spec and changelog

**Files:**
- Modify: `docs/architecture.md` (diagram line and the `ytdlp` and `download` rows, plus a `videosrc` row), `docs/configuration.md` (if not done in Task 3), `README.md` and `docs/install.md` (a short "Using Melarka in China" section: set `LARK_VIDEO_SOURCES=bilibili` or pick Bilibili in Admin → System; abroad servers can enable both), `CHANGELOG.md`, `docs/superpowers/specs/2026-11-19-bilibili-source-design.md` (Status becomes "implemented"; the "Source and ids" section says `IsVideoID` stays YouTube-only and `IsAnyVideoID` serves the shared pipeline; `b23.tv` left out in v1)
- Test: `internal/config/hygiene_test.go` (no email addresses), `scripts/oss-check.sh`

- [ ] **Step 1: Make the edits.**
- [ ] **Step 2: Run** `go vet ./... && go test ./... && cd web && npm test && npm run build`, then `scripts/oss-check.sh`. Expected: PASS.
- [ ] **Step 3: Commit** `docs: Bilibili source (architecture, install, changelog, spec status)`.
- [ ] **Step 4: Manual check, required before announcing (not before merge):** from a network inside China, or an equivalent, enable Bilibili only, search, download one song, preview one video, and report the result in the PR.
