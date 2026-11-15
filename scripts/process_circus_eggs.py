#!/usr/bin/env python3
"""Turn the generated subagent eggs (scripts/generate_circus_eggs.sh) into cut-outs.

    python3 scripts/process_circus_eggs.py <png_dir> [out_dir]

Same treatment as the Circus backdrop art (public/zen/circus/backdrops/ASSETS.md):
white paper background removed (edge-connected near-white pixels, plus enclosed
paper gaps over 400 px), specks dropped, 1 px erosion and a feathered alpha; the
lightest paper toned to warm cream; trimmed and downscaled to 600 px tall (like
egg.webp); a cream scissor-cut border and a soft umber drop shadow baked in;
saved as WebP (quality 82). Needs Pillow, NumPy and SciPy.
"""
import sys
from pathlib import Path

import numpy as np
from PIL import Image, ImageFilter
from scipy import ndimage

HEIGHT = 600
BORDER = 6
CREAM = np.array([244, 236, 214], dtype=np.float32)  # #f4ecd6
PAPER = np.array([244, 235, 211], dtype=np.float32)  # #f4ebd3
PAD = BORDER + 14


def cut_out(rgb: np.ndarray) -> np.ndarray:
    near_white = (rgb >= 232).all(axis=2)
    labels, _ = ndimage.label(near_white)
    edge = set(np.unique(np.concatenate([labels[0], labels[-1], labels[:, 0], labels[:, -1]]))) - {0}
    background = np.isin(labels, list(edge))
    # Enclosed paper gaps (between an arm and the body, under a parasol): only
    # bare paper, i.e. almost pure white with no engraving in it. The egg
    # shells are near-white too, but stippled, so they stay.
    bare = (rgb >= 247).all(axis=2)
    for i in range(1, labels.max() + 1):
        if i in edge:
            continue
        region = labels == i
        size = region.sum()
        if size > 400 and bare[region].mean() > 0.97:
            background |= region
    figure = ~background
    # Drop specks under 0.2 % of the figure
    flabels, n = ndimage.label(figure)
    if n > 1:
        fsizes = ndimage.sum(figure, flabels, index=np.arange(1, n + 1))
        keep = np.where(fsizes >= 0.002 * figure.sum())[0] + 1
        figure = np.isin(flabels, keep)
    figure = ndimage.binary_erosion(figure, iterations=1)
    return figure


def process(src: Path, out_dir: Path) -> Path:
    im = Image.open(src).convert('RGB')
    rgb = np.asarray(im).astype(np.float32)
    mask = cut_out(np.asarray(im))
    # Lightest paper inside the figure toned to warm cream
    light = (rgb.min(axis=2) >= 225)[..., None]
    rgb = np.where(light, rgb * 0.35 + PAPER * 0.65, rgb)
    alpha = Image.fromarray((mask * 255).astype(np.uint8)).filter(ImageFilter.GaussianBlur(0.9))
    rgba = Image.fromarray(rgb.clip(0, 255).astype(np.uint8)).convert('RGBA')
    rgba.putalpha(alpha)
    rgba = rgba.crop(rgba.getbbox())
    scale = (HEIGHT - 2 * PAD) / rgba.height
    rgba = rgba.resize((round(rgba.width * scale), HEIGHT - 2 * PAD), Image.LANCZOS)

    w, h = rgba.width + 2 * PAD, rgba.height + 2 * PAD
    canvas = Image.new('RGBA', (w, h), (0, 0, 0, 0))
    a = np.zeros((h, w), dtype=np.uint8)
    a[PAD:PAD + rgba.height, PAD:PAD + rgba.width] = np.asarray(rgba)[..., 3]
    grown = ndimage.binary_dilation(a > 40, iterations=BORDER)
    # Soft umber shadow (38 %, offset 4,5, blur 2.2)
    shadow = Image.fromarray((ndimage.shift(grown.astype(np.float32), (5, 4), order=0) * 255 * 0.38).astype(np.uint8))
    shadow = shadow.filter(ImageFilter.GaussianBlur(2.2))
    canvas.paste(Image.new('RGBA', (w, h), (43, 29, 16, 255)), (0, 0), shadow)
    border = Image.fromarray((grown * 255).astype(np.uint8)).filter(ImageFilter.GaussianBlur(0.6))
    canvas.paste(Image.new('RGBA', (w, h), tuple(int(c) for c in CREAM) + (255,)), (0, 0), border)
    canvas.alpha_composite(rgba, (PAD, PAD))
    out = out_dir / f'egg-{src.stem}.webp'
    canvas.save(out, 'WEBP', quality=82, method=6)
    return out


def main() -> None:
    src_dir = Path(sys.argv[1])
    out_dir = Path(sys.argv[2]) if len(sys.argv) > 2 else Path('internal/server/frontend/public/zen/circus/eggs')
    out_dir.mkdir(parents=True, exist_ok=True)
    for png in sorted(src_dir.glob('*.png')):
        out = process(png, out_dir)
        with Image.open(out) as im:
            print(f'{out} {im.width}x{im.height} {out.stat().st_size // 1024} KB')


if __name__ == '__main__':
    main()
