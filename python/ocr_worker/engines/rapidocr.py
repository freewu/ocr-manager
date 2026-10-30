"""RapidOCR：PaddleOCR 检测/识别模型的 ONNX 版，纯 CPU 就能跑。

兼容两代包名：
- `rapidocr`（2.x/3.x，`from rapidocr import RapidOCR`）
- `rapidocr_onnxruntime`（旧包名）
两代的返回结构也不同（对象 / (结果, 耗时) 元组），这里统一成 Block。
"""

from __future__ import annotations

from typing import Any

from ..images import Bitmap, EngineError, to_numpy
from .base import Block, Engine, Page, register


@register
class RapidOcrEngine(Engine):
    name = "rapidocr"

    def __init__(self, ctx) -> None:
        super().__init__(ctx)
        self._engine = None
        self._api = ""

    def load(self) -> None:
        options = self.options
        engine = None
        last_error: Exception | None = None

        for module_name, class_name in (("rapidocr", "RapidOCR"), ("rapidocr_onnxruntime", "RapidOCR")):
            try:
                module = __import__(module_name, fromlist=[class_name])
                cls = getattr(module, class_name)
            except Exception as exc:  # noqa: BLE001 - 缺包 / 版本不兼容都算
                last_error = exc
                continue
            try:
                engine = cls()
                self._api = module_name
                break
            except Exception as exc:  # noqa: BLE001
                last_error = exc

        if engine is None:
            raise EngineError(
                "RapidOCR 依赖还没装或加载失败：在「模型」页点这个模型的「下载」重新安装依赖"
                + (f"（{last_error}）" if last_error else "")
            )

        self._engine = engine
        self.loaded = True
        self.report("load", 100.0, f"RapidOCR 就绪（{self._api}）")

    def recognize(self, pages: list[Bitmap], options: dict[str, Any]) -> list[Page]:
        if self._engine is None:
            self.load()
        assert self._engine is not None

        total = max(1, len(pages))
        results: list[Page] = []
        for position, bitmap in enumerate(pages):
            if bitmap.image is None:
                raise EngineError("RapidOCR 需要像素数据，拿到的是空位图")
            self.report("ocr", position / total * 100.0, f"识别第 {position + 1}/{total} 页")
            raw = self._engine(to_numpy(bitmap.image))
            blocks = _parse(raw)
            results.append(Page(index=bitmap.index, width=bitmap.width, height=bitmap.height, blocks=blocks).finish())
            self.report("ocr", (position + 1) / total * 100.0, f"已识别 {position + 1}/{total} 页")
        return results


def _parse(raw: Any) -> list[Block]:
    """把 RapidOCR 各种返回形状摊平成 Block 列表。"""
    lines = _extract_lines(raw)
    blocks: list[Block] = []
    for line in lines:
        box, text, score = line
        if not text:
            continue
        blocks.append(Block(text=str(text), box=_as_points(box), confidence=_as_score(score)))
    return blocks


def _extract_lines(raw: Any) -> list[tuple[Any, str, Any]]:
    # 新 API：对象带 .boxes / .txts / .scores
    boxes = getattr(raw, "boxes", None)
    texts = getattr(raw, "txts", None)
    scores = getattr(raw, "scores", None)
    if boxes is not None and texts is not None:
        scores = list(scores) if scores is not None else [None] * len(texts)
        return list(zip(boxes, list(texts), scores))

    # 旧 API：(结果, 耗时)，结果是 [[box, text, score], ...]
    if isinstance(raw, tuple) and raw and isinstance(raw[0], (list, tuple)):
        raw = raw[0]
    if isinstance(raw, dict):
        raw = raw.get("result", raw.get("data", []))
    if not raw:
        return []

    lines: list[tuple[Any, str, Any]] = []
    for item in raw:
        if isinstance(item, (list, tuple)) and len(item) >= 3:
            lines.append((item[0], item[1], item[2]))
        elif isinstance(item, dict):
            lines.append((item.get("box") or item.get("points") or [], item.get("text", ""), item.get("score")))
    return lines


def _as_points(box: Any) -> list[list[float]]:
    if box is None:
        return []
    try:
        points = [[float(point[0]), float(point[1])] for point in box]
    except (TypeError, ValueError, IndexError):
        return []
    return points


def _as_score(score: Any) -> float | None:
    try:
        value = float(score)
    except (TypeError, ValueError):
        return None
    return value
