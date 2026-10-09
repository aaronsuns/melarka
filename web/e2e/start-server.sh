#!/usr/bin/env bash
# Starts a throwaway Lark on 127.0.0.1:4700 with a tiny generated library.
# The admin password comes from LARK_E2E_PW (set by playwright.config.ts).
# Lyrics and covers use only the file's own (embedded/sidecar/folder) data: e2e never touches the network.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/music/main/邓丽君精选" "$TMP/music/main/Alan Walker" "$TMP/music/youtube" "$TMP/data"
tone() { ffmpeg -nostdin -loglevel error -y -f lavfi -i "sine=frequency=$1:duration=30" "${@:2}"; }
# A folder cover for 邓丽君精选; Faded has none (its cover answers 404).
ffmpeg -nostdin -loglevel error -y -f lavfi -i color=c=0x8a5cf6:s=600x600 -frames:v 1 "$TMP/music/main/邓丽君精选/cover.jpg"
tone 440 -metadata title=甜蜜蜜 -metadata artist=邓丽君 -metadata album=邓丽君精选 -b:a 192k "$TMP/music/main/邓丽君精选/01 甜蜜蜜.mp3"
printf '[00:00.50]甜蜜蜜第一句\n[00:02.00]甜蜜蜜第二句\n[00:04.00]甜蜜蜜第三句\n' > "$TMP/music/main/邓丽君精选/01 甜蜜蜜.lrc"
tone 523 -metadata title=月亮代表我的心 -metadata artist=邓丽君 -metadata album=邓丽君精选 "$TMP/music/main/邓丽君精选/02 月亮代表我的心.flac"
tone 660 -metadata title=Faded -metadata artist="Alan Walker" "$TMP/music/main/Alan Walker/Faded.wav"
printf 'libraries:\n  - name: main\n    path: %s/music/main\n  - name: youtube\n    path: %s/music/youtube\n    download_target: true\nytdlp_path: %s\nlyrics:\n  providers: [embedded]\n  prefetch_interval: 0s\nartwork:\n  providers: [embedded, folder]\n  prefetch_interval: 0s\n' \
  "$TMP" "$TMP" "$ROOT/web/e2e/fake-yt-dlp.sh" > "$TMP/data/config.yaml"
mkdir -p "$TMP/feeds"
python3 - "$TMP/feeds/videos.xml" <<'PY'
import datetime, sys
now = datetime.datetime.now(datetime.timezone.utc)
def entry(vid, title, hours, short=False):
    when = (now - datetime.timedelta(hours=hours)).strftime("%Y-%m-%dT%H:%M:%S+00:00")
    link = f"https://www.youtube.com/{'shorts/' + vid if short else 'watch?v=' + vid}"
    return (f"<entry><yt:videoId>{vid}</yt:videoId><yt:channelId>UCfakechannel00000000001</yt:channelId><title>{title}</title>"
            f"<link rel=\"alternate\" href=\"{link}\"/><published>{when}</published>"
            f"<media:group><media:description>{title} 的简介</media:description></media:group></entry>")
open(sys.argv[1], "w", encoding="utf-8").write(
    "<?xml version=\"1.0\" encoding=\"UTF-8\"?><feed xmlns:yt=\"http://www.youtube.com/xml/schemas/2015\" "
    "xmlns:media=\"http://search.yahoo.com/mrss/\" xmlns=\"http://www.w3.org/2005/Atom\"><title>假频道</title>"
    + entry("fakeshort01", "假短片", 1, short=True) + entry("fakeep00003", "假节目 第3集", 2) + entry("fakeep00002", "假节目 第2集", 3)
    + entry("fakeep00001", "假节目 第1集", 4) + entry("fakeep0000b", "假节目 旧的B", 5) + entry("fakeep0000a", "假节目 旧的A", 6)
    + "</feed>")
PY
for v in fakevideo01 fakevideo11; do # the 视频 e2e's card, one per project
  mkdir -p "$TMP/feeds/vi/$v"
  ffmpeg -nostdin -loglevel error -y -f lavfi -i color=c=0x3a7bd5:s=320x180 -frames:v 1 "$TMP/feeds/vi/$v/mqdefault.jpg"
done
python3 -m http.server 4701 --bind 127.0.0.1 --directory "$TMP/feeds" >/dev/null 2>&1 &
FEED_PID=$!
trap 'kill $FEED_PID 2>/dev/null; rm -rf "$TMP"' EXIT
(cd "$ROOT" && go build -o "$TMP/lark" ./cmd/lark)
LARK_WEB_DIR="$ROOT/web/dist" LARK_YOUTUBE_FEED_URL=http://127.0.0.1:4701/videos.xml LARK_YOUTUBE_THUMB_URL=http://127.0.0.1:4701/vi LARK_DATA_DIR="$TMP/data" LARK_LISTEN=127.0.0.1:4700 LARK_LANGUAGE=zh-Hans \
  LARK_ADMIN_USER=admin LARK_ADMIN_PASSWORD="$LARK_E2E_PW" "$TMP/lark"
