import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from ocr_worker.compose import markdown_of, sort_blocks, text_of  # noqa: E402
from ocr_worker.engines.base import Block, Page  # noqa: E402
from ocr_worker.images import probe_size  # noqa: E402


class BlockComposeTest(unittest.TestCase):
    def test_text_of_joins_lines(self):
        blocks = [Block("第一行"), Block("第二行"), Block("   ")]
        self.assertEqual(text_of(blocks), "第一行\n第二行")

    def test_markdown_headings_and_tables(self):
        blocks = [
            Block("标题", kind="title"),
            Block("正文", kind="text"),
            Block("| a | b |\n| - | - |", kind="table"),
            Block("x^2", kind="formula"),
        ]
        out = markdown_of(blocks)
        self.assertIn("## 标题", out)
        self.assertIn("| a | b |", out)
        self.assertIn("$$", out)

    def test_sort_blocks_reading_order(self):
        top_left = Block("1", box=[[10, 10], [50, 10], [50, 30], [10, 30]])
        top_right = Block("2", box=[[60, 12], [90, 12], [90, 32], [60, 32]])
        bottom = Block("3", box=[[10, 80], [90, 80], [90, 110], [10, 110]])
        ordered = [str(block.text) for block in sort_blocks([bottom, top_right, top_left])]
        self.assertEqual(ordered, ["1", "2", "3"])


class PageTest(unittest.TestCase):
    def test_page_dict_shape(self):
        page = Page(index=0, width=100, height=200, blocks=[Block("hello", box=[[0, 0], [1, 0], [1, 1], [0, 1]], confidence=0.91234)])
        body = page.finish().as_dict()
        self.assertEqual(body["index"], 0)
        self.assertEqual(body["text"], "hello")
        self.assertEqual(body["blocks"][0]["confidence"], 0.9123)
        self.assertEqual(body["markdown"], "hello")
        self.assertEqual(body["width"], 100)


class ProbeSizeTest(unittest.TestCase):
    def test_png_header(self):
        # 1x1 PNG 的 IHDR 里写的是 1x1
        raw = bytes.fromhex("89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c489")
        path = os.path.join(os.path.dirname(__file__), "_tiny.png")
        with open(path, "wb") as fh:
            fh.write(raw)
        try:
            self.assertEqual(probe_size(path), (1, 1))
        finally:
            os.remove(path)

    def test_missing_file(self):
        self.assertEqual(probe_size("/nonexistent/file.png"), (0, 0))


if __name__ == "__main__":
    unittest.main()
