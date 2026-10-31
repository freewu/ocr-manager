import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from ocr_worker.engines.base import EngineError  # noqa: E402
from ocr_worker.engines.paddle import ONEDNN_MARK, _engine_kwargs, _explain  # noqa: E402


class OneDNNExplainTest(unittest.TestCase):
    """Paddle 3.x 的 oneDNN 后端在 PIR 执行器上会直接崩，报错得翻成人话。"""

    def test_translates_onednn_error(self):
        raw = RuntimeError(
            "(Unimplemented) ConvertPirAttribute2RuntimeAttribute not support "
            "[pir::ArrayAttribute<pir::DoubleAttribute>] "
            "(at ..\\paddle\\fluid\\framework\\new_executor\\instruction\\onednn_instruction.cc:118)"
        )
        out = _explain(raw)
        self.assertIsInstance(out, EngineError)
        self.assertIn("enable_mkldnn", str(out))
        self.assertIn("paddlepaddle", str(out))

    def test_marker_matches_real_message(self):
        self.assertIn("ConvertPirAttribute2RuntimeAttribute", ONEDNN_MARK)

    def test_other_errors_pass_through(self):
        raw = RuntimeError("CUDA out of memory")
        self.assertIs(_explain(raw), raw)


class DefaultOptionsTest(unittest.TestCase):
    """默认不能开 oneDNN：开了必崩（见上）。"""

    def test_mkldnn_off_by_default(self):
        self.assertIs(_engine_kwargs({})["enable_mkldnn"], False)

    def test_mkldnn_can_be_enabled_explicitly(self):
        self.assertIs(_engine_kwargs({"enable_mkldnn": True})["enable_mkldnn"], True)

    def test_lang_defaults_to_chinese(self):
        self.assertEqual(_engine_kwargs({})["lang"], "ch")
        self.assertEqual(_engine_kwargs({"lang": "en"})["lang"], "en")

    def test_unknown_options_are_not_forwarded(self):
        # 模型选项里会有引擎自己的字段（prompt 之类），不能原样丢给 PaddleOCR：
        # 它会直接抛 ValueError: Unknown argument
        kwargs = _engine_kwargs({"prompt": "x", "max_new_tokens": 8})
        self.assertNotIn("prompt", kwargs)
        self.assertNotIn("max_new_tokens", kwargs)


if __name__ == "__main__":
    unittest.main()
