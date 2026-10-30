"""external：只登记、不代管的模型（MinerU / Surya 这类独立工具）。

清单里留一条是为了让用户在同一个界面里对比选型、点进官网看用法；
真要用它们，去它们自己的环境里跑。
"""

from __future__ import annotations

from typing import Any

from ..images import Bitmap, EngineError
from .base import Block, Engine, Page, register


@register
class ExternalEngine(Engine):
    name = "external"

    def load(self) -> None:
        raise EngineError(self._message())

    def recognize(self, pages: list[Bitmap], options: dict[str, Any]) -> list[Page]:
        raise EngineError(self._message())

    def _message(self) -> str:
        command = str(self.options.get("command") or "").strip()
        hint = f"可以这样用：{command}" if command else "请参考它的官方文档。"
        return (
            f"「{self.ctx.model_id}」是外部登记模型，本应用不代管它的运行环境。{hint}"
        )
