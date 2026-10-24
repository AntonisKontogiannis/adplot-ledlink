"""Generate the adplot LED Link app icon (PNG + multi-size ICO)."""

import os

from PIL import Image, ImageDraw, ImageFilter

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
S = 1024
BG_TOP = (22, 33, 44)
BG_BOTTOM = (10, 15, 21)
CYAN = (34, 211, 238)
GREEN = (52, 211, 153)


def linear_gradient(size, top, bottom):
    img = Image.new("RGB", size)
    px = img.load()
    h = size[1]
    for y in range(h):
        t = y / max(h - 1, 1)
        row = tuple(round(top[i] + (bottom[i] - top[i]) * t) for i in range(3))
        for x in range(size[0]):
            px[x, y] = row
    return img


base = linear_gradient((S, S), BG_TOP, BG_BOTTOM)

mask = Image.new("L", (S, S), 0)
ImageDraw.Draw(mask).rounded_rectangle(
    (0, 0, S - 1, S - 1), radius=int(S * 0.23), fill=255
)
icon = Image.new("RGBA", (S, S), (0, 0, 0, 0))
icon.paste(base, (0, 0), mask)

box = (S * 0.17, S * 0.29, S * 0.83, S * 0.71)
radius = int(S * 0.075)
stroke = int(S * 0.028)

glow = Image.new("RGBA", (S, S), (0, 0, 0, 0))
ImageDraw.Draw(glow).rounded_rectangle(
    box, radius=radius, outline=CYAN + (255,), width=stroke
)
icon = Image.alpha_composite(icon, glow.filter(ImageFilter.GaussianBlur(S * 0.03)))

screen = Image.new("RGBA", (S, S), (0, 0, 0, 0))
ImageDraw.Draw(screen).rounded_rectangle(
    box, radius=radius, outline=CYAN + (255,), width=stroke
)
icon = Image.alpha_composite(icon, screen)

r = S * 0.105
cx = cy = S / 2

dot_glow = Image.new("RGBA", (S, S), (0, 0, 0, 0))
ImageDraw.Draw(dot_glow).ellipse(
    (cx - r, cy - r, cx + r, cy + r), fill=GREEN + (255,)
)
icon = Image.alpha_composite(icon, dot_glow.filter(ImageFilter.GaussianBlur(S * 0.045)))

dot = Image.new("RGBA", (S, S), (0, 0, 0, 0))
ImageDraw.Draw(dot).ellipse((cx - r, cy - r, cx + r, cy + r), fill=GREEN + (255,))
icon = Image.alpha_composite(icon, dot)

icon.resize((512, 512), Image.LANCZOS).save(os.path.join(ROOT, "desktop/build/icon.png"))
icon.resize((512, 512), Image.LANCZOS).save(os.path.join(ROOT, "desktop/assets/icon.png"))
icon.save(
    os.path.join(ROOT, "desktop/build/icon.ico"),
    "ICO",
    sizes=[(s, s) for s in (16, 24, 32, 48, 64, 128, 256)],
)
print("icons written")
