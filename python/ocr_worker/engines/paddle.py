"""PaddleOCR（PP-OCRv5）：检测 + 方向 + 识别三段式，中文场景的主力。

PaddleOCR 3.x 自己管模型下载（缓存目录用 `PADDLE_PDX_CACHE_HOME` 指到权重目录），
所以「下载」按钮这里只负责把包和首次推理的模型缓存落到应用自己的目录里。
"""

from __future__ import annotations

import os
from typing import Any

from ..images import Bitmap, EngineError, to_numpy
from .base import Block, Engine, Page, register


@register
class PaddleEngine(Engine):
    name = "paddle"

    def __init__(self, ctx) -> None:
        super().__init__(ctx)
        self._engine = None
        self._mode = ""

    def load(self) -> None:
        try:
            from paddleocr import PaddleOCR
        except ImportError as exc:
            raise EngineError("PaddleOCR 依赖还没装：在「模型」页点这个模型的「下载」，或在「设置 → Python 环境」安装依赖") from exc

        options = self.options
        cache_dir = os.path.join(self.ctx.models_dir, "paddlex")
        os.makedirs(cache_dir, exist_ok=True)
        os.environ.setdefault("PADDLE_PDX_CACHE_HOME", cache_dir)

        kwargs = _engine_kwargs(options)
        try:
            self._engine = PaddleOCR(**kwargs)
            self._mode = "predict"
        except TypeError:
            # 2.x：参数名不一样，用最小集合
            self._engine = PaddleOCR(lang=str(options.get("lang") or "ch"), use_angle_cls=True)
            self._mode = "ocr"
        self.loaded = True
        self.report("load", 100.0, "PaddleOCR 就绪")

    def recognize(self, pages: list[Bitmap], options: dict[str, Any]) -> list[Page]:
        if self._engine is None:
            self.load()
        assert self._engine is not None

        total = max(1, len(pages))
        results: list[Page] = []
        for position, bitmap in enumerate(pages):
            if bitmap.image is None:
                raise EngineError("PaddleOCR 需要像素数据，拿到的是空位图")
            self.report("ocr", position / total * 100.0, f"识别第 {position + 1}/{total} 页")
            array = to_numpy(bitmap.image)
            blocks = self._run(array)
            results.append(Page(index=bitmap.index, width=bitmap.width, height=bitmap.height, blocks=blocks).finish())
            self.report("ocr", (position + 1) / total * 100.0, f"已识别 {position + 1}/{total} 页")
        return results

    def _run(self, array) -> list[Block]:
        assert self._engine is not None
        try:
            if self._mode == "predict":
                output = self._engine.predict(array)
            else:
                output = self._engine.ocr(array)
        except Exception as exc:  # noqa: BLE001
            raise _explain(exc) from exc
        return _parse(output)


# paddle 的 oneDNN 后端在 PIR 执行器下没实现的算子：报错只有底层 C++ 名字，
# 用户完全不知道该怎么办，这里翻成人话。
ONEDNN_MARK = "ConvertPirAttribute2RuntimeAttribute"


def _engine_kwargs(options: dict[str, Any]) -> dict[str, Any]:
    """把模型选项翻成 PaddleOCR 的构造参数。

    Paddle 3.x 在 CPU 上默认开 oneDNN（MKLDNN）推理，但它在 PIR 新执行器下
    有很多算子没实现，典型报错（识别必崩）：

        NotImplementedError: ConvertPirAttribute2RuntimeAttribute not support
        [pir::ArrayAttribute<pir::DoubleAttribute>]

    所以默认走纯 paddle 后端；想试 MKLDNN 加速的在模型选项里写
    `enable_mkldnn = true`。
    """
    kwargs: dict[str, Any] = {"lang": str(options.get("lang") or "ch")}
    for key in ("use_doc_orientation_classify", "use_doc_unwarping", "use_textline_orientation"):
        if key in options:
            kwargs[key] = bool(options[key])
    kwargs["enable_mkldnn"] = bool(options.get("enable_mkldnn", False))
    return kwargs


def _explain(exc: Exception) -> Exception:
    """把 paddle 底层那些「看不懂但好修」的异常翻成能照着做的提示。"""
    if ONEDNN_MARK in str(exc):
        return EngineError(
            "Paddle 的 oneDNN（MKLDNN）加速在当前的 paddlepaddle 上跑不起来："
            "模型选项里把 enable_mkldnn 关掉（默认就是关的），"
            "或在「设置 → Python 环境」里把 paddlepaddle 升级到 3.1 以上"
        )
    return exc


def _parse(output: Any) -> list[Block]:
    blocks: list[Block] = []
    for page in _as_items(output):
        texts = _field(page, "rec_texts") or _field(page, "texts")
        scores = _field(page, "rec_scores") or _field(page, "scores")
        boxes = _field(page, "rec_polys") or _field(page, "dt_polys") or _field(page, "boxes")
        if texts and boxes is not None:
            for index, text in enumerate(texts):
                score = scores[index] if scores and index < len(scores) else None
                box = boxes[index] if index < len(boxes) else []
                blocks.append(Block(text=str(text), box=_points(box), confidence=_float(score)))
            continue
        # 2.x 形状：[[box, (text, score)], ...]
        for line in page or []:
            if not isinstance(line, (list, tuple)) or len(line) < 2:
                continue
            box, payload = line[0], line[1]
            if isinstance(payload, (list, tuple)) and payload:
                text, score = str(payload[0]), payload[1] if len(payload) > 1 else None
            else:
                text, score = str(payload), None
            blocks.append(Block(text=text, box=_points(box), confidence=_float(score)))
    return blocks


def _as_items(output: Any) -> list[Any]:
    if output is None:
        return []
    if isinstance(output, dict):
        return [output]
    if isinstance(output, (list, tuple)):
        return list(output)
    # OCRResult 之类：支持 [] / to_dict
    try:
        return [output.to_dict()]
    except Exception:  # noqa: BLE001
        return [output]


def _field(page: Any, name: str) -> Any:
    try:
        value = page[name]
    except Exception:  # noqa: BLE001
        value = getattr(page, name, None)
    return value


def _points(box: Any) -> list[list[float]]:
    try:
        return [[float(point[0]), float(point[1])] for point in box]
    except (TypeError, ValueError, IndexError):
        return []


def _float(value: Any) -> float | None:
    try:
        return float(value)
    except (TypeError, ValueError):
        return None
