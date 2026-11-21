# Large mode (大图模式) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A per-account "大图模式" setting that turns 收藏 into a grid of large covers (tap to play) and enlarges the mini player and tab bar buttons.

**Architecture:** One new boolean `large_mode` in the existing per-user preferences (`internal/prefs`, `/me/preferences`). The web app reads it from `usePrefs()`; `TrackList` gets a `grid` variant used only by the 收藏 tab, and a `data-large` attribute on the shell drives larger bars through CSS. Nothing is hidden or locked.

**Tech Stack:** Go + SQLite (migrations), React + TypeScript + Vitest, Playwright e2e, plain CSS in `web/src/styles.css`.

**Spec:** `docs/superpowers/specs/2026-11-21-large-mode-design.md`

## Global Constraints

- Start from a fresh branch off `origin/main` (e.g. `feat/large-mode`); do not branch from local `main`.
- Migrations are append-only: add `internal/db/migrations/0034_user_prefs_large_mode.sql`, never edit an existing one.
- `large_mode` follows the `car_lyrics`/`normalize_loudness` contract: always set in what Get/Put return (default `false`); `nil` in a Put keeps the stored value.
- UI text keys go into `web/src/i18n/locales/en.json` first, then `zh-Hans`, `zh-Hant`, `sv` (locale test fails on a missing key). Never translate music metadata.
- User-visible name is Melarka; code identifiers stay `lark`.
- No personal data (emails, tokens) in code, fixtures or docs.
- Before each commit: `go vet ./... && go test ./...` and `cd web && npm test && npm run build`; before the final commit also `cd web && npm run e2e` (serves built `web/dist`, build first).
- Commit style `type(scope): summary`. Private repo: commit and push once tested.
- Only 收藏 changes layout. 歌曲, search, playlists, album and artist pages stay as they are. Nothing is hidden or locked.

## Review Focus

1. A server or cached answer without `large_mode` (older server): the web treats it as off. Tested in Task 2.
2. A client that does not send `large_mode` (the iOS/Android wrappers, an older web build) must not reset it. Tested in Task 1.
3. Empty 收藏 in large mode still shows the "no favorites" text, and the search box still works. Tested in Task 3.
4. A broken track in the grid stays disabled and dimmed. Tested in Task 3.
5. Long or CJK-heavy titles wrap to at most two lines and never widen a cell; the ⋯ menu on a left-column cell stays on screen. Checked by the e2e screenshot in Task 3.

---

### Task 1: `large_mode` preference on the server

**Files:**
- Create: `internal/db/migrations/0034_user_prefs_large_mode.sql`
- Modify: `internal/prefs/prefs.go`
- Test: `internal/prefs/prefs_test.go`, `internal/api/prefs_handlers_test.go`
- Modify: `docs/architecture.md` (prefs row mentions it), package comment in `prefs.go`

**Interfaces:**
- Produces: `prefs.Prefs.LargeMode *bool` with JSON key `large_mode`; `GET/PUT /api/v1/me/preferences` return it always set.

- [ ] **Step 1: Write failing tests** in `internal/prefs/prefs_test.go`, modelled on `TestCarLyricsDefaultsOnAndSurvivesOlderClients`:
  - `TestLargeModeDefaultsOffAndSurvivesOlderClients`: `Get` for a new user has `LargeMode != nil && !*LargeMode`; `Put` with `LargeMode=&true` returns true; a following `Put(Prefs{OnOpen:"nothing"})` (field omitted) keeps it true; a first `Put` for a second user without the field gives false; another user's value is not leaked.
  In `internal/api/prefs_handlers_test.go` add a round-trip: `PUT {"language":null,"on_open":"resume","large_mode":true}` then `GET` returns `"large_mode":true`.
- [ ] **Step 2: Run** `go test ./internal/prefs ./internal/api -run 'LargeMode|Prefs'`. Expected: FAIL (no field).
- [ ] **Step 3: Implement.** Migration: `ALTER TABLE user_prefs ADD COLUMN large_mode INTEGER NOT NULL DEFAULT 0 CHECK (large_mode IN (0,1));` with a one-line comment like 0026. In `prefs.go` add `LargeMode *bool` (`json:"large_mode"`) with the same comment style as `CarLyrics`; extend `Get` (default `false`, scan the column) and `Put` (insert `COALESCE(?,0)`, upsert `COALESCE(?, user_prefs.large_mode)`) exactly as `normalize_loudness` does. Mention large mode in the package comment and the `docs/architecture.md` prefs row.
- [ ] **Step 4: Run** `go vet ./... && go test ./...`. Expected: PASS (also the migration/doc tests).
- [ ] **Step 5: Commit** `feat(prefs): large_mode account preference`.

### Task 2: Settings toggle and web plumbing

**Files:**
- Modify: `web/src/api/types.ts` (`Prefs`), `web/src/pages/SettingsPage.tsx`, `web/src/i18n/locales/{en,zh-Hans,zh-Hant,sv}.json`, `web/src/App.tsx`
- Test: `web/src/pages/settings.test.tsx`

**Interfaces:**
- Consumes: `large_mode` from Task 1.
- Produces: `Prefs.large_mode?: boolean` (absent = off); `data-large="true"` on the `.shell` element when on (consumed by Task 3 CSS); locale keys `settings.largeMode`, `settings.largeModeHint`.

- [ ] **Step 1: Write failing test** in `settings.test.tsx`, modelled on the volume-normalization test: `"large mode is off by default, explained, and saves large_mode"` — finds checkbox named "大图模式", expects `checked === false`, the hint text `适合小朋友和老人：封面更大，按钮更大` is shown, clicking PUTs a body equal to `{ language: null, on_open: "resume", large_mode: true }` and the box ends checked. Add a second test: the prefs GET answer without `large_mode` renders unchecked (Review Focus 1).
- [ ] **Step 2: Run** `cd web && npx vitest run src/pages/settings.test.tsx`. Expected: FAIL.
- [ ] **Step 3: Implement.** Add `large_mode?: boolean` to `Prefs` with the "absent from an older server's answer: off" comment. In `SettingsPage.tsx` add a third `field-check` block after the loudness one (same `switch` markup and `aria-describedby` pattern), checked when `prefs.large_mode === true`. Locale strings: zh-Hans `大图模式` / `适合小朋友和老人：封面更大，按钮更大`; en `Large mode` / `For young children and older people: bigger covers and bigger buttons.`; write zh-Hant and sv equivalents. In `App.tsx`'s `Shell`, read `usePrefs().prefs.large_mode === true` and set `data-large={large ? "true" : undefined}` on `<div className="shell">`.
- [ ] **Step 4: Run** `cd web && npm test`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): large mode setting`.

### Task 3: Favorites cover grid and larger bars

**Files:**
- Modify: `web/src/components/TrackList.tsx` (new prop), `web/src/pages/LibraryPage.tsx`, `web/src/styles.css`
- Test: `web/src/pages/library.test.tsx`, `web/src/components/TrackList.test.tsx`
- Modify: `web/e2e/favorites.spec.ts` (grid case with a screenshot check), `docs/superpowers/specs/2026-11-21-large-mode-design.md` (set "Status: implemented" and note any breakpoints chosen)

**Interfaces:**
- Consumes: `prefs.large_mode` (Task 2) and `.shell[data-large="true"]`.
- Produces: `TrackList` prop `grid?: boolean` (default false). When true the `<ul>` has class `tracks tracks-grid` and everything else (menu, tap behaviour, `onPlay`, broken handling) is unchanged.

- [ ] **Step 1: Write failing tests.**
  - `TrackList.test.tsx`: with `grid`, the list has class `tracks-grid`; clicking a title button still calls `onPlay`; a `broken` track's main button is disabled; the ⋯ button still opens the menu with "下一首播放".
  - `library.test.tsx`, modelled on the existing favorites tests: with prefs `{ large_mode: true }` the 收藏 tab list has `tracks-grid`; the 歌曲 tab does not; with `large_mode` false or absent, 收藏 has no `tracks-grid`; with no favorites in large mode the "no favorites" text shows and the search box is present.
- [ ] **Step 2: Run** `cd web && npx vitest run src/components/TrackList.test.tsx src/pages/library.test.tsx`. Expected: FAIL.
- [ ] **Step 3: Implement.** `TrackList`: add the `grid` prop and apply the class on the `<ul>`; keep one render path. `LibraryPage`'s `Songs`: pass `grid={favorites && prefs.large_mode === true}` using `usePrefs()`. CSS (all under `.tracks-grid` or `.shell[data-large="true"]`):
  - `.tracks-grid`: `display:grid; grid-template-columns:repeat(auto-fill,minmax(150px,1fr)); gap:16px 12px`; two columns at phone width, three on an iPad.
  - Each `.track` becomes a column card; the existing `.track + .track::before` hairline is removed; `.track-main` is a column (cover, then title); `.cover` fills the cell width with `aspect-ratio:1` (override the inline 44px with `!important`, like `.grid .cover`).
  - Title: `fs-sub` or larger, up to 2 lines with `-webkit-line-clamp: 2`; the artist/meta lines are hidden in grid mode; the ♥ shows as an overlay on the cover's corner.
  - `.track-more` is absolutely positioned on the cover's corner, at least 44×44.
  - The ⋯ menu is anchored so it stays inside the screen for left-column cells (for example `left:0; right:auto` in the left column, or `position: fixed` bottom sheet style; builder's choice).
  - `.shell[data-large="true"] .mini-btn { width:64px; height:64px; min-height:64px }`, `--mini-h` raised accordingly, and a taller `--tab-h` with larger tab icons and labels.
- [ ] **Step 4: Run** `cd web && npm test && npm run build && npm run e2e`. Expected: PASS. In `favorites.spec.ts` add a large-mode case that sets `large_mode` through the API, opens 收藏 and takes a screenshot; look at it yourself at phone width and at 820px (iPad): two to three covers per row, no clipped titles, the ⋯ menu fully on screen for a left-column cell.
- [ ] **Step 5: Run** `go vet ./... && go test ./...` once more, update the spec status line, and commit `feat(web): large mode favorites grid and bigger bars`. Push, and tell Aaron which release it would go into (do not tag a release without being asked).
