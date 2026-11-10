"""Product Hunt gallery (1270x760, English) and the 240x240 thumbnail, in ../producthunt/.
Phone screens from ../raw (demo data), the desktop screen from docs/screenshots."""
import os
import numpy as np
from PIL import Image, ImageDraw, ImageFont, ImageFilter
S = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ROOT = os.path.dirname(os.path.dirname(S))
FONT = "/System/Library/AssetsV2/com_apple_MobileAsset_Font8/86ba2c91f017a3749571a82f2c6d890ac7ffb2fb.asset/AssetData/PingFang.ttc"
MONO = "/System/Library/Fonts/Menlo.ttc"
W, H = 1270, 760
GREEN, WHITE, SOFT = (52, 211, 120), (255, 255, 255), (220, 214, 235)
font = lambda sz, i=11: ImageFont.truetype(FONT, sz, index=i)
OUT = f"{S}/producthunt"


def bg():
    a = np.linspace(0, 1, W)[None, :, None]
    g = (np.array((14, 10, 28)) * (1 - a) + np.array((52, 22, 66)) * a).astype(np.uint8)
    return Image.fromarray(np.repeat(g, H, axis=0), "RGB")


def text(d, x, y, txt, sz, fill, idx=11, maxw=None):
    ft = font(sz, idx)
    while maxw and d.textlength(txt, font=ft) > maxw:
        sz -= 2; ft = font(sz, idx)
    d.text((x, y), txt, font=ft, fill=fill)
    return y + sz * 1.35


def shadowed(c, im, x, y, r):
    w, h = im.size
    sh = Image.new("L", (w + 80, h + 80), 0)
    ImageDraw.Draw(sh).rounded_rectangle((40, 50, w + 40, h + 50), r, fill=150)
    c.paste((0, 0, 0), (x - 40, y - 40), sh.filter(ImageFilter.GaussianBlur(18)))
    m = Image.new("L", (w, h), 0)
    ImageDraw.Draw(m).rounded_rectangle((0, 0, w - 1, h - 1), r, fill=255)
    c.paste(im, (x, y), m)


def phone(c, name, x, y, w):
    im = Image.open(f"{S}/raw/{name}").convert("RGB")
    im = im.resize((w, round(im.height * w / im.width)), Image.LANCZOS)
    shadowed(c, im, x, y, 26)


def left_block(d, title, lines, top=150, maxw=560):
    y = text(d, 70, top, title, 54, WHITE, maxw=maxw)
    y += 20
    for ln in lines:
        y = text(d, 70, y, ln, 30, SOFT, 7, maxw=maxw) + 6
    return y


def save(c, n):
    os.makedirs(OUT, exist_ok=True)
    c.save(f"{OUT}/gallery_{n}.png")
    print(n)


# 1: the pitch, three phones
c = bg(); d = ImageDraw.Draw(c)
y = left_block(d, "Your family's own Spotify", [
    "A music server you run at home,", "with native iPhone and Android apps.", "",
    "One account per person: their own", "favorites, playlists and radio."])
text(d, 70, 640, "open source · github.com/aaronsuns/melarka", 24, GREEN, 7)
for i, n in enumerate(["home", "lyrics_4", "favorites"]):
    phone(c, f"en_{n}.png", 650 + i * 200, 95 if i == 1 else 125, 190)
save(c, 1)

# 2: lyrics and the car
c = bg(); d = ImageDraw.Draw(c)
left_block(d, "Lyrics, in the car too", [
    "Synced lyrics in the app, on the lock", "screen and on the car's display.", "",
    "Your favorites start playing by", "themselves when the phone connects", "to the car over Bluetooth."])
phone(c, "en_lyrics_2.png", 720, 80, 230)
phone(c, "en_cover.png", 980, 120, 230)
save(c, 2)

# 3: offline and the family
c = bg(); d = ImageDraw.Draw(c)
left_block(d, "Works with no signal", [
    "Favorites are cached on the phone,", "so walks and tunnels keep playing.", "",
    "Background play, lock-screen and", "car controls, gapless."])
phone(c, "en_favorites.png", 720, 80, 230)
phone(c, "en_me.png", 980, 120, 230)
save(c, 3)

# 4: the web app
c = bg(); d = ImageDraw.Draw(c)
text(d, 70, 50, "And a web app for any browser", 46, WHITE)
im = Image.open(f"{ROOT}/docs/screenshots/desktop-home.jpg").convert("RGB")
im = im.resize((960, 600), Image.LANCZOS)
shadowed(c, im, (W - 960) // 2, 130, 14)
save(c, 4)

# 5: install
c = bg(); d = ImageDraw.Draw(c)
y = left_block(d, "One Docker container, one command", [
    "Go + SQLite. It reads your music folders and never",
    "rewrites your files or tags. Songs can also come",
    "from YouTube, into your own library."], top=170, maxw=1100)
box = (70, y + 40, W - 70, y + 150)
d.rounded_rectangle(box, 16, fill=(10, 8, 20))
mono = ImageFont.truetype(MONO, 22)
d.text((box[0] + 30, box[1] + 22), "curl -fsSL https://raw.githubusercontent.com/aaronsuns/melarka/", font=mono, fill=GREEN)
d.text((box[0] + 30, box[1] + 58), "     main/deploy/install.sh | sh -s -- /path/to/your/music", font=mono, fill=GREEN)
text(d, 70, 640, "AGPL-3.0 · melarka.zexa.se", 24, GREEN, 7)
save(c, 5)

# the thumbnail
Image.open(f"{ROOT}/web/public/icon-512.png").convert("RGB").resize((240, 240), Image.LANCZOS).save(f"{OUT}/thumbnail.png")
print("thumbnail")
