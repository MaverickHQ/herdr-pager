#!/usr/bin/env python3
"""Composes the README's animated demo and the repository's social preview.

The screens come from the real firmware in the kit's emulator: bridge/emutest's
TestDemo records them, with how long each shows, the button that led there and
a caption, in bridge/emutest/out/demo/reel.json. This script draws the pager's
two buttons above each screen and the caption below it, and writes:

  docs/images/demo.gif            the demo, for the README
  docs/images/social-preview.png  1280x640, for GitHub's Settings > Social preview
  docs/images/social-preview.gif  the same, its device playing the demo: a README header
  docs/images/how-it-works.png    the README's diagram: the pager, pager-bridge and herdr
  docs/images/board-demo.gif      the demo on the board, in the owner's photo (docs/images/board.jpg)

Usage, from the repository's root (Pillow, and macOS's Avenir Next and Menlo):

  (cd bridge && PAGER_EMUTEST=1 PAGER_DEMO=1 go test -run TestDemo -count=1 -timeout 10m ./emutest/)
  python3 tools/media/compose.py
"""

import argparse
import json
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter, ImageFont

ROOT = Path(__file__).resolve().parents[2]

AVENIR = "/System/Library/Fonts/Avenir Next.ttc"
MENLO = "/System/Library/Fonts/Menlo.ttc"
AVENIR_BOLD, AVENIR_DEMI, AVENIR_MEDIUM = 0, 2, 5  # faces in the collection

BG = (22, 19, 17)
GLASS = (10, 9, 8)
TEXT = (237, 228, 219)
MUTED = (150, 138, 128)
ACCENT = (232, 116, 59)  # the pager's orange (#CC5A2A), lifted for a dark background
NUB = (62, 56, 52)

TAGS = ["esp32-s3", "esp-idf", "lvgl", "go", "ssh", "claude-code", "herdr", "embedded"]


def font(path, size, index=0):
    return ImageFont.truetype(path, size, index=index)


def text_width(draw, text, f):
    return draw.textlength(text, font=f)


def draw_caption(draw, caption, box, regular, strong):
    """Draws caption centred in box, with the buttons' names in the accent colour."""
    left, top, right, bottom = box
    runs = []
    for i, word in enumerate(caption.split(" ")):
        if i:
            runs.append((" ", regular, TEXT))
        key = word.rstrip(":;,.")
        if key in ("BOOT", "PWR"):
            runs.append((key, strong, ACCENT))
            if word[len(key):]:
                runs.append((word[len(key):], regular, TEXT))
        else:
            runs.append((word, regular, TEXT))
    width = sum(text_width(draw, t, f) for t, f, _ in runs)
    ascent, descent = regular.getmetrics()
    x = left + (right - left - width) / 2
    y = top + (bottom - top - ascent - descent) / 2
    for t, f, colour in runs:
        draw.text((x, y), t, font=f, fill=colour)
        x += text_width(draw, t, f)


def draw_buttons(draw, buttons, origin_x, edge_y, scale, pressed, label_font=None):
    """Draws BOOT and PWR on the device's top edge, each above its hint cell."""
    for name, (x, w) in buttons.items():
        on = pressed.startswith(name)
        cx = origin_x + (x + w / 2) * scale
        half = 24 * scale ** 0.5
        draw.rounded_rectangle((cx - half, edge_y - 11, cx + half, edge_y - 1), radius=4, fill=ACCENT if on else NUB)
        if label_font:
            label = name.upper()
            draw.text((cx - text_width(draw, label, label_font) / 2, edge_y - 30), label, font=label_font,
                      fill=ACCENT if on else MUTED)


def compose_gif(reel_dir, out):
    reel = json.loads((reel_dir / "reel.json").read_text())
    pad, top, bottom = 24, 46, 58
    sw, sh = 640, 172
    width, height = sw + 2 * pad, top + sh + bottom
    regular = font(AVENIR, 18, AVENIR_MEDIUM)
    strong = font(AVENIR, 18, AVENIR_DEMI)
    label = font(MENLO, 11, 1)
    frames, durations = [], []
    for fr in reel["frames"]:
        screen = Image.open(reel_dir / fr["file"]).convert("RGB")
        im = Image.new("RGB", (width, height), BG)
        d = ImageDraw.Draw(im)
        d.rounded_rectangle((pad - 6, top - 6, pad + sw + 5, top + sh + 5), radius=8, fill=GLASS)
        im.paste(screen, (pad, top))
        draw_buttons(d, reel["buttons"], pad, top - 6, 1, fr.get("press", ""), label)
        draw_caption(d, fr["caption"], (pad, top + sh + 6, pad + sw, height), regular, strong)
        frames.append(im.quantize(colors=256, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE))
        durations.append(fr["ms"])
    frames[0].save(out, save_all=True, append_images=frames[1:], duration=durations, loop=0, disposal=1)
    return len(frames), sum(durations)


class Preview:
    """The social preview's 1280x640 canvas: the name, the tagline, the tags and
    the device, with any screen of the demo on it."""

    width, height = 1280, 640
    scale = 1.45
    bez = 22

    def __init__(self, buttons):
        self.buttons = buttons
        self.sw, self.sh = round(640 * self.scale), round(172 * self.scale)
        dw, dh = self.sw + 2 * self.bez, self.sh + 2 * self.bez
        self.dx, self.dy = (self.width - dw) // 2, self.height - dh - 40

        # A soft glow of the screen's orange behind the device.
        im = Image.new("RGB", (self.width, self.height), BG)
        glow = Image.new("L", (self.width, self.height), 0)
        ImageDraw.Draw(glow).ellipse((self.dx - 60, self.dy - 40, self.dx + dw + 60, self.dy + dh + 80), fill=90)
        glow = glow.filter(ImageFilter.GaussianBlur(70))
        im = Image.composite(Image.new("RGB", (self.width, self.height), (204, 90, 42)), im, glow)

        d = ImageDraw.Draw(im)
        d.text((78, 44), "herdr-pager", font=font(AVENIR, 96, AVENIR_BOLD), fill=TEXT)
        d.text((82, 166), "A 1990s-style pager for your coding agents", font=font(AVENIR, 34, AVENIR_MEDIUM),
               fill=(205, 192, 180))
        chip_font = font(MENLO, 19)
        x, y, h = 82, 232, 38
        for tag in TAGS:
            w = text_width(d, tag, chip_font) + 30
            d.rounded_rectangle((x, y, x + w, y + h), radius=h // 2, fill=(38, 32, 29), outline=ACCENT, width=2)
            ascent, descent = chip_font.getmetrics()
            d.text((x + 15, y + (h - ascent - descent) / 2), tag, font=chip_font, fill=TEXT)
            x += w + 10
        d.rounded_rectangle((self.dx, self.dy, self.dx + dw, self.dy + dh), radius=26, fill=GLASS,
                            outline=(64, 57, 52), width=2)
        self.base = im

    def frame(self, screen, pressed=""):
        im = self.base.copy()
        im.paste(screen.resize((self.sw, self.sh), Image.Resampling.LANCZOS), (self.dx + self.bez, self.dy + self.bez))
        draw_buttons(ImageDraw.Draw(im), self.buttons, self.dx + self.bez, self.dy, self.scale, pressed)
        return im


def page_index(reel):
    """The demo's paged list: the still preview, and where the animated one starts."""
    return [i for i, fr in enumerate(reel["frames"]) if fr["caption"].startswith("An agent needs you")][-1]


def compose_preview(reel_dir, out):
    reel = json.loads((reel_dir / "reel.json").read_text())
    page = reel["frames"][page_index(reel)]
    Preview(reel["buttons"]).frame(Image.open(reel_dir / page["file"]).convert("RGB")).save(out, optimize=True)


def compose_preview_gif(reel_dir, out):
    """The preview with the device playing the demo, its buttons lit as pressed and
    no captions. It starts at the paged list, so its first frame is the still
    preview: what a link preview shows of an animated image."""
    reel = json.loads((reel_dir / "reel.json").read_text())
    i = page_index(reel)
    order = reel["frames"][i:] + reel["frames"][:i]
    preview = Preview(reel["buttons"])
    frames = [preview.frame(Image.open(reel_dir / fr["file"]).convert("RGB"), fr.get("press", "")) for fr in order]
    # One palette for every frame, from all of them, so the parts that never
    # change map to the same colours and only the device is stored again.
    thumbs = [f.resize((f.width // 2, f.height // 2), Image.Resampling.NEAREST) for f in frames]
    montage = Image.new("RGB", (thumbs[0].width, thumbs[0].height * len(thumbs)))
    for k, t in enumerate(thumbs):
        montage.paste(t, (0, k * t.height))
    palette = montage.quantize(colors=256, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE)
    # The glow is dithered once, so it doesn't band, and stays the same in every
    # frame; the device, flat colours and text, is pasted over it undithered.
    base = preview.base.quantize(palette=palette, dither=Image.Dither.FLOYDSTEINBERG)
    dw, dh = preview.sw + 2 * preview.bez, preview.sh + 2 * preview.bez
    box = (preview.dx, preview.dy - 12, preview.dx + dw, preview.dy + dh)
    quantized = []
    for f in frames:
        q = base.copy()
        q.paste(f.quantize(palette=palette, dither=Image.Dither.NONE).crop(box), box[:2])
        quantized.append(q)
    frames = quantized
    frames[0].save(out, save_all=True, append_images=frames[1:], duration=[fr["ms"] for fr in order], loop=0,
                   disposal=1)


def spaced(draw, xy, text, f, fill, tracking):
    """Draws text with extra space between its letters."""
    x, y = xy
    for ch in text:
        draw.text((x, y), ch, font=f, fill=fill)
        x += text_width(draw, ch, f) + tracking
    return x - tracking


def spaced_width(draw, text, f, tracking):
    return sum(text_width(draw, ch, f) for ch in text) + tracking * (len(text) - 1)


def arrow(draw, x0, x1, y, colour, width=4, head=16):
    """A horizontal arrow from x0 to x1."""
    sign = 1 if x1 > x0 else -1
    draw.line((x0, y, x1 - sign * head, y), fill=colour, width=width)
    draw.polygon([(x1, y), (x1 - sign * head, y - head * 0.6), (x1 - sign * head, y + head * 0.6)], fill=colour)


def compose_diagram(reel_dir, out):
    """How it works, at twice the README's width: the pager, pager-bridge and
    herdr, and what goes each way between them."""
    reel = json.loads((reel_dir / "reel.json").read_text())
    screen = Image.open(reel_dir / reel["frames"][page_index(reel)]["file"]).convert("RGB")
    width, height = 1760, 450
    margin, top, node_h = 40, 132, 132
    im = Image.new("RGB", (width, height), BG)

    # The three nodes' boxes, left to right; the arrows fill the gaps.
    scale, pad = 0.6, 14
    sw, sh = round(640 * scale), round(172 * scale)
    pager_w, bridge_w, herdr_w = sw + 2 * pad, 310, 350
    gap = (width - 2 * margin - pager_w - bridge_w - herdr_w) // 2
    px = margin
    bx = px + pager_w + gap
    hx = bx + bridge_w + gap

    glow = Image.new("L", (width, height), 0)
    ImageDraw.Draw(glow).ellipse((px - 40, top - 60, px + pager_w + 40, top + node_h + 60), fill=70)
    glow = glow.filter(ImageFilter.GaussianBlur(50))
    im = Image.composite(Image.new("RGB", (width, height), (204, 90, 42)), im, glow)
    d = ImageDraw.Draw(im)

    # The pager: its real screen, in its bezel, with its two buttons.
    py = top + (node_h - sh - 2 * pad) // 2
    d.rounded_rectangle((px, py, px + pager_w, py + sh + 2 * pad), radius=16, fill=GLASS, outline=(64, 57, 52),
                        width=2)
    im.paste(screen.resize((sw, sh), Image.Resampling.LANCZOS), (px + pad, py + pad))
    draw_buttons(d, reel["buttons"], px + pad, py, scale, "")

    mono = font(MENLO, 28)
    mono_b = font(MENLO, 30, 1)
    small = font(MENLO, 23)
    card = (34, 29, 26)

    # pager-bridge: one binary.
    d.rounded_rectangle((bx, top, bx + bridge_w, top + node_h), radius=16, fill=card, outline=(64, 57, 52), width=2)
    for i, c in enumerate([(120, 108, 98)] * 3):
        d.ellipse((bx + 20 + i * 22, top + 18, bx + 32 + i * 22, top + 30), fill=c)
    label = "pager-bridge"
    d.text((bx + (bridge_w - text_width(d, label, mono_b)) / 2, top + 50), label, font=mono_b, fill=TEXT)
    label = "Go · :2277"
    d.text((bx + (bridge_w - text_width(d, label, small)) / 2, top + 92), label, font=small, fill=MUTED)

    # herdr: the same agents, in their panes.
    d.rounded_rectangle((hx, top, hx + herdr_w, top + node_h), radius=16, fill=card, outline=(64, 57, 52), width=2)
    rows = [("!", "api-refactor"), ("*", "release-notes"), (">", "ci-pipeline"), (".", "docs-site")]
    row_font = font(MENLO, 23)
    for i, (glyph, name) in enumerate(rows):
        y = top + 14 + i * 27
        d.text((hx + 24, y), glyph, font=row_font, fill=ACCENT if glyph == "!" else MUTED)
        d.text((hx + 54, y), name, font=row_font, fill=TEXT if glyph == "!" else (200, 188, 178))

    # Between them: the link, and what goes each way.
    chip_font = font(MENLO, 22)
    mid = top + node_h // 2
    for x0, x1, link, right, left in [
        (px + pager_w, bx, "SSH, both keys pinned", "approve · deny", "list · cards"),
        (bx + bridge_w, hx, "herdr's JSON socket", "keystrokes", "agents · prompts"),
    ]:
        a, b = x0 + 22, x1 - 22
        cx = (x0 + x1) / 2
        w = text_width(d, link, chip_font) + 28
        d.rounded_rectangle((cx - w / 2, 54, cx + w / 2, 90), radius=18, fill=(38, 32, 29), outline=ACCENT, width=2)
        d.text((cx - w / 2 + 14, 59), link, font=chip_font, fill=TEXT)
        arrow(d, a, b, mid - 16, ACCENT)
        arrow(d, b, a, mid + 16, (200, 188, 178))
        d.text((cx - text_width(d, right, mono) / 2, mid - 58), right, font=mono, fill=TEXT)
        d.text((cx - text_width(d, left, mono) / 2, mid + 26), left, font=mono, fill=(200, 188, 178))

    # Under each node: its name, and one line on what it is.
    title = font(AVENIR, 34, 8)
    sub = font(AVENIR, 27, AVENIR_MEDIUM)
    for x, w, name, line in [
        (px, pager_w, "THE PAGER", "on your desk, on your Wi-Fi"),
        (bx, bridge_w, "THE BRIDGE", "one Go binary on your Mac"),
        (hx, herdr_w, "HERDR", "your agents, in their panes"),
    ]:
        tw = spaced_width(d, name, title, 4)
        spaced(d, (x + (w - tw) / 2, top + node_h + 34), name, title, TEXT, 4)
        d.text((x + (w - text_width(d, line, sub)) / 2, top + node_h + 84), line, font=sub, fill=MUTED)
    im.save(out, optimize=True)


# The board's glass in docs/images/board.jpg (the owner's photo, 2026-10-25),
# found by its dark pixels: top left, top right, bottom right, bottom left.
BOARD_GLASS = [(123.3, 380.4), (1000.5, 61.4), (1081.0, 336.5), (265.1, 723.8)]
# The lit area inside the glass, as fractions of its length and height.
BOARD_INSET = (0.035, 0.10)


def homography(src, dst):
    """The 3x3 projective map taking the four points src to dst."""
    import numpy as np
    rows, rhs = [], []
    for (x, y), (u, v) in zip(src, dst):
        rows.append([x, y, 1, 0, 0, 0, -u * x, -u * y]); rhs.append(u)
        rows.append([0, 0, 0, x, y, 1, -v * x, -v * y]); rhs.append(v)
    h = np.linalg.solve(np.array(rows, float), np.array(rhs, float))
    return np.append(h, 1).reshape(3, 3)


def apply(h, x, y):
    u, v, w = h @ [x, y, 1]
    return u / w, v / w


def compose_board(reel_dir, photo, out):
    """The demo playing on the board in the owner's photo: each screen warped
    onto the glass's lit area, with the glass's own reflections kept over it."""
    import numpy as np
    reel = json.loads((reel_dir / "reel.json").read_text())
    i = page_index(reel)
    order = reel["frames"][i:] + reel["frames"][:i]
    base = Image.open(photo).convert("RGB")
    W, H = base.size

    unit = homography([(0, 0), (1, 0), (1, 1), (0, 1)], BOARD_GLASS)
    iu, iv = BOARD_INSET
    lit = [apply(unit, u, v) for u, v in [(iu, iv), (1 - iu, iv), (1 - iu, 1 - iv), (iu, 1 - iv)]]
    # PERSPECTIVE takes the map from the output (the photo) to the input (the screen).
    to_screen = homography(lit, [(0, 0), (640, 0), (640, 172), (0, 172)])
    coeffs = tuple((to_screen / to_screen[2, 2]).flatten()[:8])

    ss = 4  # the lit area's edge, anti-aliased
    big = Image.new("L", (W * ss, H * ss), 0)
    ImageDraw.Draw(big).polygon([(x * ss, y * ss) for x, y in lit], fill=255)
    alpha = np.asarray(big.resize((W, H), Image.Resampling.LANCZOS), float)[..., None] / 255
    photo_px = np.asarray(base, float)

    frames = []
    for fr in order:
        screen = Image.open(reel_dir / fr["file"]).convert("RGB")
        warped = screen.transform((W, H), Image.Transform.PERSPECTIVE, coeffs, Image.Resampling.BICUBIC)
        warped = np.asarray(warped.filter(ImageFilter.GaussianBlur(0.6)), float)  # as soft as the photo
        lit_px = np.clip(warped * 0.86 + photo_px, 0, 255)  # a little dimmer; the glass's reflections stay on top
        frames.append(Image.fromarray((photo_px * (1 - alpha) + lit_px * alpha).astype("uint8")))

    # One palette from all the frames; the photo dithered once, so it stays the
    # same in every frame, and each frame's lit area pasted over it undithered.
    thumbs = [f.resize((W // 2, H // 2), Image.Resampling.NEAREST) for f in frames]
    montage = Image.new("RGB", (W // 2, H // 2 * len(thumbs)))
    for k, t in enumerate(thumbs):
        montage.paste(t, (0, k * (H // 2)))
    palette = montage.quantize(colors=256, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE)
    still = base.quantize(palette=palette, dither=Image.Dither.FLOYDSTEINBERG)
    mask = Image.fromarray(((alpha[..., 0] > 0.02) * 255).astype("uint8"))
    out_frames = []
    for f in frames:
        q = still.copy()
        q.paste(f.quantize(palette=palette, dither=Image.Dither.NONE), (0, 0), mask)
        out_frames.append(q)
    out_frames[0].save(out, save_all=True, append_images=out_frames[1:], duration=[fr["ms"] for fr in order], loop=0,
                       disposal=1)


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--reel", type=Path, default=ROOT / "bridge/emutest/out/demo")
    ap.add_argument("--out", type=Path, default=ROOT / "docs/images")
    args = ap.parse_args()
    n, ms = compose_gif(args.reel, args.out / "demo.gif")
    compose_preview(args.reel, args.out / "social-preview.png")
    compose_preview_gif(args.reel, args.out / "social-preview.gif")
    compose_diagram(args.reel, args.out / "how-it-works.png")
    compose_board(args.reel, args.out / "board.jpg", args.out / "board-demo.gif")
    for name in ("demo.gif", "social-preview.png", "social-preview.gif", "how-it-works.png", "board-demo.gif"):
        size = (args.out / name).stat().st_size
        print(f"{args.out / name}: {size / 1024:.0f} KB")
    print(f"demo: {n} frames, {ms / 1000:.1f} s a loop")


if __name__ == "__main__":
    main()
