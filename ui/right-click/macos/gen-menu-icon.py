#!/usr/bin/env python3
"""生成 Finder 右键菜单图标 assets/zpack-menu.icns。

把 Z 造型（assets/zpack-icon-1024.png）缩到原大的 SCALE 倍，居中贴回同样大的透明画布，
再降采样成 iconset 十件套交给 iconutil。图标画布仍是 16×16 点——变的只是造型大小，
四周透明（2026-11-23 用户定案：原大偏大，缩到 90%）。

用法：python3 ui/right-click/macos/gen-menu-icon.py        # 需 Pillow
"""
import pathlib
import subprocess
import tempfile

from PIL import Image

SCALE = 0.90  # 造型相对原大的比例
SIZES = (16, 32, 128, 256, 512)  # iconset 各档的点尺寸，每档含 @2x
# BICUBIC 分步降采样：LANCZOS 会在造型四周散出极淡的振铃杂边（一步直降 16/32 更明显），
# 而菜单里实际显示的正是这几档
RESAMPLE = dict(resample=Image.Resampling.BICUBIC, reducing_gap=3.0)

ROOT = pathlib.Path(__file__).resolve().parents[3]
SOURCE = ROOT / "assets/zpack-icon-1024.png"
TARGET = ROOT / "assets/zpack-menu.icns"


def padded_master():
    """Z 造型缩到 SCALE 倍，居中贴回原尺寸的透明画布。"""
    glyph = Image.open(SOURCE).convert("RGBA")
    shrink = glyph.resize(
        (round(glyph.width * SCALE), round(glyph.height * SCALE)), **RESAMPLE
    )
    canvas = Image.new("RGBA", glyph.size, (0, 0, 0, 0))
    canvas.paste(
        shrink, ((glyph.width - shrink.width) // 2, (glyph.height - shrink.height) // 2)
    )
    return canvas


def main():
    master = padded_master()
    with tempfile.TemporaryDirectory() as workdir:
        iconset = pathlib.Path(workdir) / "zpack-menu.iconset"
        iconset.mkdir()
        for size in SIZES:
            for suffix, factor in (("", 1), ("@2x", 2)):
                pixels = size * factor
                master.resize((pixels, pixels), **RESAMPLE).save(
                    iconset / f"icon_{size}x{size}{suffix}.png"
                )
        subprocess.run(["iconutil", "-c", "icns", str(iconset), "-o", str(TARGET)], check=True)
    print(f"✓ 已生成 {TARGET.relative_to(ROOT)}（造型 {SCALE:.0%}，四周透明）")


if __name__ == "__main__":
    main()
