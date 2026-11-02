"""守住「注册表」和「模型清单」对得上。

历史上 `_ensure_loaded()` 漏了 `external`，模型清单里写着
`engine = "external"` 的两个模型（MinerU / Surya）一点「启用」就报
`未知引擎：external（可用：mock, paddle, rapidocr, tesseract, vlm）`，
纯手工点一遍才发现。这里让它自动红。
"""

from __future__ import annotations

import tomllib
import unittest
from pathlib import Path

from ocr_worker.engines import available, create
from ocr_worker.engines.base import EngineError

ROOT = Path(__file__).resolve().parents[2]


def model_engines() -> set[str]:
    """config/models.toml 里出现过的所有 engine。"""
    data = tomllib.loads((ROOT / "config" / "models.toml").read_text(encoding="utf-8"))
    return {m["engine"] for m in data.get("models", []) if m.get("engine")}


class RegistryTest(unittest.TestCase):
    def test_registered(self) -> None:
        got = set(available())
        missing = model_engines() - got
        self.assertFalse(
            missing,
            f"模型清单用了这些引擎，但 worker 里没注册：{sorted(missing)}（已注册：{sorted(got)}）",
        )

    def test_external_is_registered(self) -> None:
        self.assertIn("external", available())

    def test_unknown_engine_lists_choices(self) -> None:
        # 报错文案要能直接看出「可用的是哪些」，别只甩个名字。
        with self.assertRaises(EngineError) as e:
            create("nope", None)  # type: ignore[arg-type]
        self.assertIn("未知引擎：nope", str(e.exception))
        self.assertIn("external", str(e.exception))


if __name__ == "__main__":
    unittest.main()
