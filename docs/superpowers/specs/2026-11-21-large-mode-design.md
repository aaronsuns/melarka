# Large mode (大图模式) — design

Status: implemented (2026-11-21). Approved by Aaron in conversation. Size: small change. Grid: 150px cells (190px from 700px wide); the ⋯ menu is a sheet above the bars; no ♥ overlay, since every item in 收藏 is a favorite.

## Intent and why

Aaron's 5-year-old uses Melarka on an iPad and pinch-zooms the 收藏 (Favorites) screen. The child cannot read and picks songs by cover thumbnail, but the cover is the smallest element of a row (44px). The same help suits older people who want bigger targets. The goal is to make playing favorites easy for them, not to restrict anything.

## Goals

- An account setting, "大图模式" (Large mode), stored on the server with the account's preferences, so it follows the account to every device.
- With it on, the 收藏 tab is a grid of large covers; one tap plays the song (same behaviour as today's row tap, including continuing as 随机播放收藏).
- Bigger touch targets in the mini player (play/pause, next) and the tab bar.

## Non-goals / anti-goals

- No lock-down: search, downloads, Settings, every other page and every menu stay reachable. No exit gate, no hidden navigation.
- No other list changes: 歌曲 tab, search results, playlists, album and artist pages keep their current layout (albums are already a cover grid).
- Not a replacement for system zoom or accessibility settings.

## Behaviour

- **Setting:** `large_mode` boolean in `/me/preferences`, default false. Toggle on the Settings page with a one-line explanation ("适合小朋友和老人：封面更大，按钮更大"). Existing accounts are unchanged until they turn it on.
- **收藏 grid:** 2 to 3 covers per row depending on width. Title under each cover in a larger font (kids use the cover, older people read the title); artist, duration and the ⬇︎ cached mark are not shown in the grid. ⋯ stays as a larger touch target on the cover's corner and opens the same menu as today, as a scrollable sheet above the bars (so a left-column cell cannot push it off screen). No ♥ overlay: every item in 收藏 is already a favorite. Search box, shuffle button and infinite loading are unchanged. A broken track's cover and title are dimmed and its main button disabled.
- **Mini player and tab bar:** larger buttons and hit areas when the mode is on.

## Approaches considered

1. **Display mode on the existing app (chosen).** One preference, a grid variant of the track list, CSS size changes. Small, nothing hidden.
2. **A separate kid shell replacing all routes.** Only worth it for a lock-down, which Aaron does not want; much more code.
3. **Hide things with flags.** Leaky and pointless once nothing needs hiding.

## Constraints (from AGENTS.md)

- Preference goes through `internal/prefs` and its API handlers; any migration is a new numbered file in `internal/db/migrations`, never an edit.
- UI text: key in `en.json` first, then `zh-Hans`, `zh-Hant`, `sv`.
- Update the specs/docs that describe preferences in the same commit.
- Before commit: `go vet ./... && go test ./...`, `cd web && npm test && npm run build`, and `npm run e2e` for the UI flow.

## Left to the builder

Exact grid column breakpoints and sizes, the CSS class naming, whether the grid is a variant prop on `TrackList` or a separate component, and whether the iOS and Android wrappers need any change (expected: none, the page is the same web app).
