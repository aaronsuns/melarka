#!/usr/bin/env python3
"""Made-up demo data for the README screenshots (web/e2e/screenshots.spec.ts).

Every artist, album, song, lyric, channel and video here is invented, and
every sound is a generated test tone. Covers and thumbnails are generated
colour gradients. Nothing touches the network.

  demo.py library <dir>   writes the music library (tones, covers, a .lrc)
  demo.py serve <port> <dir>   serves channel feeds (?channel_id=) and thumbnails
  demo.py yt <yt-dlp argv…>    answers fake-yt-dlp.sh's lookups when LARK_DEMO=1
  demo.py thumb <video id> <file>   writes a downloaded episode's thumbnail
"""
import concurrent.futures
import datetime
import http.server
import json
import os
import subprocess
import sys
import urllib.parse

# folder (its name carries the tag), album, artist, cover gradient (None: the app's initials tile),
# songs as (title, seconds). Folder words like 精选 / Piano / Jazz are the default folder tag rules.
LIBRARY = [
    ("精选 Classics/The Sine Waves - Golden Hours", "Golden Hours", "The Sine Waves", ("f7971e", "ffd200"),
     [("Morning in A", 214), ("440 Hz Lullaby", 187), ("Harmonic Afternoon", 241), ("Carrier Wave", 199)]),
    ("钢琴 Piano/Ivory Keys Trio - Practice Room", "Practice Room", "Ivory Keys Trio", ("43cea2", "185a9d"),
     [("Etude for Middle C", 176), ("Quiet Oscillator", 228), ("Sunday Scales", 163)]),
    ("咖啡 Cafe/示例乐队 - 午后咖啡", "午后咖啡", "示例乐队", ("c471f5", "fa71cd"),
     [("清晨的正弦波", 236), ("午后咖啡", 205), ("雨天的和弦", 251), ("慢慢的风", 192)]),
    ("Jazz/Square Wave Club - Late Set", "Late Set", "Square Wave Club", ("1d2b64", "f8cdda"),
     [("Middle C Blues", 263), ("Phase Shift", 218), ("Blue Note Test", 247)]),
    ("Rock/The Placeholders - Loud Defaults", "Loud Defaults", "The Placeholders", ("ee0979", "ff6a00"),
     [("Feedback Loop", 201), ("Overdrive Default", 189), ("Lorem Ipsum Riff", 223)]),
    ("民谣 Folk/演示歌手 - 回家路上", "回家路上", "演示歌手", ("56ab2f", "a8e063"),
     [("回家路上", 244), ("小镇的黄昏", 216), ("木吉他练习曲", 198)]),
    ("睡眠 Sleep/Night Owl Ensemble - Low Pass Lullabies", "Low Pass Lullabies", "Night Owl Ensemble", ("0f2027", "2c5364"),
     [("Low Pass Lullaby", 312), ("Slow Decay", 287)]),
    ("开车 Driving/Highway Oscillators - Cruise Control", "Cruise Control", "Highway Oscillators", ("fc4a1a", "f7b733"),
     [("Cruise Control", 207), ("Open Road in E", 233), ("Night Shift Sine", 219)]),
    ("儿歌 Kids/示例合唱团 - 彩虹儿歌", "彩虹儿歌", "示例合唱团", None,
     [("彩虹儿歌", 96), ("数星星", 104)]),
    ("80年代 Retro/Neon Test Card", "Neon Test Card", "Synth Demo Club", ("8e2de2", "4a00e0"),
     [("Neon Test Card", 226), ("Arcade Sunset", 238)]),
]

# The Now Playing screenshot's song, with synced lyrics (original lines, written for this demo).
LYRICS_FOR = "清晨的正弦波"
LYRICS = [
    (0.5, "窗外的光慢慢亮起来"),
    (5.0, "一条正弦波轻轻摇摆"),
    (9.5, "咖啡还冒着热气"),
    (14.0, "你说今天会是个晴天"),
    (18.5, "我们沿着河边走一走"),
    (23.0, "把昨天的烦恼放进口袋"),
    (27.5, "风把旋律吹得很远"),
    (32.0, "远到听不见也没关系"),
    (36.5, "清晨的正弦波"),
    (41.0, "一遍一遍回到原点"),
    (45.5, "像我们每天的问候"),
    (50.0, "简单 却刚刚好"),
]

# Channel ids are UC + 22 characters, video ids 11, like real ones.
CHANNELS = {
    "UCdemoKitchen00000000001": ("周末厨房", "@weekend-kitchen", ("ff9966", "ff5e62"), [
        ("demoKitch03", "第12期：番茄炒蛋的三种做法", 2, 734),
        ("demoKitch02", "第11期：十分钟早餐", 50, 612),
        ("demoKitch01", "第10期：一锅出的懒人晚饭", 170, 905)]),
    "UCdemoObservatory0000002": ("小小天文台", "@little-observatory", ("141e30", "243b55"), [
        ("demoStars03", "木星有多少颗卫星？", 5, 1210),
        ("demoStars02", "为什么月亮总是同一面朝着我们", 75, 986)]),
    "UCdemoBedtime00000000003": ("睡前故事屋", "@bedtime-stories", ("614385", "516395"), [
        ("demoStory03", "会唱歌的灯塔", 9, 842),
        ("demoStory02", "迷路的小云朵", 33, 768)]),
    "UCdemoPiano0000000000004": ("Daily Piano Practice", "@piano-practice", ("3a6073", "16222a"), [
        ("demoPiano03", "Lesson 8: left-hand arpeggios", 20, 1104),
        ("demoPiano02", "Lesson 7: playing with a metronome", 96, 957)]),
}
CHANNEL_IDS = {handle[1:]: cid for cid, (_, handle, _, _) in CHANNELS.items()}

# YouTube search results: invented songs on invented channels.
SEARCH = [
    ("demoSearch1", "示例乐队 - 清晨的正弦波 (Live)", "示例乐队 Official", 247, ("c471f5", "fa71cd")),
    ("demoSearch2", "清晨的正弦波 钢琴版 | Piano Cover", "Daily Piano Practice", 198, ("3a6073", "16222a")),
    ("demoSearch3", "清晨的正弦波 歌词版 Lyrics", "Lyrics Demo Channel", 236, ("43cea2", "185a9d")),
    ("demoSearch4", "Morning Sine Waves — 1 hour loop", "Test Tone Radio", 3600, ("f7971e", "ffd200")),
    ("demoSearch5", "清晨的正弦波 吉他教学", "Guitar Demo Lessons", 512, ("56ab2f", "a8e063")),
]


def ff(*args):
    subprocess.run(["ffmpeg", "-nostdin", "-loglevel", "error", "-y", *args], check=True)


def gradient(path, size, colors):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    c0, c1 = colors
    ff("-f", "lavfi", "-i", f"gradients=s={size}:c0=0x{c0}:c1=0x{c1}:x0=0:y0=0:x1={size.split('x')[0]}:y1={size.split('x')[1]}:speed=0",
       "-frames:v", "1", path)


def library(root):
    jobs, freq = [], 220
    for folder, album, artist, cover, songs in LIBRARY:
        d = os.path.join(root, folder)
        os.makedirs(d, exist_ok=True)
        if cover:
            jobs.append((gradient, os.path.join(d, "cover.jpg"), "600x600", cover))
        for n, (title, secs) in enumerate(songs, 1):
            freq = 220 + (freq * 7) % 440
            base = os.path.join(d, f"{n:02d} {title}")
            jobs.append((ff, "-f", "lavfi", "-i", f"sine=frequency={freq}:duration={secs}", "-ac", "1",
                         "-metadata", f"title={title}", "-metadata", f"artist={artist}", "-metadata", f"album={album}",
                         "-metadata", f"track={n}", "-b:a", "32k", base + ".mp3"))
            if title == LYRICS_FOR:
                with open(base + ".lrc", "w", encoding="utf-8") as f:
                    for at, line in LYRICS:
                        f.write(f"[{int(at // 60):02d}:{at % 60:05.2f}]{line}\n")
    with concurrent.futures.ThreadPoolExecutor(os.cpu_count()) as pool:
        for f in [pool.submit(fn, *a) for fn, *a in jobs]:
            f.result()


def colors_of(vid):
    for _, (_, _, colors, eps) in CHANNELS.items():
        if any(v == vid for v, *_ in eps):
            return colors
    for v, _, _, _, colors in SEARCH:
        if v == vid:
            return colors
    return ("444444", "222222")


def thumbs(root):
    for cid, (_, _, colors, eps) in CHANNELS.items():
        for vid, *_ in eps:
            gradient(os.path.join(root, "vi", vid, "mqdefault.jpg"), "320x180", colors)
    for vid, _, _, _, colors in SEARCH:
        gradient(os.path.join(root, "vi", vid, "mqdefault.jpg"), "320x180", colors)


def feed(cid):
    title, _, _, eps = CHANNELS[cid]
    now = datetime.datetime.now(datetime.timezone.utc)
    out = ['<?xml version="1.0" encoding="UTF-8"?><feed xmlns:yt="http://www.youtube.com/xml/schemas/2015" '
           'xmlns:media="http://search.yahoo.com/mrss/" xmlns="http://www.w3.org/2005/Atom">', f"<title>{title}</title>"]
    for vid, ep, hours, _ in eps:
        when = (now - datetime.timedelta(hours=hours)).strftime("%Y-%m-%dT%H:%M:%S+00:00")
        out.append(f"<entry><yt:videoId>{vid}</yt:videoId><yt:channelId>{cid}</yt:channelId><title>{ep}</title>"
                   f'<link rel="alternate" href="https://www.youtube.com/watch?v={vid}"/><published>{when}</published>'
                   f"<media:group><media:description>{ep}</media:description></media:group></entry>")
    out.append("</feed>")
    return "".join(out).encode()


def serve(port, root):
    thumbs(root)

    class H(http.server.SimpleHTTPRequestHandler):
        def __init__(self, *a, **k):
            super().__init__(*a, directory=root, **k)

        def log_message(self, *a):
            pass

        def do_GET(self):
            u = urllib.parse.urlparse(self.path)
            if u.path == "/videos.xml":
                cid = urllib.parse.parse_qs(u.query).get("channel_id", [""])[0]
                if cid not in CHANNELS:
                    self.send_error(404)
                    return
                body = feed(cid)
                self.send_response(200)
                self.send_header("Content-Type", "application/atom+xml")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                return
            super().do_GET()

    http.server.ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()


def episode(vid):
    for cid, (title, _, _, eps) in CHANNELS.items():
        for v, ep, hours, secs in eps:
            if v == vid:
                return cid, title, ep, secs
    return None


def yt(args):
    """The lookups fake-yt-dlp.sh hands over in demo mode (downloads stay there)."""
    last = args[-1]
    if "--skip-download" in args:  # an episode's details
        vid = last.split("v=", 1)[1].split("&", 1)[0]
        cid, ch, title, secs = episode(vid)
        print(json.dumps({"id": vid, "title": title, "channel": ch, "channel_id": cid, "duration": secs,
                          "live_status": "not_live", "availability": "public", "media_type": "video",
                          "timestamp": 0, "description": title}, ensure_ascii=False))
        return 0
    if last.startswith("ytsearch10:"):
        print(json.dumps({"_type": "playlist", "entries": [
            {"id": vid, "title": t, "channel": ch, "webpage_url": f"https://www.youtube.com/watch?v={vid}", "duration": secs,
             "thumbnails": [{"url": f"https://i.ytimg.com/vi/{vid}/hqdefault.jpg"}]}
            for vid, t, ch, secs, _ in SEARCH]}, ensure_ascii=False))
        return 0
    if last.startswith("ytsearch5:") or "&list=RD" in last:
        print(json.dumps({"_type": "playlist", "entries": []}))
        return 0
    for prefix in ("https://www.youtube.com/@", "https://www.youtube.com/channel/"):
        if last.startswith(prefix):
            key = last[len(prefix):]
            cid = CHANNEL_IDS.get(key, key)
            if cid in CHANNELS:
                title, handle, _, _ = CHANNELS[cid]
                print(json.dumps({"_type": "playlist", "id": cid, "channel": title, "channel_id": cid,
                                  "title": f"{title} - Videos", "uploader_id": handle, "channel_follower_count": 1200,
                                  "description": "", "thumbnails": [], "entries": []}, ensure_ascii=False))
                return 0
    if last.startswith("https://www.youtube.com/results?search_query="):
        print(json.dumps({"_type": "playlist", "id": "q", "title": "q", "entries": []}))
        return 0
    print(f"demo.py: unexpected yt-dlp args: {' '.join(args)}", file=sys.stderr)
    return 1


if __name__ == "__main__":
    cmd = sys.argv[1]
    if cmd == "library":
        library(sys.argv[2])
    elif cmd == "serve":
        serve(int(sys.argv[2]), sys.argv[3])
    elif cmd == "thumb":
        gradient(sys.argv[3], "320x180", colors_of(sys.argv[2]))
    elif cmd == "yt":
        sys.exit(yt(sys.argv[2:]))
    else:
        sys.exit(f"demo.py: unknown command {cmd}")
