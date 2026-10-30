#!/usr/bin/env python3
"""把仓库根目录的 logo.png 转成各端需要的小图标。

    python scripts/make_icon.py

logo.png 是应用图标源文件（正方形最省事）。这里做两件事：

1. 把可见内容居中放进正方形画布（留 8% 边距、透明底）——源图不是正方形时也不会变形；
2. 按各端要求缩放出下列文件：

    desktop/assets/Icon.png           1024x1024  打包进 exe / .app / linux 桌面图标
    web/public/logo.png                512x512   站点图标 + 页面左上角品牌图
    web/public/apple-touch-icon.png    180x180   iOS 添加到主屏
    web/public/favicon.png              64x64    浏览器标签页
    web/public/favicon.ico    16/32/48/64       老浏览器兜底

只依赖 Pillow：`python -m pip install pillow`。
"""

from __future__ import annotations

import sys
from pathlib import Path

try:
    from PIL import Image
except ModuleNotFoundError:  # pragma: no cover - 只在缺依赖时触发
    sys.exit("需要 Pillow：python -m pip install pillow")

ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "logo.png"

# (输出路径, 边长)
PNG_OUTPUTS = [
    (ROOT / "desktop" / "assets" / "Icon.png", 1024),
    (ROOT / "web" / "public" / "logo.png", 512),
    (ROOT / "web" / "public" / "apple-touch-icon.png", 180),
    (ROOT / "web" / "public" / "favicon.png", 64),
]

ICO_PATH = ROOT / "web" / "public" / "favicon.ico"
ICO_SIZES = [16, 32, 48, 64]

MARGIN = 0.08  # 正方形画布四周留白比例


def square(img: Image.Image) -> Image.Image:
    """把图居中裁到正方形画布（透明底），并留一点边距。"""
    img = img.convert("RGBA")
    bbox = img.getbbox()
    if bbox is None:
        sys.exit("logo.png 是全透明的，没法做图标")
    art = img.crop(bbox)

    side = round(max(art.size) * (1 + 2 * MARGIN))
    canvas = Image.new("RGBA", (side, side), (0, 0, 0, 0))
    canvas.paste(art, ((side - art.width) // 2, (side - art.height) // 2))
    return canvas


def main() -> int:
    if not SOURCE.exists():
        sys.exit(f"缺少源图 {SOURCE.name}（放在仓库根目录）")
    base = square(Image.open(SOURCE))
    print(f"源图 {Image.open(SOURCE).size} -> 正方形 {base.size}")

    for path, size in PNG_OUTPUTS:
        path.parent.mkdir(parents=True, exist_ok=True)
        out = base if size == base.width else base.resize((size, size), Image.LANCZOS)
        out.save(path, optimize=True)
        print(f"  {path.relative_to(ROOT)}  {size}x{size}  {path.stat().st_size} B")

    ICO_PATH.parent.mkdir(parents=True, exist_ok=True)
    base.save(ICO_PATH, format="ICO", sizes=[(size, size) for size in ICO_SIZES])
    print(f"  {ICO_PATH.relative_to(ROOT)}  {ICO_SIZES}  {ICO_PATH.stat().st_size} B")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
