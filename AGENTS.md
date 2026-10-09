# AGENTS.md — installing and working on Melarka

Instructions for coding agents. Humans: see README.md and docs/install.md (same steps, more words).

## Install Melarka unattended

Run each step, then its **Verify** command; stop and report if a verify fails.
Inputs you need: `MUSIC_DIR` (an existing directory of audio files) and a host with Docker.
Melarka runs from the published image `ghcr.io/aaronsuns/melarka`; no clone or build is needed.
Every command runs from the install directory `$DIR` (for example `/srv/melarka`); shell state
may not persist between your commands, so re-`cd "$DIR"` each time.

1. Docker with Compose v2.
   Verify: `docker compose version` prints `Docker Compose version v2…` or later, and `docker info >/dev/null && echo ok` prints `ok` (the user may run Docker).
2. Install directory and files, used unchanged.
   `mkdir -p "$DIR" && cd "$DIR"`
   `R=https://raw.githubusercontent.com/aaronsuns/melarka/main/deploy; curl -fsSL -o docker-compose.yml $R/docker-compose.example.yml && curl -fsSL -o config.yaml $R/config.example.yaml`
   `printf 'MUSIC_DIR=%s\nLARK_ADMIN_PASSWORD=%s\n' "$MUSIC_DIR" "$(openssl rand -base64 18)" > .env && chmod 600 .env`
   Optional lines in `.env`: `LARK_LANGUAGE=zh-Hans` (or `en`, `zh-Hant`, `sv`), `LARK_LASTFM_API_KEY=<key>`.
   Do not edit `docker-compose.yml` or `config.yaml`; everything site-specific belongs in `.env`
   (`TZ` is the exception: uncomment it in `docker-compose.yml` only if the user asks).
   Verify: `docker compose config --quiet && grep -q 'image: ghcr.io/aaronsuns/melarka:' docker-compose.yml && echo ok`.
3. Writable directories for uid 1000 (the container user).
   `mkdir -p data youtube channels previews && sudo chown 1000:1000 data youtube channels previews`
   Verify: `docker run --rm -u 1000:1000 -v "$PWD/data:/d" -v "$PWD/youtube:/y" -v "$PWD/channels:/c" -v "$PWD/previews:/p" alpine sh -c 'for x in d y c p; do touch /$x/.w && rm /$x/.w; done' && echo ok`.
   Also: `docker run --rm -u 1000:1000 -v "$MUSIC_DIR:/m:ro" alpine ls /m | head -3` lists files (readable).
4. Start.
   `docker compose pull && docker compose up -d`
   Verify (allow 2 minutes): `for i in $(seq 60); do curl -fsS http://127.0.0.1:4600/api/v1/info && break; sleep 2; done` prints JSON containing `"name":"Melarka"` (it is not the first field).
   On failure: `docker compose logs --tail 50 melarka`.
5. Sign in once. The token goes into a private file for steps 6 and 7.
   `PW=$(sed -n 's/^LARK_ADMIN_PASSWORD=//p' .env)`
   `(umask 077; curl -fsS -X POST http://127.0.0.1:4600/api/v1/auth/login -H 'Content-Type: application/json' -d "{\"username\":\"admin\",\"password\":\"$PW\",\"device_name\":\"install-check\"}" | sed -n 's/^{"token":"\([^"]*\)".*/Authorization: Bearer \1/p' > .lark-install-auth)`
   Verify: `grep -q '^Authorization: Bearer .' .lark-install-auth && echo ok`.
   Never print `$PW` or the token; tell the user the password is in `.env` (`LARK_ADMIN_PASSWORD`).
6. Library scanned.
   Verify (large libraries take minutes; this waits up to 10):
   `for i in $(seq 150); do curl -fsS -H @.lark-install-auth http://127.0.0.1:4600/api/v1/admin/scan/status | grep -Eq '"library_id":1,"running":false,"last":\{[^}]*\},"last_error":"","finished_at":[1-9]' && echo scanned && break; sleep 4; done`
   then `curl -fsS -H @.lark-install-auth 'http://127.0.0.1:4600/api/v1/tracks?limit=1' | grep -q '"items":\[{' && echo ok`.
   If `last_error` is not empty, report it: usually the music directory is not readable by uid 1000.
7. Clean up the install-check sign-in.
   `ID=$(curl -fsS -H @.lark-install-auth http://127.0.0.1:4600/api/v1/devices | grep -o '"id":[0-9]*,"name":"install-check"' | head -1 | sed 's/[^0-9]//g')`
   `curl -fsS -o /dev/null -w '%{http_code}\n' -X DELETE -H @.lark-install-auth "http://127.0.0.1:4600/api/v1/devices/$ID"; rm -f .lark-install-auth`
   Verify: `test ! -e .lark-install-auth && echo ok` (the DELETE printed `204`).
8. Optional, needs the user: an HTTPS reverse proxy (README "Reverse proxy and HTTPS"), then open the site on a phone and Add to Home Screen, or install the iPhone app (docs/iphone.md).

Upgrading later: back up `data/lark.db` (`sqlite3 data/lark.db ".backup lark-backup.db"`), change the image tag in
`docker-compose.yml`, then `docker compose pull && docker compose up -d` and repeat the step 4 verify.

## Naming

The product is **Melarka** (internal code name `lark`).
- Everything users see says Melarka: README, UI title, app display name, image name, releases, docs.
- Everything in code stays `lark`: the Go module and packages, the `lark` binary, `LARK_*` env vars, the
  database, `window.larkNative`, Swift types and the Xcode target.
- New code keeps using `lark` identifiers. Never rename existing identifiers to melarka.
- New user-visible strings say Melarka.

## Working on the code

- Layout: `cmd/lark` (main), `internal/<package>` (one job each; see docs/architecture.md), `web/` (React app, embedded at build time), `ios/` (the iPhone app, XcodeGen), `skills/` (agent skills), `deploy/` (compose and config examples), `docs/` (user docs).
- Before every commit: `go vet ./... && go test ./...` and `cd web && npm test && npm run build`; for UI flows also `npm run e2e` (it serves the built `web/dist`, so build first).
- Tests never touch the network: external programs (ffmpeg, ffprobe, yt-dlp) and HTTP services (lyrics providers, Last.fm) sit behind interfaces; use fakes, `httptest`, and recorded fixtures under `testdata/`.
- Never commit personal data: no email addresses, real tokens or real account names in code, fixtures or docs (`internal/config/hygiene_test.go` checks every tracked text file for email addresses; `scripts/oss-check.sh` is the release gate). Recorded fixtures replace keys with obviously fake ones.
- Melarka never writes tags into audio files. Metadata, tags and lyrics live in SQLite.
- Migrations in `internal/db/migrations` are append-only: add the next number, never edit a released one.
- Config: a new `config.yaml` key or `LARK_*` variable gets its default in `internal/config`, a line in `deploy/config.example.yaml` and a row in the tables in `docs/configuration.md`; `internal/config/docs_test.go` fails otherwise.
- UI text: add the key to `web/src/i18n/locales/en.json` first, then `zh-Hans`, `zh-Hant`, `sv` — the locale test fails on any missing key or placeholder. Never translate music metadata.
- API errors: `{"error": "<English>", "code": "<stable_code>"}`; new codes get an `error.<code>` key.
- Every admin route must be listed in `TestAdminRoutesForbiddenForMembers`.
- Commits: `type(scope): summary` (`feat(lyrics): …`, `fix(web): …`). Contributions require the CLA in CONTRIBUTING.md.
