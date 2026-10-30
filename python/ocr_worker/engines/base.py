"""引擎公共接口。

每个引擎只实现三件事：`load`（把模型读进内存）、`prepare`（可选：下载权重）、
`recognize`（吃位图，吐块）。进度、日志通过 `Context` 回到 Go 侧。
"""

from __future__ import annotations

import time
from dataclasses import dataclass, field
from typing import Any, Callable

from ..compose import markdown_of, sort_blocks, text_of
from ..images import Bitmap, EngineError

__all__ = [
    "Block",
    "Page",
    "Context",
    "Engine",
    "EngineError",
    "register",
    "create",
    "available",
]


@dataclass
class Block:
    text: str
    box: list[list[float]] = field(default_factory=list)
    confidence: float | None = None
    kind: str = "text"
    meta: dict[str, Any] = field(default_factory=dict)

    def as_dict(self) -> dict[str, Any]:
        body: dict[str, Any] = {"text": self.text, "kind": self.kind}
        if self.box:
            body["box"] = [[round(float(x), 2), round(float(y), 2)] for x, y in self.box]
        if self.confidence is not None:
            body["confidence"] = round(float(self.confidence), 4)
        if self.meta:
            body["meta"] = self.meta
        return body


@dataclass
class Page:
    index: int
    width: int
    height: int
    blocks: list[Block] = field(default_factory=list)
    markdown: str | None = None
    # 这一页来自哪个输入文件（产物目录里的副本名）；多文件一次识别时前端按它分 tab
    source: str = ""

    def finish(self) -> "Page":
        self.blocks = sort_blocks(self.blocks)
        return self

    def as_dict(self) -> dict[str, Any]:
        body: dict[str, Any] = {
            "index": self.index,
            "width": self.width,
            "height": self.height,
            "text": text_of(self.blocks),
            "blocks": [block.as_dict() for block in self.blocks],
        }
        body["markdown"] = self.markdown if self.markdown is not None else markdown_of(self.blocks)
        if self.source:
            body["source"] = self.source
        return body


class Context:
    """一次 worker 生命周期的上下文（模型信息 + 上报通道）。"""

    def __init__(
        self,
        *,
        model_id: str,
        engine: str,
        repo: str,
        models_dir: str,
        hf_endpoint: str,
        options: dict[str, Any],
        report: Callable[[str, float | None, str], None],
        log: Callable[[str, str], None],
    ) -> None:
        self.model_id = model_id
        self.engine = engine
        self.repo = repo
        self.models_dir = models_dir
        self.hf_endpoint = hf_endpoint
        # 清单里的默认参数（识别请求里的 options 覆盖它）
        self.options = dict(options or {})
        self.report = report
        self.log = log
        self.started_at = time.time()

    def merged(self, options: dict[str, Any] | None) -> dict[str, Any]:
        out = dict(self.options)
        if options:
            out.update({key: value for key, value in options.items() if value is not None})
        return out


class Engine:
    """引擎基类：子类至少实现 `recognize`。"""

    name = "base"
    # 需要 GPU 的引擎在这里声明，Go 侧还有一份（清单里的 requires_gpu）
    requires_gpu = False

    def __init__(self, ctx: Context) -> None:
        self.ctx = ctx
        self.options = ctx.merged(None)
        self.loaded = False

    # ---------------------------------------------------------------- 生命周期

    def info(self) -> dict[str, Any]:
        return {"engine": self.name, "model": self.ctx.model_id}

    def load(self) -> None:
        self.loaded = True

    def prepare(self) -> None:
        """下载权重。不需要下载的引擎什么都不做。"""
        return

    def close(self) -> None:
        self.loaded = False

    # ---------------------------------------------------------------- 识别

    def recognize(self, pages: list[Bitmap], options: dict[str, Any]) -> list[Page]:
        raise NotImplementedError

    # ---------------------------------------------------------------- 工具

    def report(self, stage: str, percent: float | None, message: str = "") -> None:
        self.ctx.report(stage, percent, message)

    def log(self, message: str, level: str = "info") -> None:
        self.ctx.log(level, message)


_REGISTRY: dict[str, type[Engine]] = {}


def register(engine_cls: type[Engine]) -> type[Engine]:
    _REGISTRY[engine_cls.name] = engine_cls
    return engine_cls


def available() -> list[str]:
    _ensure_loaded()
    return sorted(_REGISTRY)


def create(engine: str, ctx: Context) -> Engine:
    # 引擎实现按需 import：装没装 extras 决定能不能加载，别拖累 mock
    _ensure_loaded()
    cls = _REGISTRY.get((engine or "").strip().lower())
    if cls is None:
        raise EngineError(f"未知引擎：{engine}（可用：{', '.join(available())}）")
    return cls(ctx)


_loaded = False


def _ensure_loaded() -> None:
    global _loaded
    if _loaded:
        return
    from . import mock, paddle, rapidocr, tesseract, vlm  # noqa: F401

    _loaded = True
