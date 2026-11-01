"""外部命令行引擎（MinerU / Surya）的解析与查找逻辑。

真跑 CLI 需要装几 GB 的包，这里只测「把它的输出摊成 Block」这段纯逻辑。
"""

import json
import os
import sys
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if ROOT not in sys.path:
    sys.path.insert(0, ROOT)

from ocr_worker.engines import external  # noqa: E402
from ocr_worker.engines.base import Context  # noqa: E402


def make_engine(command: str) -> external.ExternalEngine:
    ctx = Context(
        model_id="surya",
        engine="external",
        repo="datalab-to/surya",
        models_dir="",
        hf_endpoint="",
        options={"command": command},
        report=lambda *args: None,
        log=lambda *args: None,
    )
    return external.ExternalEngine(ctx)


class FindCommandTest(unittest.TestCase):
    def test_finds_venv_script(self):
        # worker 自己的解释器就在 venv 里，同名文件一定存在
        name = os.path.basename(sys.executable)
        found = external.find_command(name)
        self.assertTrue(found, f"应该能在 venv 里找到 {name}")
        self.assertTrue(os.path.isfile(found))

    def test_missing_command_returns_empty(self):
        self.assertEqual(external.find_command("ocr-manager-没这个命令"), "")

    def test_command_name_from_options(self):
        self.assertEqual(make_engine("surya_ocr <image>")._command_name(), "surya_ocr")
        self.assertEqual(make_engine("  ")._command_name(), "")


class BoxTest(unittest.TestCase):
    def test_bbox_becomes_four_corners(self):
        self.assertEqual(
            external._box([1, 2, 3, 4]),
            [[1.0, 2.0], [3.0, 2.0], [3.0, 4.0], [1.0, 4.0]],
        )

    def test_polygon_passthrough(self):
        self.assertEqual(
            external._box([[0, 0], [2, 0], [2, 3], [0, 3]]),
            [[0.0, 0.0], [2.0, 0.0], [2.0, 3.0], [0.0, 3.0]],
        )

    def test_junk_is_empty(self):
        self.assertEqual(external._box(None), [])
        self.assertEqual(external._box("x"), [])


class SuryaParseTest(unittest.TestCase):
    def test_text_lines_become_blocks(self):
        blocks = external._surya_blocks(
            {
                "text_lines": [
                    {
                        "text": " 你好 ",
                        "confidence": 0.91,
                        "polygon": [[0, 0], [10, 0], [10, 5], [0, 5]],
                    },
                    {"text": "", "confidence": 0.2},
                    "纯字符串行",
                ]
            }
        )
        self.assertEqual([b.text for b in blocks], ["你好", "纯字符串行"])
        self.assertAlmostEqual(blocks[0].confidence, 0.91)
        self.assertEqual(len(blocks[0].box), 4)

    def test_list_payload_is_flattened(self):
        payload = [{"text_lines": [{"text": "a"}]}, {"lines": [{"text": "b"}]}]
        self.assertEqual([b.text for b in external._surya_blocks(payload)], ["a", "b"])

    def test_parse_reads_results_json(self):
        with tempfile.TemporaryDirectory() as out:
            os.makedirs(os.path.join(out, "page-1"))
            with open(os.path.join(out, "page-1", "results.json"), "w", encoding="utf-8") as fh:
                json.dump({"text_lines": [{"text": "来自 surya"}]}, fh, ensure_ascii=False)
            blocks, markdown = external._parse_surya(out, "/tmp/page-1.png")
        self.assertEqual([b.text for b in blocks], ["来自 surya"])
        self.assertIsNone(markdown)

    def test_parse_without_json_raises_readable_error(self):
        with tempfile.TemporaryDirectory() as out:
            with self.assertRaises(Exception) as ctx:
                external._parse_surya(out, "/tmp/page-1.png")
        self.assertIn("results.json", str(ctx.exception))


class MineruParseTest(unittest.TestCase):
    def test_markdown_becomes_blocks(self):
        with tempfile.TemporaryDirectory() as out:
            page_dir = os.path.join(out, "page", "auto")
            os.makedirs(page_dir)
            path = os.path.join(page_dir, "page.md")
            with open(path, "w", encoding="utf-8") as fh:
                fh.write("# 标题\n\n正文一行\n")
            blocks, markdown = external._parse_mineru(out, "/tmp/page.png")
        self.assertEqual([b.text for b in blocks], ["# 标题", "正文一行"])
        self.assertIn("正文一行", markdown)

    def test_without_markdown_raises(self):
        with tempfile.TemporaryDirectory() as out:
            with self.assertRaises(Exception) as ctx:
                external._parse_mineru(out, "/tmp/page.png")
        self.assertIn("markdown", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
