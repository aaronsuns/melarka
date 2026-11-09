"""Build melarka_<lang>_v2.mp4 (1080x1920): the second cut, which opens on the car.

  python build_video_v2.py zh|en

Same pieces as build_video.py (demo-data captures in ../raw), but shorter, and the first second
is the hook: a drawn car screen (labelled as an illustration) showing what Melarka really sends
it: the current lyric line as the title, "song · artist" under it. Captions are on screen from
frame 0. ELEVEN_KEY overrides ~/.elevenlabs/key.

Captures come from promo.spec.ts (copy it to web/e2e/ to run it; see its header).
Voice: ElevenLabs (Jessica, eleven_v3), cached by text in ~/.cache/egg-raiders-tts.
"""
import hashlib, json, os, subprocess, sys, urllib.request, wave
import numpy as np
from PIL import Image, ImageDraw, ImageFont, ImageFilter

LANG = sys.argv[1] if len(sys.argv) > 1 else "zh"
S = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RAW, WORK = f"{S}/raw", f"{S}/work/{LANG}_v2"
os.makedirs(f"{WORK}/vo", exist_ok=True)
FONT = "/System/Library/AssetsV2/com_apple_MobileAsset_Font8/86ba2c91f017a3749571a82f2c6d890ac7ffb2fb.asset/AssetData/PingFang.ttc"
W, H, FPS = 1080, 1920, 30
DPR = 3
WIN_W, WIN_H, WIN_Y = 900, 1380, 430
WIN_X = (W - WIN_W) // 2
BG1, BG2 = (14, 10, 28), (52, 22, 66)
ACCENT = (52, 211, 120)
URL = "github.com/aaronsuns/melarka"

def font(sz, idx=11):
    return ImageFont.truetype(FONT, sz, index=idx)

P = "zh_" if LANG == "zh" else "en_"
LYR = [(f"{P}lyrics_{n}.png", 0, 0, False) for n in range(1, 7)]
CAR_LINES = ["窗外的光慢慢亮起来", "一条正弦波轻轻摇摆", "咖啡还冒着热气", "你说今天会是个晴天"]
CAR_SUB = "清晨的正弦波 · 示例乐队"
if LANG == "zh":
    SHOTS = [
        dict(cap=["一上车，歌就自己放", "歌词还显示在车机上"], car=True,
             vo="一上车，手机连上蓝牙，歌就自己开始放，车里的大屏上还跟着显示歌词。", seg=[]),
        dict(cap=["这是我给全家做的", "音乐服务器，替代了 Spotify"],
             vo="这是我给全家做的音乐服务器，叫Melarka，我们家用它替代了Spotify。",
             seg=[(P + "home.png", 0, 160, False)]),
        dict(cap=["歌词逐行同步", "对不上？点一下就对齐"],
             vo="歌词一行一行跟着走，对不上的话，点一下就对齐。", seg=LYR[:4]),
        dict(cap=["歌从哪来？", "YouTube 一搜就下 · 或用本地音乐"],
             vo="歌从哪儿来？YouTube上一搜就能下载，也可以直接用你自己的本地音乐。",
             seg=[(P + "search.png", 0, 300, False)]),
        dict(cap=["散步没信号也不怕", "收藏离线缓存在手机里"],
             vo="出去散步没有信号，收藏的歌也照样能放。",
             seg=[(P + "favorites.png", 0, 160, False)]),
        dict(cap=["全家每人一个账号", "各自的收藏 · 歌单 · 私人电台"],
             vo="全家每人一个账号，各听各的。",
             seg=[(P + "users.png", 0, 0, False)]),
        dict(cap=["iPhone · 安卓原生 App", "开源免费 · 装在自己家里"],
             vo="iPhone和安卓都有原生App，开源免费，装在你自己家的电脑或NAS上。",
             seg=[(P + "cover.png", 0, 140, False)]),
        dict(cap=None, end=["Melarka", "自托管的家庭音乐服务器", URL, "欢迎试用、提意见 · 喜欢就点个 Star"],
             vo="欢迎试用，提提意见，喜欢的话去GitHub点个星星。", seg=[]),
    ]
else:
    SHOTS = [
        dict(cap=["I get in the car...", "and my music starts by itself"], car=True,
             vo="I get in the car, the phone connects, and my music just starts playing, with the lyrics on the car screen.", seg=[]),
        dict(cap=["I built it for my family", "it replaced Spotify for us"],
             vo="It's a music server I built for my family. It's called Melarka, and it replaced Spotify for us.",
             seg=[(P + "home.png", 0, 160, False)]),
        dict(cap=["Synced lyrics", "off by a beat? tap to align"],
             vo="Lyrics scroll line by line, and one tap lines them up.", seg=LYR[:4]),
        dict(cap=["Where does the music come from?", "YouTube, or your own files"],
             vo="The music comes from YouTube, or from your own music folder.",
             seg=[(P + "search.png", 0, 300, False)]),
        dict(cap=["Walks with no signal", "favorites cached on the phone"],
             vo="On walks with no signal, it plays from the offline cache.",
             seg=[(P + "favorites.png", 0, 160, False)]),
        dict(cap=["One account per person", "own favorites, playlists, radio"],
             vo="Everyone at home gets their own account.",
             seg=[(P + "users.png", 0, 0, False)]),
        dict(cap=["Native iPhone + Android apps", "free, open source, self-hosted"],
             vo="Native iPhone and Android apps. Free, open source, on your own server or NAS.",
             seg=[(P + "cover.png", 0, 140, False)]),
        dict(cap=None, end=["Melarka", "a self-hosted music server for your family", URL, "try it, tell me what you think, star it"],
             vo="Try it, tell me what you think, and star it on GitHub if you like it.", seg=[]),
    ]

def dur_of(p):
    return float(subprocess.check_output(["ffprobe", "-v", "error", "-show_entries", "format=duration",
                                          "-of", "csv=p=0", p]).decode())

def eleven(text, path):
    key = os.environ.get("ELEVEN_KEY") or open(os.path.expanduser("~/.elevenlabs/key")).read().strip()
    def req(method, url, body=None):
        r = urllib.request.Request(url, method=method, data=json.dumps(body).encode() if body else None)
        r.add_header("xi-api-key", key); r.add_header("Content-Type", "application/json")
        return urllib.request.urlopen(r, timeout=120).read()
    spoken = "[warm] " + text
    cache = os.path.expanduser("~/.cache/egg-raiders-tts"); os.makedirs(cache, exist_ok=True)
    vid_file = os.path.join(cache, "jessica.id")
    if os.path.exists(vid_file):
        vid = open(vid_file).read().strip()
    else:
        vid = [v for v in json.loads(req("GET", "https://api.elevenlabs.io/v1/voices"))["voices"] if v["name"].startswith("Jessica")][0]["voice_id"]
        open(vid_file, "w").write(vid)
    mp3 = os.path.join(cache, hashlib.sha1(f"{vid}|eleven_v3|{spoken}".encode()).hexdigest()[:16] + ".mp3")
    if not os.path.exists(mp3):
        audio = req("POST", f"https://api.elevenlabs.io/v1/text-to-speech/{vid}?output_format=mp3_44100_128",
                    {"text": spoken, "model_id": "eleven_v3", "voice_settings": {"stability": 0.5, "similarity_boost": 0.8}})
        open(mp3, "wb").write(audio)
    subprocess.run(["ffmpeg", "-y", "-loglevel", "error", "-i", mp3, "-af",
        "silenceremove=start_periods=1:start_threshold=-45dB,areverse,silenceremove=start_periods=1:start_threshold=-45dB,areverse",
        "-ac", "1", "-ar", "44100", "-c:a", "pcm_s16le", path], check=True)
    return mp3

# ---------- voice ----------
t = 0.0
for i, s in enumerate(SHOTS):
    p = f"{WORK}/vo/{i}.wav"
    s["mp3"] = eleven(s["vo"], p)
    s["vo_path"], s["vo_len"] = p, dur_of(p)
    s["dur"] = max(2.6, s["vo_len"] + 0.5) if s["cap"] else max(4.5, s["vo_len"] + 1.2)
    s["t0"] = t
    t += s["dur"]
TOTAL = t
print("total", round(TOTAL, 1), [round(s["dur"], 2) for s in SHOTS])
if os.environ.get("VO_ONLY"):
    json.dump([{"vo": s["vo"], "mp3": s["mp3"]} for s in SHOTS], open(f"{WORK}/vo.json", "w"), ensure_ascii=False, indent=1)
    sys.exit(0)

# ---------- static pieces ----------
def gradient():
    a = np.linspace(0, 1, H)[:, None, None]
    g = (np.array(BG1) * (1 - a) + np.array(BG2) * a).astype(np.uint8)
    return Image.fromarray(np.repeat(g, W, axis=1), "RGB")
BG = gradient()

MASK = Image.new("L", (WIN_W, WIN_H), 0)
ImageDraw.Draw(MASK).rounded_rectangle((0, 0, WIN_W - 1, WIN_H - 1), 48, fill=255)
SCALE = WIN_W / 1170.0
cache = {}
def img(name):
    if name not in cache:
        im = Image.open(f"{RAW}/{name}").convert("RGB")
        cache[name] = im.resize((WIN_W, round(im.height * SCALE)), Image.LANCZOS)
    return cache[name]

def window(name, ycss):
    im = img(name)
    y = int(round(ycss * DPR * SCALE))
    y = max(0, min(y, im.height - WIN_H))
    return im.crop((0, y, WIN_W, y + WIN_H))

def shadow():
    sh = Image.new("L", (WIN_W + 120, WIN_H + 120), 0)
    ImageDraw.Draw(sh).rounded_rectangle((60, 70, WIN_W + 60, WIN_H + 70), 54, fill=170)
    return sh.filter(ImageFilter.GaussianBlur(26))
SHADOW = shadow()

def ease(x):
    x = min(max(x, 0), 1)
    return x * x * (3 - 2 * x)

def frame_base():
    f = BG.copy()
    d = ImageDraw.Draw(f)
    ft = font(36, 7)
    w = d.textlength(URL, font=ft)
    d.text(((W - w) / 2, 1856), URL, font=ft, fill=(180, 170, 205))
    return f
BASE = frame_base()

def draw_caption(f, lines, k):
    d = ImageDraw.Draw(f, "RGBA")
    sizes, fills, ys = [88, 64], [(255, 255, 255), ACCENT], [110, 246]
    off = (1 - ease(k)) * 40
    alpha = int(255 * ease(k))
    for line, sz, fill, y in zip(lines, sizes, fills, ys):
        ft = font(sz, 11)
        w = d.textlength(line, font=ft)
        while w > W - 100:
            sz -= 4; ft = font(sz, 11); w = d.textlength(line, font=ft)
        x = (W - w) / 2
        d.text((x + 3, y + off + 4), line, font=ft, fill=(0, 0, 0, alpha // 2))
        d.text((x, y + off), line, font=ft, fill=fill + (alpha,))

ICON = Image.open(f"{RAW}/icon.png").convert("RGBA").resize((320, 320), Image.LANCZOS)
def end_card(lines, k):
    f = BG.copy()
    a = ease(k)
    icon = ICON.copy(); icon.putalpha(icon.getchannel("A").point(lambda v: int(v * a)))
    f.paste(icon, (W // 2 - 160, 400), icon)
    d = ImageDraw.Draw(f, "RGBA")
    spec = [(150, (255, 255, 255), 790, 11), (56, (200, 190, 225), 990, 7), (58, ACCENT, 1150, 11), (50, (235, 230, 245), 1280, 7)]
    for i, (txt, (sz, fill, y, idx)) in enumerate(zip(lines, spec)):
        kk = ease((k - 0.12 * i) * 1.6)
        ft = font(sz, idx)
        while d.textlength(txt, font=ft) > W - 90:
            sz -= 3; ft = font(sz, idx)
        w = d.textlength(txt, font=ft)
        d.text(((W - w) / 2, y + (1 - kk) * 30), txt, font=ft, fill=fill + (int(255 * kk),))
    return f

# ---------- the hook: a drawn car screen (illustration) ----------
def car_frame(lt, dur):
    f = Image.new("RGB", (W, H), (8, 8, 12))
    a = np.linspace(0, 1, H)[:, None, None]
    g = (np.array((10, 10, 16)) * (1 - a) + np.array((26, 22, 34)) * a).astype(np.uint8)
    f = Image.fromarray(np.repeat(g, W, axis=1), "RGB")
    d = ImageDraw.Draw(f, "RGBA")
    # dashboard shape and the head unit
    d.rounded_rectangle((-40, 560, W + 40, 1500), 90, fill=(20, 20, 26))
    sx, sy, sw, sh = 70, 640, W - 140, 600
    d.rounded_rectangle((sx - 14, sy - 14, sx + sw + 14, sy + sh + 14), 44, fill=(4, 4, 6))
    d.rounded_rectangle((sx, sy, sx + sw, sy + sh), 34, fill=(16, 22, 34))
    d.text((sx + 44, sy + 36), "Bluetooth  ·  Melarka", font=font(34, 7), fill=(120, 170, 255))
    d.text((sx + sw - 150, sy + 36), "08:12", font=font(34, 7), fill=(170, 175, 190))
    # cover tile
    d.rounded_rectangle((sx + 44, sy + 120, sx + 244, sy + 320), 22, fill=(52, 211, 120))
    nx, ny = sx + 144, sy + 220  # a music note, drawn
    d.ellipse((nx - 34, ny + 20, nx + 6, ny + 52), fill=(16, 22, 34))
    d.rectangle((nx - 2, ny - 60, nx + 6, ny + 36), fill=(16, 22, 34))
    d.polygon([(nx + 6, ny - 60), (nx + 50, ny - 40), (nx + 50, ny - 18), (nx + 6, ny - 38)], fill=(16, 22, 34))
    step = max(1.4, (dur - 0.3) / len(CAR_LINES))
    j = min(int(lt / step), len(CAR_LINES) - 1)
    k = 1.0 if j == 0 else ease((lt - j * step) / 0.25)  # frame 0 is already complete
    tx = sx + 280
    ft = font(64, 11)
    line = CAR_LINES[j]
    while d.textlength(line, font=ft) > sx + sw - 44 - tx:
        ft = font(ft.size - 4, 11)
    d.text((tx, sy + 140 + (1 - k) * 24), line, font=ft, fill=(255, 255, 255, int(255 * k)))
    d.text((tx, sy + 240), CAR_SUB, font=font(40, 7), fill=(170, 175, 195))
    # progress
    py = sy + 420
    d.rounded_rectangle((sx + 44, py, sx + sw - 44, py + 10), 5, fill=(50, 56, 72))
    p = 0.06 + 0.1 * lt / dur
    d.rounded_rectangle((sx + 44, py, sx + 44 + (sw - 88) * p, py + 10), 5, fill=(52, 211, 120))
    # controls
    cy, c = sy + 520, (220, 225, 235)  # previous, pause, next, drawn
    cx = W / 2
    d.rectangle((cx - 12 - 20, cy - 26, cx - 12, cy + 26), fill=c); d.rectangle((cx + 12, cy - 26, cx + 12 + 20, cy + 26), fill=c)
    for dirn, x0 in ((-1, cx - 190), (1, cx + 190)):
        for o in (0, 30):
            x = x0 + dirn * o
            d.polygon([(x - dirn * 30, cy - 24), (x, cy), (x - dirn * 30, cy + 24)], fill=c)
        d.rectangle((x0 - dirn * 38 - 3, cy - 24, x0 - dirn * 38 + 3, cy + 24), fill=c) if False else None
    # phone toast that the car connected
    tk = 1.0
    ty = 1330 + (1 - tk) * 40
    d.rounded_rectangle((120, ty, W - 120, ty + 120), 30, fill=(40, 40, 52, int(235 * tk)))
    msg = "已连接车载蓝牙 · 随机播放收藏" if LANG == "zh" else "Car connected · shuffling favorites"
    ft2 = font(40, 7)
    w = d.textlength(msg, font=ft2)
    d.text(((W - w) / 2, ty + 36), msg, font=ft2, fill=(235, 235, 245, int(255 * tk)))
    lab = "示意图" if LANG == "zh" else "illustration"
    d.text((W - 60 - d.textlength(lab, font=font(28, 7)), 1520), lab, font=font(28, 7), fill=(120, 115, 140))
    ftu = font(36, 7)
    d.text(((W - d.textlength(URL, font=ftu)) / 2, 1856), URL, font=ftu, fill=(180, 170, 205))
    return f

def shot_view(s, lt):
    segs = s["seg"]
    segd = s["dur"] / len(segs)
    j = min(int(lt / segd), len(segs) - 1)
    name, y0, y1, _ = segs[j]
    p = (lt - j * segd) / segd
    view = window(name, y0 + (y1 - y0) * ease((p - 0.12) / 0.8))
    # crossfade into the next segment over its last 0.25 s
    rem = (j + 1) * segd - lt
    if j + 1 < len(segs) and rem < 0.25 and "lyrics" not in name:  # lyric frames cut, a blend ghosts the scroll
        n2, a2, _, _ = segs[j + 1]
        view = Image.blend(view, window(n2, a2), 1 - rem / 0.25)
    return view

# ---------- render ----------
out_silent = f"{WORK}/silent.mp4"
ff = subprocess.Popen(["ffmpeg", "-y", "-loglevel", "error", "-f", "rawvideo", "-pix_fmt", "rgb24",
                       "-s", f"{W}x{H}", "-r", str(FPS), "-i", "-", "-c:v", "libx264", "-preset", "medium",
                       "-crf", "18", "-pix_fmt", "yuv420p", out_silent], stdin=subprocess.PIPE)
for n in range(int(round(TOTAL * FPS))):
    t = n / FPS
    si = max(i for i, s in enumerate(SHOTS) if s["t0"] <= t + 1e-9)
    s = SHOTS[si]
    lt = t - s["t0"]
    if s["cap"] is None:
        fr = end_card(s["end"], lt / 1.2)
    elif s.get("car"):
        fr = car_frame(lt, s["dur"])
        draw_caption(fr, s["cap"], 1.0 if si == 0 else lt / 0.35)
    else:
        fr = BASE.copy()
        fr.paste((0, 0, 0), (WIN_X - 60, WIN_Y - 60), SHADOW)
        fr.paste(shot_view(s, lt), (WIN_X, WIN_Y), MASK)
        ImageDraw.Draw(fr).rounded_rectangle((WIN_X - 2, WIN_Y - 2, WIN_X + WIN_W + 1, WIN_Y + WIN_H + 1), 50,
                                             outline=(90, 70, 120), width=3)
        draw_caption(fr, s["cap"], lt / 0.35)
    if si > 0 and lt < 0.2:  # short dip between shots
        fr = Image.blend(Image.new("RGB", (W, H), BG1), fr, 0.4 + 0.6 * lt / 0.2)
    ff.stdin.write(fr.tobytes())
ff.stdin.close(); ff.wait()

# ---------- music: soft generated pad + pluck (no third-party audio) ----------
SR = 44100
N = int(TOTAL * SR) + SR
tt = np.arange(N) / SR
music = np.zeros(N)
def note(f): return 440.0 * 2 ** ((f - 69) / 12)
chords = [[60, 64, 67, 71], [57, 60, 64, 67], [53, 57, 60, 64], [55, 59, 62, 67]]
bar = 60 / 96 * 4
for b in range(int(TOTAL / bar) + 2):
    c = chords[b % 4]; st = int(b * bar * SR); ln = int(bar * SR * 1.1)
    if st >= N: break
    seg = tt[:min(ln, N - st)]
    env = np.minimum(1, seg / 0.6) * np.exp(-seg / 5)
    for m in c:
        music[st:st + len(seg)] += 0.06 * env * np.sin(2 * np.pi * note(m - 12) * seg)
    for k in range(8):
        m = c[[0, 1, 2, 3, 2, 1, 2, 3][k]] + 12
        s0 = st + int(k * bar / 8 * SR); l2 = int(0.5 * SR)
        if s0 + l2 > N: continue
        s2 = tt[:l2]
        music[s0:s0 + l2] += 0.035 * np.exp(-s2 / 0.18) * np.sin(2 * np.pi * note(m) * s2) * np.minimum(1, s2 / 0.005)
    for k in range(4):
        s0 = st + int(k * bar / 4 * SR); l2 = int(0.15 * SR)
        if s0 + l2 > N: continue
        s2 = tt[:l2]
        music[s0:s0 + l2] += 0.05 * np.exp(-s2 / 0.05) * np.sin(2 * np.pi * (55 + 60 * np.exp(-s2 / 0.03)) * s2)
music = music[:int(TOTAL * SR)]
fade = int(1.5 * SR); music[-fade:] *= np.linspace(1, 0, fade)
music /= np.max(np.abs(music)) + 1e-9
music *= 0.9
with wave.open(f"{WORK}/music.wav", "wb") as w_:
    w_.setnchannels(1); w_.setsampwidth(2); w_.setframerate(SR); w_.writeframes((music * 32767).astype(np.int16).tobytes())

# ---------- mix ----------
inputs, filters = [], []
for i, s in enumerate(SHOTS):
    inputs += ["-i", s["vo_path"]]
    ms = int((s["t0"] + 0.2) * 1000)
    filters.append(f"[{i+1}:a]aresample=44100,aformat=channel_layouts=mono,adelay={ms}|{ms},volume=1.6[v{i}]")
mi = len(SHOTS)
inputs += ["-i", f"{WORK}/music.wav"]
filters.append(f"{''.join(f'[v{i}]' for i in range(mi))}amix=inputs={mi}:normalize=0[vo]")
filters.append(f"[{mi+1}:a]volume=0.15[bg]")
filters.append("[vo][bg]amix=inputs=2:normalize=0,alimiter=limit=0.95[a]")
out = f"{S}/melarka_{LANG}_v2.mp4"
subprocess.run(["ffmpeg", "-y", "-loglevel", "error", "-i", out_silent] + inputs +
               ["-filter_complex", ";".join(filters), "-map", "0:v", "-map", "[a]", "-c:v", "copy",
                "-c:a", "aac", "-b:a", "160k", "-ar", "44100", "-ac", "2", "-t", f"{TOTAL:.3f}",
                "-movflags", "+faststart", out], check=True)
print("wrote", out, round(TOTAL, 1))
