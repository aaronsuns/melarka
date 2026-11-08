"""1080x1350 cover cards (zh, en): title, three phone screens from ../raw, the GitHub link."""
import os
import numpy as np
from PIL import Image, ImageDraw, ImageFont, ImageFilter
S = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FONT = "/System/Library/AssetsV2/com_apple_MobileAsset_Font8/86ba2c91f017a3749571a82f2c6d890ac7ffb2fb.asset/AssetData/PingFang.ttc"
W, H = 1080, 1350
font = lambda sz, i=11: ImageFont.truetype(FONT, sz, index=i)
def bg():
    a = np.linspace(0, 1, H)[:, None, None]
    g = (np.array((14, 10, 28)) * (1 - a) + np.array((52, 22, 66)) * a).astype(np.uint8)
    return Image.fromarray(np.repeat(g, W, axis=1), "RGB")
def centered(d, y, txt, sz, fill, idx=11):
    ft = font(sz, idx)
    while d.textlength(txt, font=ft) > W - 80:
        sz -= 3; ft = font(sz, idx)
    d.text(((W - d.textlength(txt, font=ft)) / 2, y), txt, font=ft, fill=fill)
def phone(c, name, x, y, w):
    im = Image.open(f"{S}/raw/{name}").convert("RGB")
    im = im.resize((w, round(im.height * w / im.width)), Image.LANCZOS).crop((0, 0, w, round(w * 2532 / 1170)))
    h = im.height
    sh = Image.new("L", (w + 80, h + 80), 0)
    ImageDraw.Draw(sh).rounded_rectangle((40, 50, w + 40, h + 50), 36, fill=160)
    c.paste((0, 0, 0), (x - 40, y - 40), sh.filter(ImageFilter.GaussianBlur(18)))
    m = Image.new("L", (w, h), 0); ImageDraw.Draw(m).rounded_rectangle((0, 0, w - 1, h - 1), 32, fill=255)
    c.paste(im, (x, y), m)
for lang, title, sub, feats, foot in [
    ("zh", "我给全家做的音乐 App", "完美替代 Spotify · 歌来自 YouTube 或本地音乐",
     ["逐行同步歌词 · 车载蓝牙自动播放", "离线缓存 · 全家每人一个账号 · 关注 YouTube 频道"],
     "iPhone · 安卓原生 App · 开源免费 · github.com/aaronsuns/melarka"),
    ("en", "My family's Spotify replacement", "songs from YouTube or your own files, on your own server",
     ["synced lyrics · plays when the car connects", "offline favorites · one account per person · YouTube channels"],
     "native iPhone + Android apps · open source · github.com/aaronsuns/melarka")]:
    c = bg(); d = ImageDraw.Draw(c)
    centered(d, 60, title, 80, (255, 255, 255))
    centered(d, 175, sub, 40, (52, 211, 120), 7)
    w = 310
    for i, n in enumerate(["home", "lyrics_4", "me"]):
        phone(c, f"{lang}_{n}.png", 30 + i * (w + 30) + 15, 280 if i != 1 else 260, w)
    for k, f in enumerate(feats):
        centered(d, 1040 + k * 75, f, 44, (240, 235, 250), 7)
    centered(d, 1265, foot, 30, (200, 190, 225), 7)
    os.makedirs(f"{S}/cards", exist_ok=True)
    c.save(f"{S}/cards/cover_{lang}.png"); print(lang)
