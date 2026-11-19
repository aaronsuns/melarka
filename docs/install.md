# Installing Melarka

The quickest way is the [one-line installer](../README.md#tldr-install-in-one-command), which
does all of the steps below for you. This guide expands the
[step-by-step quick start in the README](../README.md#quick-start-step-by-step). If you
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
| `latest` | the newest release; what `docker-compose.yml` runs unless `.env` sets `MELARKA_VERSION` |
| `0.2.2` | exactly this release: set `MELARKA_VERSION=0.2.2` in `.env` to upgrade only when you choose |
| `0.2` | the newest `0.2.x` patch release |

The image contains the `lark` server binary, ffmpeg, yt-dlp and Node.js (which yt-dlp uses for
YouTube's JavaScript challenges). It runs as uid `1000`, listens on port `4600`, and keeps its
state under `/data`. `docker run --rm ghcr.io/aaronsuns/melarka:latest --version` prints the
version.

## Step by step

1. **Make a directory** for Melarka, for example `/srv/melarka`, and `cd` into it.

2. **Save the compose file** as `docker-compose.yml`:

   ```bash
   curl -fsSL -o docker-compose.yml https://raw.githubusercontent.com/aaronsuns/melarka/main/deploy/docker-compose.example.yml
   ```

   It mounts your music from `MUSIC_DIR` in `.env` (or `./music` next to it if unset). You can
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

## In mainland China

YouTube is blocked in mainland China. Everything that does not need YouTube works as usual: your
own music folder, lyrics (NetEase, QQ Music and Kugou are among the lyrics sources), playlists,
favorites, the offline cache and the apps.

**Search and downloads without a proxy: Bilibili.** Add `LARK_VIDEO_SOURCES=bilibili` to `.env`
and run `docker compose up -d` (or, as admin, tick Bilibili in Admin → System). Search, downloads,
pasted links (`bilibili.com/video/BV…`, favourites and collections; not `b23.tv` short links) and
previews then use Bilibili. A server abroad can offer both: `LARK_VIDEO_SOURCES=youtube,bilibili`
shows a YouTube | 哔哩哔哩 switch above the results. Channels, video and "For you" are built on
YouTube and stay hidden while YouTube is off.

To use the YouTube features, only the **server** needs a way out; the phones never contact YouTube
(thumbnails and videos come through your server). If you already run a proxy on your network
(Clash, v2rayA and similar; Clash usually listens on port 7890), add these lines to `.env` and
restart with `docker compose up -d`:

```
HTTPS_PROXY=http://192.168.1.10:7890
HTTP_PROXY=http://192.168.1.10:7890
NO_PROXY=localhost,127.0.0.1
```

Use your proxy's LAN address, not `127.0.0.1`: inside the container, `127.0.0.1` is the container
itself. Both Melarka and yt-dlp read these variables, so the yt-dlp updates from GitHub go through
the proxy too. Pulling `ghcr.io/aaronsuns/melarka` can also be slow without it.


**中文简述：** 在中国大陆，YouTube 无法直接访问。本地音乐、歌词（歌词来源包括网易云、QQ 音乐、酷狗）、
歌单、收藏、离线缓存和手机 App 都能正常使用。**不用翻墙就能搜索和下载：** 在 `.env` 里加一行
`LARK_VIDEO_SOURCES=bilibili`，再运行 `docker compose up -d`（或用管理员账号在“管理 → 系统”里勾选哔哩哔哩），
搜索、下载、粘贴链接（`bilibili.com/video/BV…`、收藏夹和合集；不支持 `b23.tv` 短链接）和试听就都走 B 站。
海外的服务器可以两个都开（`youtube,bilibili`），搜索结果上方会出现 YouTube | 哔哩哔哩 切换。频道、视频和
“为你推荐”基于 YouTube，YouTube 关闭时会隐藏；要用它们，服务器需要代理：如果局域网里已有代理（如 Clash，
端口通常是 7890），把上面三行写进 `.env`（地址换成代理机器的局域网 IP，不要写 127.0.0.1），再运行
`docker compose up -d`。

## Upgrading and backups

See [Backup, restore and upgrade](../README.md#backup-restore-and-upgrade) in the README. In
short: back up `data/lark.db` with `sqlite3 data/lark.db ".backup …"`, then run the installer
again or `docker compose pull && docker compose up -d`.

## Uninstalling

`docker compose down` stops and removes the container. Your music is untouched; delete the
Melarka directory (`data`, `youtube`, `channels`, `previews`, and the config files) to remove
everything else.
