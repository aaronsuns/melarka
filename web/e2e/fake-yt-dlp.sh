#!/usr/bin/env bash
# Stub yt-dlp used only by web/e2e tests (never shipped, never run against
# the real network). It mirrors exactly the argv shapes internal/ytdlp/ytdlp.go
# builds — --version, a --flat-playlist -J lookup (search or single-URL
# resolve), and the download invocation (-o <destNoExt>.%(ext)s ... -- <url>)
# — and produces a tiny real m4a with ffmpeg so the rest of the pipeline
# (scan, probe, embed overrides, stream) runs unmodified. Anything that
# doesn't match one of those exact shapes fails loudly (not silently) so a
# future ytdlp.go argv change that this stub hasn't been updated for is
# caught immediately, instead of producing some confusing downstream error.
set -euo pipefail

orig="$*"
fail() {
  echo "fake-yt-dlp: unexpected args: ${orig}" >&2
  exit 1
}

if [[ "${1:-}" == "--version" ]]; then
  echo "2099.01.01-fake"
  exit 0
fi

# Self-update (the server runs one at startup, and the admin 更新 button).
if [[ "$*" == "-U" ]]; then
  echo "yt-dlp is up to date (2099.01.01-fake)"
  exit 0
fi

args=("$@")
n=${#args[@]}

# Every search/resolve/download must enable node as the JS runtime.
[[ "${args[0]:-}" == "--js-runtimes" && "${args[1]:-}" == "node" ]] || fail

# has NEEDLE: true if NEEDLE appears verbatim among args.
has() {
  local needle="$1" a
  for a in "${args[@]}"; do
    [[ "$a" == "$needle" ]] && return 0
  done
  return 1
}

if has "-o"; then
  # Download invocation: see Client.Download in internal/ytdlp/ytdlp.go for
  # the exact argv it builds. Required: --progress (otherwise no LARKPROG
  # lines would ever appear), --print after_move:filepath (as two separate
  # args, in that order), -o <dest>, and -- before the URL.
  has "--progress" || fail
  has "--no-playlist" || fail
  has "--max-downloads" || fail
  has "--" || fail
  dest=""
  print_ok=false
  for ((i = 0; i < n; i++)); do
    if [[ "${args[$i]}" == "-o" ]]; then
      dest="${args[$((i + 1))]:-}"
    fi
    if [[ "${args[$i]}" == "--print" && "${args[$((i + 1))]:-}" == "after_move:filepath" ]]; then
      print_ok=true
    fi
  done
  [[ -n "$dest" ]] || fail
  $print_ok || fail
  last_url="${args[$((n - 1))]}"
  vid_id="${last_url#*v=}"
  vid_id="${vid_id%%&*}"
  secs=5
  [[ "$last_url" == *"v=fakevideo"* ]] && secs=30 # long enough to seek and switch at a position
  # Which file: by the -f value's shape, not its exact text (a changed
  # format list must not silently fall through to the audio branch).
  # Audio (music, episode audio, the m4a preview): -f bestaudio…. Video
  # (the 360p preview, an episode/HD merge with --merge-output-format):
  # anything else — an audio-only mp4 named .mp4, which both test browsers
  # play in <video> (no H.264 needed). No -f at all is unexpected.
  fmt=""
  meta_tpl=""
  progress_tpl=""
  for ((i = 0; i < n - 1; i++)); do
    [[ "${args[$i]}" == "-f" ]] && fmt="${args[$((i + 1))]}"
    [[ "${args[$i]}" == "--print" && "${args[$((i + 1))]}" == before_dl:LARKMETA* ]] && meta_tpl="${args[$((i + 1))]}"
    [[ "${args[$i]}" == "--progress-template" ]] && progress_tpl="${args[$((i + 1))]}"
  done
  is_video=false
  case "$fmt" in
    "") fail ;;
    bestaudio*) has "--merge-output-format" && is_video=true ;;
    *) is_video=true ;;
  esac
  # A preview (and 高清) asks for its details (size, channel, the format
  # picked) before the first byte, in exactly this shape.
  if [[ -n "$meta_tpl" && "$meta_tpl" != "before_dl:LARKMETA %(.{filesize,channel_id,channel,title,duration,description,format_id})j" ]]; then
    fail
  fi
  # A preview (served while it grows: --no-part) reads its size and
  # percent from its own progress line.
  if has "--no-part"; then
    [[ -n "$meta_tpl" ]] || fail
    [[ "$progress_tpl" == "download:LARKSIZE %(progress.total_bytes)s %(progress._percent_str)s" ]] || fail
  fi
  # The 360p preview: a merged ≤360p (never 18, which YouTube refuses).
  # fakevideo11 (the chromium-360 e2e's watch) takes the merged path, as
  # real videos do; the others report format 18 so the server's remaining
  # progressive path (a preview served while it grows) stays covered.
  format_id="140"
  $is_video && format_id="136+140"
  merged=false
  if [[ "$fmt" == 134+140/* ]]; then
    has "--no-part" || fail
    [[ "$fmt" == "134+140/(bv*[height<=360][vcodec^=avc1]+ba[ext=m4a])/(bv*[height<=360]+ba)" ]] || fail
    has "--merge-output-format" || fail
    has "--check-formats" && fail
    format_id="18"
    case "$vid_id" in
      fakevideo11) merged=true format_id="134+140" ;;
    esac
  fi
  if [[ -n "$meta_tpl" ]]; then
    echo 'LARKMETA {"filesize": null, "channel_id": "UCfakechannel00000000001", "channel": "假频道", "title": null, "duration": '"${secs}"', "description": "假视频的简介\n<b>第二行</b>", "format_id": "'"${format_id}"'"}'
  fi

  destNoExt="${dest%.%(ext)s}"
  mkdir -p "$(dirname "$destNoExt")"
  if $merged; then
    # The video stream, then the audio stream, each 0–100 (the server maps
    # them to 0–90 and 90–100), slow enough that the page shows 准备中 N%.
    for pct in 20.0 45.0 70.0 100.0; do
      echo "LARKSIZE 30000 ${pct}%"
      sleep 1
    done
    echo "LARKSIZE 5000 50.0%"
    sleep 1
    echo "LARKSIZE 5000 100.0%"
  else
    echo "LARKPROG 50.0%"
  fi
  if $is_video; then
    ffmpeg -nostdin -loglevel error -y -f lavfi -i "sine=frequency=330:duration=${secs}" -c:a aac -f mp4 "${destNoExt}.mp4"
    echo "LARKPROG 100.0%"
    echo "${destNoExt}.mp4"
    exit 101
  fi
  ffmpeg -nostdin -loglevel error -y -f lavfi -i "sine=frequency=440:duration=5" -c:a aac "${destNoExt}.m4a"
  echo "LARKPROG 100.0%"
  echo "${destNoExt}.m4a"
  # Like real yt-dlp: --max-downloads 1 reached after a successful download.
  exit 101
fi

# Channel episode details (VideoInfo): --skip-download -O "%(.{…})j" <watch url>.
if has "--skip-download"; then
  id="${args[$((n - 1))]#*v=}"
  echo "{\"id\": \"${id}\", \"title\": \"假节目 ${id}\", \"channel\": \"假频道\", \"channel_id\": \"UCfakechannel00000000001\", \"duration\": 5, \"live_status\": \"not_live\", \"availability\": \"public\", \"media_type\": \"video\", \"timestamp\": 0, \"description\": \"假节目的简介\"}"
  exit 0
fi

has "--flat-playlist" || fail
has "-J" || fail

last="${args[$((n - 1))]}"

# Two shapes: a ytsearch10:<query> search (always the same two canned
# results, 11-char ids like real ones — the server drops any other id shape) or a single resolved video URL (the "paste a
# YouTube link" flow), which gets a one-video object with no
# "_type":"playlist" wrapper, same as real yt-dlp.
# A query ending in "chromium-360" (the 视频 e2e's second project) gets its
# own ids, fakevideo11/12, so the two projects never see each other's
# previews, history or kept episodes on the shared server.
if [[ "$last" == ytsearch10:* ]]; then
  a=01 b=02
  [[ "$last" == *chromium-360 ]] && a=11 b=12
  cat <<JSON
{"_type":"playlist","entries":[
{"id":"fakevideo${a}","title":"假歌手 - 测试歌曲【MV】","channel":"假歌手频道","channel_id":"UCfakechannel00000000001","webpage_url":"https://www.youtube.com/watch?v=fakevideo${a}","duration":123,"thumbnails":[{"url":"https://i.ytimg.com/vi/fakevideo${a}/hqdefault.jpg"}]},
{"id":"fakevideo${b}","title":"Another Song","channel":"Some Channel","webpage_url":"https://www.youtube.com/watch?v=fakevideo${b}","duration":200,"thumbnails":[{"url":"https://i.ytimg.com/vi/fakevideo${b}/hqdefault.jpg"}]}
]}
JSON
  exit 0
fi

# Recommendations: a seed-song search (ytsearch5:) always finds one 30 s
# video — the e2e library's tones are 30 s, so it matches any of them — and
# every YouTube Mix (watch?v=<id>&list=RD<id>, expanded: never --no-playlist)
# is the same canned list: two songs, a compilation the server must filter
# out, and the e2e download fakevideo01 (filtered as already downloaded).
if [[ "$last" == ytsearch5:* ]]; then
  cat <<'JSON'
{"_type":"playlist","entries":[
{"id":"fakeseed001","title":"种子歌曲","channel":"种子频道","duration":30,"thumbnails":[{"url":"https://i.ytimg.com/vi/fakeseed001/hqdefault.jpg"}]}
]}
JSON
  exit 0
fi
if [[ "$last" == "https://www.youtube.com/watch?v="*"&list=RD"* ]]; then
  has "--no-playlist" && fail
  cat <<'JSON'
{"_type":"playlist","id":"RDmix","title":"Mix","entries":[
{"_type":"url","ie_key":"Youtube","id":"fakerecom01","url":"https://www.youtube.com/watch?v=fakerecom01","title":"推荐歌手 - 推荐歌曲一","channel":"推荐歌手","duration":200,"live_status":null},
{"_type":"url","ie_key":"Youtube","id":"fakecompil1","url":"https://www.youtube.com/watch?v=fakecompil1","title":"经典老歌合集","channel":"合集频道","duration":3000,"live_status":null},
{"_type":"url","ie_key":"Youtube","id":"fakerecom02","url":"https://www.youtube.com/watch?v=fakerecom02","title":"推荐歌手 - 推荐歌曲二","channel":"推荐歌手","duration":180,"live_status":null},
{"_type":"url","ie_key":"Youtube","id":"fakevideo01","url":"https://www.youtube.com/watch?v=fakevideo01","title":"假歌手 - 测试歌曲【MV】","channel":"假歌手频道","duration":123,"live_status":null}
]}
JSON
  exit 0
fi

# A channel page (@handle or /channel/UC…), read with --playlist-end 1.
if [[ "$last" == "https://www.youtube.com/@fakechannel" || "$last" == "https://www.youtube.com/channel/UCfakechannel00000000001" ]]; then
  cat <<'JSON'
{"_type":"playlist","id":"UCfakechannel00000000001","channel":"假频道","channel_id":"UCfakechannel00000000001","title":"假频道 - Videos","uploader_id":"@fakechannel","channel_follower_count":1200,"description":"一个用来测试的频道","thumbnails":[],"entries":[]}
JSON
  exit 0
fi
# Channel search (the sp=EgIQAg filter).
if [[ "$last" == "https://www.youtube.com/results?search_query="*"sp=EgIQAg"* ]]; then
  cat <<'JSON'
{"_type":"playlist","id":"q","title":"q","entries":[
{"_type":"url","ie_key":"YoutubeTab","id":"UCfakechannel00000000001","url":"https://www.youtube.com/channel/UCfakechannel00000000001","channel":"假频道","uploader_id":"@fakechannel","channel_follower_count":1200,"thumbnails":[]}
]}
JSON
  exit 0
fi

# Playlist search (the sp= filtered results page) and one canned playlist.
if [[ "$last" == "https://www.youtube.com/results?search_query="* ]]; then
  cat <<'JSON'
{"_type":"playlist","id":"q","title":"q","entries":[
{"_type":"url","ie_key":"YoutubeTab","id":"PLfakelist0001","url":"https://www.youtube.com/playlist?list=PLfakelist0001","title":"假歌单","channel":"假歌手频道","thumbnails":[{"url":"https://i.ytimg.com/vi/fakevideo01/hqdefault.jpg"}]}
]}
JSON
  exit 0
fi
if [[ "$last" == "https://www.youtube.com/playlist?list=PLfakelist0001" ]]; then
  cat <<'JSON'
{"_type":"playlist","id":"PLfakelist0001","title":"假歌单","channel":"假歌手频道","playlist_count":2,"entries":[
{"id":"fakevideo01","title":"假歌手 - 测试歌曲【MV】","channel":"假歌手频道","duration":123,"url":"https://www.youtube.com/watch?v=fakevideo01"},
{"id":"fakevideo03","title":"假歌手 - 第二首歌","channel":"假歌手频道","duration":99,"url":"https://www.youtube.com/watch?v=fakevideo03"}
]}
JSON
  exit 0
fi

# A bare video URL is the only other legal shape here (ValidURL already
# restricted it to the YouTube host allow-list before it ever reached argv).
[[ "$last" == http://* || "$last" == https://* ]] || fail

id="$last"
if [[ "$last" == *"v="* ]]; then
  id="${last#*v=}"
  id="${id%%&*}"
else
  id="${last##*/}"
fi
title="假歌手 - 测试歌曲【MV】"
[[ "$id" == "fakevideo03" ]] && title="假歌手 - 第二首歌"
cat <<JSON
{"id":"${id}","title":"${title}","channel":"假歌手频道","webpage_url":"${last}","duration":123,"thumbnails":[{"url":"https://i.ytimg.com/vi/${id}/hqdefault.jpg"}]}
JSON
