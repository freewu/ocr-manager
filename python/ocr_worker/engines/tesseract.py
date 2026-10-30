"""Tesseract：调用系统安装的 tesseract（pytesseract 只是薄封装）。

Windows 上装 UB Mannheim 发行版，中文要勾 `Chinese (Simplified)` 语言包；
Linux 用 `apt install tesseract-ocr tesseract-ocr-chi-sim`。
"""

from __future__ import annotations

import shutil
from typing import Any

from ..images import Bitmap, EngineError
from .base import Block, Engine, Page, register


@register
class TesseractEngine(Engine):
    name = "tesseract"

    def load(self) -> None:
        try:
            import pytesseract  # noqa: F401
        except ImportError as exc:
            raise EngineError("缺少 pytesseract：在「模型」页点这个模型的「下载」，或在「设置 → Python 环境」安装依赖") from exc
        if not shutil.which("tesseract"):
            raise EngineError(
                "系统里找不到 tesseract：装 https://github.com/UB-Mannheim/tesseract/wiki "
                "（或 `apt install tesseract-ocr`），并确认它在 PATH 里"
            )
        self.loaded = True
        self.report("load", 100.0, "Tesseract 就绪")

    def recognize(self, pages: list[Bitmap], options: dict[str, Any]) -> list[Page]:
        if not self.loaded:
            self.load()
        import pytesseract
        from pytesseract import Output

        lang = str(options.get("lang") or "chi_sim+eng")
        psm = str(options.get("psm") or "6")
        config = f"--psm {psm}"
        total = max(1, len(pages))
        results: list[Page] = []

        for position, bitmap in enumerate(pages):
            if bitmap.image is None:
                raise EngineError("Tesseract 需要像素数据，拿到的是空位图")
            self.report("ocr", position / total * 100.0, f"识别第 {position + 1}/{total} 页")
            data = pytesseract.image_to_data(bitmap.image, lang=lang, config=config, output_type=Output.DICT)
            blocks = _group_lines(data)
            results.append(Page(index=bitmap.index, width=bitmap.width, height=bitmap.height, blocks=blocks).finish())
            self.report("ocr", (position + 1) / total * 100.0, f"已识别 {position + 1}/{total} 页")
        return results


def _group_lines(data: dict[str, list]) -> list[Block]:
    """把 word 级别的结果按 (block, par, line) 聚成行。"""
    groups: dict[tuple[int, int, int], dict[str, Any]] = {}
    order: list[tuple[int, int, int]] = []
    count = len(data.get("text", []))
    for idx in range(count):
        text = (data["text"][idx] or "").strip()
        if not text:
            continue
        try:
            conf = float(data["conf"][idx])
        except (TypeError, ValueError):
            conf = -1.0
        if conf < 0:
            continue
        key = (
            int(data.get("block_num", [0] * count)[idx]),
            int(data.get("par_num", [0] * count)[idx]),
            int(data.get("line_num", [0] * count)[idx]),
        )
        left = int(data["left"][idx])
        top = int(data["top"][idx])
        width = int(data["width"][idx])
        height = int(data["height"][idx])
        entry = groups.get(key)
        if entry is None:
            entry = {"words": [], "scores": [], "left": left, "top": top, "right": left + width, "bottom": top + height}
            groups[key] = entry
            order.append(key)
        entry["words"].append(text)
        entry["scores"].append(conf / 100.0)
        entry["left"] = min(entry["left"], left)
        entry["top"] = min(entry["top"], top)
        entry["right"] = max(entry["right"], left + width)
        entry["bottom"] = max(entry["bottom"], top + height)

    blocks: list[Block] = []
    for key in order:
        entry = groups[key]
        scores = entry["scores"] or [0.0]
        left, top, right, bottom = entry["left"], entry["top"], entry["right"], entry["bottom"]
        blocks.append(
            Block(
                text=" ".join(entry["words"]),
                box=[[left, top], [right, top], [right, bottom], [left, bottom]],
                confidence=sum(scores) / len(scores),
                meta={"block": key[0], "par": key[1]},
            )
        )
    return blocks
