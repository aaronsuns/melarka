# Configuration

Melarka reads environment variables and an optional YAML file, `config.yaml`. Environment
variables are for the few things that differ per machine (paths, the first admin, keys);
`config.yaml` holds everything else.

[`deploy/config.example.yaml`](../deploy/config.example.yaml) lists every key at its default, so
using it unchanged is the same as having no file except for the two libraries it creates. Delete
any line you don't change: a key you leave out keeps its default.

After changing either, restart: `docker compose up -d` picks up a changed `docker-compose.yml`
or `.env`, and `docker compose restart melarka` a changed `config.yaml`.

## Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `LARK_DATA_DIR` | `/data` | database (`lark.db`), transcode and yt-dlp caches, the updated yt-dlp, and by default channels and previews |
| `LARK_LISTEN` | `:4600` | HTTP listen address |
| `LARK_CONFIG` | `<data dir>/config.yaml` | path of the config file (the file is optional) |
| `LARK_ADMIN_USER` | none (`admin` in the compose example) | the first admin's username, used only while no admin exists |
| `LARK_ADMIN_PASSWORD` | none | the first admin's password, used only while no admin exists |
| `LARK_LANGUAGE` | none | overrides `language` from `config.yaml` |
| `LARK_LASTFM_API_KEY` | none | turns on Last.fm tagging and Last.fm's similar tracks (see [Tags](#tags)) |
| `LARK_OFFLINE_CACHE` | none | `on` or `off`; overrides `offline_cache` from `config.yaml` (see [Offline cache](#offline-cache)) |
| `LARK_WEB_DIR` | none | serve the web app from this directory instead of the embedded copy (development) |
| `LARK_YOUTUBE_FEED_URL` | `https://www.youtube.com/feeds/videos.xml` | where channel feeds are read (tests point it at a local server) |
| `LARK_YOUTUBE_THUMB_URL` | `https://i.ytimg.com/vi` | where the video thumbnail proxy reads YouTube thumbnails, as `<url>/<video id>/mqdefault.jpg` (tests point it at a local server) |
| `TZ` | UTC | the container's time zone, in `Area/City` form, such as `America/New_York`; used for `recommendations.refresh_at` |

With Docker, put `LARK_ADMIN_PASSWORD` (and any key) in `.env`, which the compose file loads with
`env_file`. The image already sets `LARK_DATA_DIR=/data` and `LARK_LISTEN=:4600`.

## `config.yaml` keys

| Key | Default | Meaning |
|---|---|---|
| `language` | `en` | UI language for users who haven't picked one: `en`, `zh-Hans`, `zh-Hant` or `sv` |
| `music_root` | `/music` | libraries added in the admin console must live under this directory |
| `offline_cache` | `true` | lets the web app keep songs on phones for offline play; `false` removes it from every phone (see [Offline cache](#offline-cache)) |
| `libraries` | none | libraries created on the very first start, when the database has none |
| `libraries[].name` | | display name |
| `libraries[].path` | | directory inside the container (`music_root` isn't enforced here) |
| `libraries[].download_target` | `false` | YouTube downloads are saved here; mark exactly one library |
| `ffmpeg_path` | `ffmpeg` | ffmpeg binary, looked up on `PATH` unless absolute |
| `ffprobe_path` | `ffprobe` | ffprobe binary (tags, durations, embedded lyrics) |
| `ytdlp_path` | `yt-dlp` | yt-dlp binary; an updated copy in `<data dir>/bin/yt-dlp` takes precedence |
| `transcode` | | transcoding settings |
| `transcode.cache_max_bytes` | `21474836480` (20 GiB) | cap on the transcode cache |
| `transcode.max_concurrent` | `2` | ffmpeg processes at once |
| `transcode.nice` | `true` | run ffmpeg at low CPU priority |
| `lyrics` | | lyrics settings |
| `lyrics.providers` | `[embedded, lrclib, netease, qq, kugou]` | lyrics sources in ranking order; remove one to switch it off |
| `lyrics.prefetch_interval` | `3s` | background lookup pace, one song per interval; `0s` turns it off |
| `artwork` | | cover settings |
| `artwork.providers` | `[embedded, folder, itunes, netease, qq]` | cover sources in order; remove one to switch it off |
| `artwork.prefetch_interval` | `3s` | background cover lookups, one song per interval; `0s` turns it off |
| `loudness` | | background loudness measurement (see [Loudness](#loudness)) |
| `loudness.enabled` | `true` | measure each song's loudness and true peak in the background |
| `loudness.gap` | `2s` | pause between songs; `0s` measures back to back |
| `loudness.retry_failed_after` | `720h` | a song that failed to measure is tried again after this (at least `1h`) |
| `tagging` | | tagging settings |
| `tagging.folder_rules` | 18 rules (see the example) | folder-name rules; writing this key replaces the whole list |
| `tagging.folder_rules[].match` | | texts matched case-insensitively against a song's folder path |
| `tagging.folder_rules[].tags` | | vocabulary tags given to matching songs |
| `tagging.lastfm_min_count` | `10` | minimum Last.fm tag count for a tag to be kept |
| `recommendations` | | "For you" settings (see [For you](#for-you)) |
| `recommendations.enabled` | `true` | `false` switches the nightly refresh and the Home section off |
| `recommendations.refresh_at` | `"02:30"` | time of day (`HH:MM`) the nightly refresh starts, in the container's local time: UTC unless `TZ` is set |
| `channels` | | following YouTube channels, previews and Video (see [Channels](#channels)) |
| `channels.enabled` | `true` | `false` switches Channels, previews and Video off, with their workers |
| `channels.root` | `""` (`<data dir>/channels`) | where episode files are stored (`<channel id>/<video id>.m4a`, `.v.mp4`, `.jpg`) |
| `channels.poll_interval` | `2h` | how often each followed channel's public feed is read (at least `15m`) |
| `channels.keep_days` | `10` | days an episode is kept after it was published (`1`–`3650`); each user can set their own per channel, the longest wins |
| `channels.initial_backfill` | `3` | newest episodes fetched when someone follows a channel (`0`–`15`) |
| `channels.max_gb` | `200` | cap on all episode files; over it the oldest episodes nobody kept are deleted first |
| `channels.preview_root` | `""` (`<data dir>/previews`) | where temporary previews and video thumbnails are stored |
| `channels.preview_ttl` | `24h` | a preview is deleted this long after it was last played (at least `1h`) |
| `channels.preview_max_gb` | `10` | cap on all previews; the least recently played are deleted first |

Durations need a unit: write `0s`, not `0`. Melarka refuses to start on an unknown language, an
unknown lyrics or cover provider, an unknown folder-rule tag, or an out-of-range value, and says
which key is wrong in its log (`docker compose logs melarka`).

The compose example mounts `./channels` and `./previews` at the default locations,
`/data/channels` and `/data/previews`. To keep episodes on a bigger disk, change the host side of
that mount; you don't need to change `channels.root`.

## Languages

The server default is `language` in `config.yaml`, or `LARK_LANGUAGE` if set. Each user can pick
their own in **Me → Language**, or "Server default" to follow the server. The choice is stored on
the server, so it follows the user to every device.

## YouTube downloads

Search in Melarka and, below your library's results, **Search YouTube** lists videos and
playlists. You can also paste a YouTube link (a song or a playlist) into Search. A link to a song
inside a playlist asks whether you want the song or the whole list. **Me → Download queue** shows
your download jobs.

- Downloads are saved as m4a into the library marked `libraries[].download_target`
  (`/music/youtube` in the example).
- Each downloaded song is added to the favorites of whoever asked for it.
- Downloading a whole playlist (up to 200 entries) creates a Melarka playlist in YouTube's order.
  Downloading it again adds what is new, keeps songs you added by hand, and fetches nothing twice.
- YouTube often breaks old yt-dlp versions, so Melarka updates its own copy of yt-dlp in the
  background at every start. An admin can also update it from **Admin → System**.

## For you

Home shows **For you** (为你推荐): about 30 YouTube songs picked for each user from up to ten
seeds, which are their most played songs of the last 30 days and their newest favorites. For each
seed Melarka reads its YouTube Mix and, when `LARK_LASTFM_API_KEY` is set, Last.fm's similar
tracks. A song suggested by more seeds ranks higher.

- Never suggested: songs already downloaded or in the library, songs you marked **Not
  interested**, anything under 1 or over 10 minutes, live streams, and compilations.
- The list refreshes nightly at `recommendations.refresh_at`, one user at a time, and on demand
  with **Refresh**.
- The work is gentle: yt-dlp runs niced, yields to people's searches, and searches at most 10
  seed songs per user per day. Failures wait and are not retried in a loop.

## Channels

The **Channels** tab follows YouTube channels without a YouTube account. Find one by searching, by
pasting a channel, handle or video link, or with **Follow channel** on any YouTube video row.
Episodes are kept apart from the music library: they are never in shuffle, radio, tags, lyrics or
recommendations.

- Every `channels.poll_interval` Melarka reads each followed channel's public feed. Episodes
  published after you followed are downloaded; when you follow, the newest
  `channels.initial_backfill` are fetched right away. A failing feed is retried later and later
  (up to once a day) without stopping the others.
- Per channel you choose: audio only (default) or audio and video (up to 720p), how many days to
  keep episodes, pause, and whether Shorts and finished live streams count. Members-only, private
  and age-restricted videos are skipped.
- An episode is deleted `keep_days` after it was published (the longest of its followers), unless
  someone tapped **Keep** or played it in the last two days. Over `channels.max_gb`, the oldest
  unkept episodes go first. Episodes of a channel nobody follows any more go at the next hourly
  sweep, except kept ones.
- Episodes play in their own queue and resume where you left off on any device, at 1× to 2×.
- **For you** on the Channels tab (推荐) lists channels that the YouTube Mixes of your channels' newest episodes
  lead to most often. It refreshes nightly and on **Refresh** (at most 50 Mix reads per day for
  manual refreshes).

### Previews

**Preview** (▶ 试听) plays any YouTube video in For you, Suggestions or a channel page before you
keep it. Melarka downloads it to `channels.preview_root` and starts playing while it downloads.
**Keep in my music** moves the file into the music library (as a download that becomes your
favorite), and **Keep in Channels** into Channels, with no second download.

- When YouTube pushes back (a bot check, a timeout), nothing is marked failed and Melarka asks you
  to try again.
- Previews start only with at least 2 GiB free on the preview disk; at most 20 per user and 2
  downloading at once.
- Previews are deleted `channels.preview_ttl` after they were last played, and the least recently
  played go first over `channels.preview_max_gb`.

### Video (视频)

**Video** watches YouTube through Melarka, for every signed-in user, while Channels are on. The
phone never contacts YouTube directly: search, related videos, thumbnails and the video itself all
come from your server.

- **Search** lists videos; **related** is the video's YouTube Mix (up to 25), cached for a day.
- **Thumbnails are proxied** from `LARK_YOUTUBE_THUMB_URL` and kept in
  `<channels.preview_root>/thumbs` for 7 days.
- **Playing** is a preview: 360p starts at once while it downloads, and **HD** fetches the up to
  720p version and switches over when it is complete.
- **History** is your own: the videos you opened and the searches you made, for 90 days. Clearing
  it also clears your video suggestions.

## Lyrics

When a song has no stored lyrics, Melarka asks the `lyrics.providers` in order and keeps the best
answer (synced lyrics beat plain text, then the version whose duration is closest to the song's,
then provider order):

- `embedded`: lyrics inside the file, or a `.lrc` file with the same name next to it. A `.lrc`
  next to the file always wins.
- `lrclib`: [LRCLIB](https://lrclib.net), an open lyrics API.
- `netease`, `qq`, `kugou`: NetEase Cloud Music, QQ Music and Kugou, which cover Chinese songs
  best. These are **unofficial APIs**: they may break, or change their terms, at any time. Remove
  them from `lyrics.providers` to switch them off.

A lookup takes at most 6 seconds. When no provider has a song, Melarka asks again after 7 days.
Songs tagged `instrumental` are skipped. In the background Melarka looks up one song every
`lyrics.prefetch_interval`, kept and favorite songs first.

**Fixing lyrics.** Everyone can fix lyrics that run early or late, for everyone: in the lyrics
view, **−0.5s / +0.5s** shift them, or long-press a line (right-click on a computer) while it is
being sung (**Hold to align to this line**). **Wrong lyrics** drops the shown lyrics and shows the
next best version (with **Undo** for 10 seconds). Admins can pick a different match, search with
their own title and artist, use a looser **Broad search**, restore rejected lyrics, or mark a
song as having no lyrics.

Titles of YouTube downloads are cleaned before searching (wrappers such as 《…》插曲, words such
as MV or 歌词, a trailing translation). For songs Melarka still cannot find, the
[lyrics agent skill](agent-skills.md) can help.

## Covers

Melarka shows the picture embedded in the file (YouTube downloads embed their thumbnail), else
`cover.*`, `folder.*` or `front.*` (jpg or png) in the song's folder. Then come the online
sources: `itunes` (Apple's iTunes Search API), `netease` and `qq` (unofficial, like their lyrics
APIs). Covers are cached as 300 px and 1000 px JPEGs in `<data dir>/cache/artwork`; deleting that
directory only costs a re-extract. Music files and folders are never written.

## Loudness

Melarka measures each song's integrated loudness (EBU R128, in LUFS) and true peak with ffmpeg's
`ebur128` filter, one song at a time in the background: new songs first, newest first, so a fresh
scan or download is measured within minutes. The ffmpeg process runs at low priority when
`transcode.nice` is on, and the worker waits while songs are being prepared for playback. It
decodes every file once, so a library of N tracks takes roughly N × (decode time + `loudness.gap`)
to finish; after that only new and changed files are measured. A changed file is measured again.
A song that cannot be measured is skipped and tried again after `loudness.retry_failed_after`.
Music files are only read, never written.

## Tags

Tags come from a fixed vocabulary of about 60 genres, moods, scenes, eras and languages. Browse
them in **Library → Tags** or on Home, and shuffle a tag. Four sources fill them in:

- **Folder rules** (`tagging.folder_rules`): for example a folder called `Piano` or `钢琴` tags its
  songs `piano`, and only a folder called `Instrumental` or `纯音乐` tags them `instrumental`
  (which skips lyrics lookups). Rules apply at every scan, and to existing songs at startup.
- **Last.fm**: set `LARK_LASTFM_API_KEY` to a free key from
  <https://www.last.fm/api/account/create>. Melarka then reads each song's Last.fm tags (at most 4
  requests a second) and keeps those that map into the vocabulary with a count of at least
  `tagging.lastfm_min_count`.
- **The [tagging agent skill](agent-skills.md)**, on your own AI subscription. Melarka itself
  calls no paid AI service and never analyses audio.
- **By hand**: admins edit a song's tags from its ⋯ menu or from Now Playing. Manual edits win
  over every other source.

## Offline cache

**Me → Offline cache** in the web app keeps your favorites (and songs you finish) on the phone at
`high` quality under a size limit you pick. A web page can't tell Wi-Fi from mobile data, so
automatic caching runs only after you tap **Cache favorites now** or with **Allow mobile data**
on. On an iPhone, use the home-screen app: iOS clears an ordinary website's storage after a while
without use. (The [iPhone app](iphone.md) has its own native cache.)

**The kill switch.** The offline cache runs in a service worker on each phone, and a service
worker stays installed even when the server stops shipping it. To turn the feature off
everywhere, set `offline_cache: false` (or `LARK_OFFLINE_CACHE=off`) and restart Melarka. On each
phone's next visit, the worker removes itself and deletes the cached songs without interrupting
playback, and the app hides the setting. Set it back to `true` to turn the feature on again.
