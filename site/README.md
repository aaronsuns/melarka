# melarka.zexa.se

The Melarka landing page: static files in `public/` (one bilingual page, English and 中文,
switched in the page), served by the Cloudflare Worker `melarka-site` on the zone zexa.se.
`worker.js` only answers HTTP Range requests for the videos (iPhone Safari needs 206 responses);
everything else is plain static assets.

- Deploy: `cd site && npx -y wrangler@4 deploy` (Node 22, wrangler logged in to the zexa.se
  Cloudflare account).
- The videos and cover cards come from `marketing/2026-11/` (`tools/build_video.py`,
  `tools/build_cards.py`); the screenshots from `docs/screenshots/`. Rebuild them there and copy
  them into `public/media/` and `public/img/`.
- The page loads no external scripts, fonts or embeds and sets no cookies (the AlternativeTo badge is a local copy in `img/`, not hot-linked).
