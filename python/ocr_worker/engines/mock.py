"""离线占位引擎：只读图片尺寸，返回固定的三行文字。

它的存在有两个理由：
1. CI / 无显卡环境里能完整跑通「上传 → 识别 → 历史 → 导出」；
2. 排查「是引擎坏了还是链路坏了」时，切到 mock 一眼就能分清。
"""

from __future__ import annotations

from typing import Any

from ..images import Bitmap
from .base import Block, Engine, Page, register

DEMO_LINES = [
    ("OCR Manager 离线演示", "title"),
    ("Mock 引擎不加载任何模型，返回这一段固定的示例文本。", "text"),
    ("Invoice No. 2026-0017 · Total 128.00 · 金额壹佰贰拾捌元整", "text"),
    ("第三行用来验证多行拼接、置信度和坐标框的显示。", "text"),
    ("MOCK / 0.99 confidence / 4 blocks", "text"),
]


@register
class MockEngine(Engine):
    name = "mock"

    def recognize(self, pages: list[Bitmap], options: dict[str, Any]) -> list[Page]:
        self.report("ocr", 0.0, "mock 引擎开始")
        limit = int(options.get("lines", 3) or 3)
        results: list[Page] = []
        total = max(1, len(pages))
        for position, bitmap in enumerate(pages):
            width = bitmap.width or 1000
            height = bitmap.height or 1400
            blocks: list[Block] = []
            count = min(limit, len(DEMO_LINES))
            margin = width * 0.08
            usable = height * 0.8
            line_height = usable / max(1, count) * 0.55
            step = usable / max(1, count)
            for row in range(count):
                text, kind = DEMO_LINES[row]
                top = height * 0.1 + row * step
                bottom = top + line_height
                blocks.append(
                    Block(
                        text=text,
                        box=[
                            [margin, top],
                            [width - margin, top],
                            [width - margin, bottom],
                            [margin, bottom],
                        ],
                        confidence=0.99 - row * 0.01,
                        kind=kind,
                    )
                )
            results.append(Page(index=bitmap.index, width=width, height=height, blocks=blocks).finish())
            percent = (position + 1) / total * 100.0
            self.report("ocr", percent, f"mock 已处理 {position + 1}/{total} 页")
        return results
