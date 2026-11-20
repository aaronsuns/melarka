# Bilibili as a second video source

Status: implemented (2026-11-19); plan in `docs/superpowers/plans/2026-11-19-bilibili-source.md`.

## Intent

YouTube is blocked in mainland China. Melarka's video features (search, playlists, downloads,
previews, the 视频 page) all run `yt-dlp` **on the server**, so a server inside China cannot use
them at all, and people there should not need a VPN to get music and video into their library.

Bilibili is the Chinese equivalent. `yt-dlp` supports it natively (search, single videos,
favourites, collections, uploader pages). This design adds Bilibili as a second **source** next to
YouTube, and lets the admin decide which sources a server offers: YouTube, Bilibili, both, or none.

Servers can run in China (Bilibili only), abroad (either or both), or anywhere in between. Both
setups must work.

## Goals

- Search Bilibili, import a pasted Bilibili playlist link, download a Bilibili video's audio into
  the library, and preview it, with the same screens people already use for YouTube.
- The admin can enable any combination of `youtube` and `bilibili`, including neither. A disabled
  source is not shown in the UI and is refused by the API; its code never runs and the server makes
  no request to that service.
- No change for existing installs: after upgrading, a server offers exactly YouTube, as before.
- Songs, favourites, playlists and download lists that came from either source live side by side
  in the same library.

## Non-goals (v1)

- Following Bilibili uploaders (channels) and personalised recommendations ("For you", related
  videos). Both are built on YouTube's RSS feeds and Mixes, which Bilibili has no equivalent of.
  They stay YouTube-only and are hidden when YouTube is off.
- Searching Bilibili for playlists. Bilibili has no playlist search; a pasted favourites,
  collection or series link still works.
- Bilibili login or cookies (higher audio quality, members-only videos), and Bilibili video parts
  beyond the first (`?p=2`). Not supported in v1.
- Proxy or VPN support, and a mirror for `yt-dlp` self-updates from inside China.
- Per-user source choice. The server's network is what is blocked, so the admin decides.

## Anti-goals

- A second copy of the download, preview or search code for Bilibili.
- A database migration that rewrites existing ids or tables.
- Any outbound request to Bilibili from a server where the admin has not enabled it, or to
  YouTube where it is disabled (including background jobs: recommendations, channel polling,
  tag back-fill).

## Constraints from the codebase

- `internal/ytdlp` is the only package that runs `yt-dlp`, and every URL passes a host allow-list
  (`ValidURL`) first. New hosts are added there, nowhere else.
- Everything keys a video by a bare `video_id` string (downloads, download lists, previews,
  episodes, recommendations). `IsVideoID` accepts exactly 11 characters `[A-Za-z0-9_-]`.
- Bilibili ids are `BV` plus ten alphanumerics, 12 characters. A YouTube id is always 11, so the two
  can never collide and **the source can be read from the id**. No `source` column anywhere.
- A YouTube cover is derivable from the id (`i.ytimg.com/vi/<id>/…`); a Bilibili cover is not (it is
  a `hdslb.com` URL that only the search or detail result carries).
- Tests never touch the network; `yt-dlp` and HTTP sit behind interfaces with fakes and recorded
  fixtures. New `config.yaml` keys and `LARK_*` variables need a default in `internal/config`, a
  line in `deploy/config.example.yaml` and a row in `docs/configuration.md`. New UI text goes in
  all four locales; new API error codes get an `error.<code>` key; new admin routes go in
  `TestAdminRoutesForbiddenForMembers`.
- The web app's pasted-link parser (`web/src/youtubeLink.ts`) mirrors the server's rules, and the
  iOS and Android clients validate video ids before putting them in a path.

## Approaches considered

- **A. One pipeline with a source (chosen).** `internal/ytdlp` gains a source concept; search takes
  a `source`; download, preview and in-library work from the id alone. Small changes in many
  places, no duplication, no schema change.
- **B. A separate Bilibili stack.** Copy handlers, tables and screens. Fast to start, but doubles
  maintenance and every YouTube fix would need porting. Rejected.
- **C. One global source switch.** Either YouTube or Bilibili, never both. Simplest, but does not
  meet "both" or "disable either". Rejected.

## Design (approach A)

### Source and ids (`internal/ytdlp`)

- `type Source string` with `youtube` and `bilibili`.
- `IsVideoID` stays YouTube-only (so channels, episodes, recommendations and discovery keep
  rejecting Bilibili ids with no edits). A new `IsAnyVideoID` accepts a YouTube id (11 chars) or a
  Bilibili id (`^BV[0-9A-Za-z]{10}$`) and is used by the shared pipeline: download, preview,
  thumbnails, adopt and the watch page. `SourceOf(id)` returns the source from the shape.
- `WatchURL(id)` returns `https://www.bilibili.com/video/<id>` for a Bilibili id.
- `VideoIDFromURL` also reads `bilibili.com/video/BV…` (query string such as `?p=` ignored; part 1
  only) and `m.bilibili.com`. `b23.tv` short links are left out in v1: Melarka never follows a
  redirect to a host it has not checked, so the UI does not treat them as links.
- `ValidURL` allow-list adds `bilibili.com`, `www.bilibili.com`, `m.bilibili.com` and
  `space.bilibili.com` (favourites, collections, series and uploader pages).
- `Video` carries its `Thumbnail`. For YouTube it stays derived; for Bilibili the search or list
  result's cover is kept (normalised to https and only when on `hdslb.com`, `BilibiliCover`) and
  `ThumbnailURL(id)` returns "". The web app shows result covers straight from that URL (its
  `isSafeThumbnailUrl` allow-list adds `*.hdslb.com`, no referrer). The server's thumbnail proxy
  stays YouTube-only: it serves only the 视频 tab, which stays YouTube-only.
- Bilibili search runs `bilisearch<n>:<query>` as a **full** lookup (not flat: yt-dlp's flat
  Bilibili entries carry no titles, and search entries carry numeric ids), printing one JSON line
  per video with `--no-playlist` (part 1 of a multi-part video), at most 20 results (one page
  fetch each). Results keep Bilibili's own order. No playlist search for Bilibili. Bilibili's HTTP
  412 is `ErrBilibiliThrottled` (API `503 bilibili_throttled`, never cached).
- Playlist import (`ResolveList`) accepts Bilibili favourites, collection, series and uploader-video
  links the same way (full lookup, plus a `playlist:` line for the list's own id and title). They
  come in through the pasted-link download path (`POST /downloads {url}`); the YouTube list
  expand-in-place endpoints (`/youtube/playlist*`) stay YouTube-only.

### Which sources are on

- Setting `video_sources`: an ordered subset of `youtube`, `bilibili`; empty means video features
  are off.
- Default comes from `LARK_VIDEO_SOURCES` (comma-separated, or `none`), default `youtube`. An
  admin's choice in **Admin → System** is stored in the database and overrides the env default
  once set. (The default stays `youtube` so existing installs do not change; a new server in China
  sets `LARK_VIDEO_SOURCES=bilibili` or picks it in Admin.)
- `GET/PUT /admin/video-sources` (admin only; body `{"sources": ["bilibili"]}`).
- `GET /info` (public) gains `video_sources` so the web app and the native apps know what to show
  before anyone signs in.
- A `Sources` object on the server answers `Enabled(source)`. Every handler that reaches
  `yt-dlp` or the network for a source checks it first and answers `409` with code
  `source_disabled` (new `error.source_disabled` key). Background workers (channels, recommendations,
  tag back-fill, update of `yt-dlp` metadata) check it each cycle and idle when YouTube is off.
- Download, preview and in-library requests infer the source from the id; a Bilibili id with
  Bilibili off is `source_disabled`.

### API changes

- `GET /youtube/search`, `/youtube/playlist`, `/youtube/playlist/entries` keep their paths (no
  breaking rename). Search gains `source=youtube|bilibili`; when omitted it uses the first enabled
  source. The search cache key includes the source; the search limiter is shared.
- Playlist endpoints pick the source from the pasted URL.
- Bilibili failures map to new codes `bilibili_search_failed` and `bilibili_unavailable`
  (region-locked or removed video, HTTP 412 throttling), each with an `error.<code>` key and an
  actionable message ("try again in a minute").
- Existing `youtube_only` errors on channel routes stay; their copy says channels are YouTube-only.

### Web app

- Search page: when both sources are on, a two-way switch (YouTube | Bilibili) above the results,
  remembered per browser; when one is on, no switch; when none, the video search and the pasted-link
  box are hidden and a short note appears in the downloads area.
- `youtubeLink.ts` becomes a generic video-link parser returning `{source, videoId, listId}`,
  kept in step with the server rules; a link for a disabled source says that source is turned off.
- Playback of a Bilibili video on the 视频 page uses the server-side preview (video media), not an
  embed iframe, so it works wherever the server can reach Bilibili. YouTube keeps its embed.
- Channels, For you and related-video screens are hidden when YouTube is off.
- Admin → System: a "Video sources" card with two checkboxes and a note explaining that YouTube is
  blocked in mainland China.
- All text in `en`, `zh-Hans`, `zh-Hant`, `sv`.

### Native apps (iOS and Android)

- The id check before a path is widened to the Bilibili shape; the `yt:<id>` queue prefix is an
  internal name and stays. Unit tests on both platforms gain Bilibili cases. The no-unprompted-
  playback rules are unchanged (no new path starts sound).

### Docs and specs

- `docs/architecture.md` (`ytdlp`, `download` rows and the diagram), `docs/configuration.md`,
  `deploy/config.example.yaml` (or env-only per repo rule), README and `docs/install.md` get a
  short "Using Melarka in China" section; `CHANGELOG.md` entry.

### Details settled during review

Marked as the designer's calls, to be checked when reading.

- **Covers for id-only requests.** No request or response shape changes. Search results already carry
  the cover to the web app. For a Bilibili id with no stored cover (pasted link, bare-id enqueue,
  preview, adopt), the server does one `yt-dlp` detail read (`VideoMeta`, which exists) and stores
  the cover in the downloads or previews row; `download/store.go` stops overwriting a stored
  Bilibili cover with `ThumbnailURL(id)`. `ThumbnailURL(id)` for a Bilibili id with nothing stored
  returns "" and the UI shows its placeholder cover. Recommendations and channels never see a
  Bilibili id (below), so their `ThumbnailURL` calls are unchanged.
- **Native apps.** They show the same web app in a web view, so Bilibili search, the source switch and
  the Admin card come from the web app with no native UI work. Native work is limited to widening
  the id check and tests. A Bilibili video queued from search plays from its server preview exactly
  as a YouTube one does today (the `yt:<id>` queue path). When `/info` lists no sources, the web
  app hides the video features, so the native shells need nothing extra.
- **Turning a source off hides nothing that already exists.** Songs already in the library, their
  files, favourites, playlists, and existing channel subscriptions and downloaded episodes stay
  visible and playable. Only new network use stops: search, link resolve, new downloads and
  previews, channel polling, recommendation refresh. Downloads already queued when the source is
  switched off are failed with `source_disabled` (retryable after the admin turns it back on);
  one already running finishes.
- **`b23.tv`.** Rule: keep it only if `yt-dlp` resolves it without Melarka writing its own
  redirect-following code; otherwise leave it out of the allow-list and show "paste the full
  bilibili.com link". Melarka itself never follows redirects to hosts off the list.
- **Storage of `video_sources`.** A new small key/value table (or the existing one if the plan finds
  it) added as the next append-only migration. `PUT` rejects unknown source names with `400
  bad_request`, de-duplicates, and accepts an empty list. The env var is the default only; once an
  admin has saved a value the database wins, and Admin shows which one is in effect.
- **Bilibili uploader links in the playlist importer** produce a one-off download list only. Nothing
  imported feeds `episodes`, `channels`, or recommendation tables; those code paths reject
  Bilibili ids (`IsVideoID` callers there use a YouTube-only check), and the plan lists each caller.
- **Live test.** A hand check from a Chinese network is required before announcing the feature
  (README/site/marketing), not before merging; fixture tests are the merge gate.

## Testing

- Go: id shape and collision tests, URL parsing, `bilisearch` argv, parsing of recorded
  `bilisearch` and favourites-list JSON fixtures (no network), source-disabled paths for every
  handler and worker, the admin routes in `TestAdminRoutesForbiddenForMembers`.
- Web: Vitest for the link parser, search switch, hidden states; Playwright flow with Bilibili
  served from the local fixture server (like `LARK_YOUTUBE_COVER_URL`).
- Android and iOS: id validation unit tests.
- Live check by hand once, from a Chinese network or equivalent, since tests cannot reach Bilibili.

## Risks

- **Bilibili throttling.** Anonymous search can answer HTTP 412. Mitigation: shared limiter, search
  cache, a clear retry message. Cookies are a possible follow-up.
- **Audio quality.** Anonymous access gives the lower audio tiers; fine for v1.
- **`yt-dlp` self-update from inside China** may not reach GitHub. The image ships a `yt-dlp`, so it
  still works; the admin update shows the failure. A mirror is a follow-up.
- **Region locks.** Some Bilibili videos are mainland-only and fail from abroad; surfaced as
  `bilibili_unavailable`.
- **Widening `IsVideoID`** touches every place that trusts the 11-character shape; the plan must
  list and test each (server, web parser, both native clients).

## Left to the builder

- The exact Bilibili `yt-dlp` format selection for audio-only, and the fixture recording.
- Whether `LARK_VIDEO_SOURCES` also gets a `config.yaml` key or stays env-only (follow the repo rule).
- UI polish of the switch and the Admin card.
