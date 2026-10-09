# Installing Melarka

This guide expands the [quick start in the README](../README.md#quick-start-5-minutes). If you
use an AI coding agent, [AGENTS.md](../AGENTS.md) has the same steps with a check after each one.

## What you need

- A Linux host (amd64 or arm64) with Docker and Docker Compose v2. A small home server or a
  Raspberry Pi-class machine is enough for a household; transcoding uses ffmpeg, niced.
- A directory of music files (any format ffmpeg reads: FLAC, MP3, AAC/M4A, Ogg, Opus, WAV and
  so on).
- For phones and the iPhone app: a domain name and an HTTPS reverse proxy (see
  [the README](../README.md#reverse-proxy-and-https)).

## The image

Releases are published to the GitHub Container Registry as `ghcr.io/aaronsuns/melarka`, for
`linux/amd64` and `linux/arm64`, with three kinds of tags:

| Tag | Meaning |
|---|---|
| `0.1.0` | exactly this release; recommended, so upgrades happen when you choose |
| `0.1` | the newest `0.1.x` patch release |
| `latest` | the newest release |

The image contains the `lark` server binary, ffmpeg, yt-dlp and Node.js (which yt-dlp uses for
YouTube's JavaScript challenges). It runs as uid `1000`, listens on port `4600`, and keeps its
state under `/data`. `docker run --rm ghcr.io/aaronsuns/melarka:0.1.0 --version` prints the
version.

## Step by step

1. **Make a directory** for Melarka, for example `/srv/melarka`, and `cd` into it.

2. **Save the compose file** as `docker-compose.yml`:

   ```bash
   curl -fsSL -o docker-compose.yml https://raw.githubusercontent.com/aaronsuns/melarka/main/deploy/docker-compose.example.yml
   ```

   It mounts your music from `MUSIC_DIR` in `.env` (or `/srv/melarka/music` if unset). You can
   also edit the music line directly.

3. **Save the config file** as `config.yaml`. It works unchanged:

   ```bash
   curl -fsSL -o config.yaml https://raw.githubusercontent.com/aaronsuns/melarka/main/deploy/config.example.yaml
   ```

4. **Create `.env`** with the first admin's password and your music directory:

   ```bash
   printf 'MUSIC_DIR=%s\nLARK_ADMIN_PASSWORD=%s\n' /path/to/your/music "$(openssl rand -base64 18)" > .env
   chmod 600 .env
   ```

   Optional lines: `LARK_LANGUAGE=zh-Hans` (or `en`, `zh-Hant`, `sv`) and
   `LARK_LASTFM_API_KEY=<key>`. See [configuration.md](configuration.md).

5. **Create the writable directories**, owned by uid 1000 (the container user):

   ```bash
   mkdir -p data youtube channels previews
   sudo chown 1000:1000 data youtube channels previews
   ```

   If Docker creates them instead, they are owned by root and Melarka can't write its database.

6. **Start it:**

   ```bash
   docker compose up -d
   curl -fsS http://127.0.0.1:4600/api/v1/info
   ```

   The second command prints JSON that includes `"name":"Melarka"` and the version. If it fails,
   look at `docker compose logs --tail 50 melarka`.

7. **Sign in** at `http://127.0.0.1:4600` as `admin` with the password from `.env`
   (`grep LARK_ADMIN_PASSWORD .env`), change it in **Me → Change password**, and add people
   under **Admin → Users**. The first scan of your library starts by itself; large libraries take
   a few minutes, and **Admin → Library** shows its progress.

## File ownership and permissions

| Host path | In the container | Needs |
|---|---|---|
| `./data` | `/data` | read and write by uid 1000 |
| `./config.yaml` | `/data/config.yaml` | read (mounted read-only) |
| your music | `/music/main` | read by uid 1000; write too if deleting a song should move it to `.lark-trash/` |
| `./youtube` | `/music/youtube` | read and write by uid 1000 |
| `./channels` | `/data/channels` | read and write by uid 1000 |
| `./previews` | `/data/previews` | read and write by uid 1000 |

Melarka never changes your music files or their tags. The only write to a music directory is
moving a deleted song into `.lark-trash/` in that library; it is restored from there or removed
for good after 30 days.

## More libraries

`config.yaml` creates libraries only on the very first start. To add more later, mount another
directory under `/music` (for example `- /srv/audiobooks:/music/audiobooks`), restart, and add
it in **Admin → Library**. Libraries added there must live under `music_root` (`/music`).

## Building the image yourself

From a clone:

```bash
git clone https://github.com/aaronsuns/melarka.git && cd melarka
docker build --build-arg VERSION=my-build -t melarka:local .
```

Then use `image: melarka:local` in `docker-compose.yml`. The build takes a few minutes: it builds
the web app with Node.js, the server with Go, and downloads a checksum-verified yt-dlp.

Without Docker, you need Go 1.26, Node.js 22, ffmpeg and yt-dlp on the host. `make web build`
builds the web app and the binary `bin/lark`; note that `go build` alone embeds only a placeholder
page, so build the web app first and copy `web/dist/` to `internal/webui/dist/`, or serve it with
`LARK_WEB_DIR=web/dist`. See [CONTRIBUTING.md](../CONTRIBUTING.md) for development.

## Upgrading and backups

See [Backup, restore and upgrade](../README.md#backup-restore-and-upgrade) in the README. In
short: back up `data/lark.db` with `sqlite3 data/lark.db ".backup …"`, change the image tag, then
`docker compose pull && docker compose up -d`.

## Uninstalling

`docker compose down` stops and removes the container. Your music is untouched; delete the
Melarka directory (`data`, `youtube`, `channels`, `previews`, and the config files) to remove
everything else.
