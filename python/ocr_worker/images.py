"""图片尺寸探测与像素读取。

`mock` 引擎只靠标准库读尺寸（这样离线 / CI 里连 Pillow 都不用装）；
真引擎走 Pillow，并按 `max_side` 缩放，避免 4K 扫描件把检测模型撑爆。
"""

from __future__ import annotations

import os
import struct
from dataclasses import dataclass


class EngineError(RuntimeError):
    """引擎可预期的失败（缺依赖 / 图片打不开），Go 侧会当用户错误显示。"""


def probe_size(path: str) -> tuple[int, int]:
    """只用标准库读常见格式的宽高；读不出来返回 (0, 0)。"""
    try:
        with open(path, "rb") as fh:
            head = fh.read(32)
            if len(head) < 2:
                return (0, 0)

            if head.startswith(b"\x89PNG\r\n\x1a\n"):
                if len(head) >= 24:
                    width, height = struct.unpack(">II", head[16:24])
                    return (int(width), int(height))
                return (0, 0)

            if head.startswith(b"GIF8"):
                width, height = struct.unpack("<HH", head[6:10])
                return (int(width), int(height))

            if head.startswith(b"BM"):
                fh.seek(18)
                raw = fh.read(8)
                if len(raw) == 8:
                    width, height = struct.unpack("<ii", raw)
                    return (abs(int(width)), abs(int(height)))
                return (0, 0)

            if head.startswith(b"RIFF") and head[8:12] == b"WEBP":
                return _probe_webp(fh, head)

            if head.startswith(b"\xff\xd8"):
                fh.seek(2)
                return _probe_jpeg(fh)
    except OSError:
        return (0, 0)
    return (0, 0)


def _probe_webp(fh, head: bytes) -> tuple[int, int]:
    kind = head[12:16]
    fh.seek(12)
    chunk = fh.read(30)
    if kind == b"VP8X" and len(chunk) >= 20:
        width = 1 + int.from_bytes(chunk[12:15], "little")
        height = 1 + int.from_bytes(chunk[15:18], "little")
        return (width, height)
    if kind == b"VP8 " and len(chunk) >= 20:
        width = int.from_bytes(chunk[14:16], "little") & 0x3FFF
        height = int.from_bytes(chunk[16:18], "little") & 0x3FFF
        return (width, height)
    if kind == b"VP8L" and len(chunk) >= 20:
        bits = int.from_bytes(chunk[9:13], "little")
        width = (bits & 0x3FFF) + 1
        height = ((bits >> 14) & 0x3FFF) + 1
        return (width, height)
    return (0, 0)


def _probe_jpeg(fh) -> tuple[int, int]:
    while True:
        marker = fh.read(2)
        if len(marker) < 2 or marker[0] != 0xFF:
            return (0, 0)
        code = marker[1]
        if code in (0xD8, 0xD9) or 0xD0 <= code <= 0xD7:
            continue
        size_raw = fh.read(2)
        if len(size_raw) < 2:
            return (0, 0)
        size = struct.unpack(">H", size_raw)[0]
        if size < 2:
            return (0, 0)
        if 0xC0 <= code <= 0xCF and code not in (0xC4, 0xC8, 0xCC):
            body = fh.read(5)
            if len(body) < 5:
                return (0, 0)
            height, width = struct.unpack(">HH", body[1:5])
            return (int(width), int(height))
        fh.seek(size - 2, 1)


@dataclass
class Bitmap:
    """一页待识别的位图。`image` 为 None 表示只有尺寸（mock 的轻量路径）。"""

    index: int
    path: str
    width: int
    height: int
    image: object | None = None
    # 原图 → 位图的缩放比（框坐标乘回去就是原图坐标）。
    scale: float = 1.0


def load_bitmap(path: str, index: int = 0, max_side: int = 0, need_pixels: bool = True) -> Bitmap:
    if not os.path.isfile(path):
        raise EngineError(f"打不开图片 {path}：文件不存在")
    width, height = probe_size(path)
    if not need_pixels:
        if width <= 0 or height <= 0:
            raise EngineError(f"打不开图片 {path}：不是可识别的图片格式（png/jpeg/gif/bmp/webp）")
        return Bitmap(index=index, path=path, width=width, height=height)

    try:
        from PIL import Image, ImageOps
    except ImportError as exc:  # pragma: no cover - 依赖缺失时才走到
        raise EngineError("缺少 Pillow：请先在「设置 → Python 环境」安装依赖") from exc

    try:
        with Image.open(path) as opened:
            image = ImageOps.exif_transpose(opened)
            image.load()
    except Exception as exc:  # noqa: BLE001 - Pillow 的异常类型太多
        raise EngineError(f"打不开图片 {path}：{exc}") from exc

    image = image.convert("RGB")
    scale = 1.0
    if max_side and max(image.size) > max_side:
        ratio = max_side / float(max(image.size))
        new_size = (max(1, int(image.width * ratio)), max(1, int(image.height * ratio)))
        image = image.resize(new_size, Image.LANCZOS)
        scale = new_size[0] / float(width or new_size[0])
        width, height = new_size
    elif width <= 0 or height <= 0:
        width, height = image.size

    return Bitmap(index=index, path=path, width=int(width), height=int(height), image=image, scale=scale)


def to_numpy(image):
    """PIL → RGB ndarray（引擎统一吃 ndarray，省得各自转）。"""
    try:
        import numpy as np
    except ImportError as exc:  # pragma: no cover
        raise EngineError("缺少 numpy：请先在「设置 → Python 环境」安装依赖") from exc
    return np.array(image.convert("RGB"))
