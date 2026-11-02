"""images.load_bitmap 的报错文案。

背景：装依赖会把 venv 里的 Pillow 换掉，正在跑的 worker 再打开图片时
可能抛一个**没带任何文字**的异常，界面上就显示「打不开图片 xxx：」后面
一片空白，用户完全没法猜。这里保证至少能看到异常类型名。
"""

from __future__ import annotations

import base64
import os
import tempfile
import unittest
from unittest import mock

from ocr_worker import images

# 1×1 透明 PNG，够 probe_size 认出尺寸
TINY_PNG = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
)


class LoadBitmapErrorTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.mkdtemp(prefix="ocr-images-")
        self.path = os.path.join(self.tmp, "page.png")
        with open(self.path, "wb") as fh:
            fh.write(TINY_PNG)

    def tearDown(self) -> None:
        for name in os.listdir(self.tmp):
            os.remove(os.path.join(self.tmp, name))
        os.rmdir(self.tmp)

    def test_missing_file(self) -> None:
        with self.assertRaises(images.EngineError) as ctx:
            images.load_bitmap(os.path.join(self.tmp, "nope.png"))
        self.assertIn("文件不存在", str(ctx.exception))

    def test_empty_exception_message_falls_back_to_type(self) -> None:
        with mock.patch("PIL.Image.open", side_effect=OSError()):
            with self.assertRaises(images.EngineError) as ctx:
                images.load_bitmap(self.path)
        message = str(ctx.exception)
        self.assertIn("打不开图片", message)
        self.assertIn("OSError", message)

    def test_exception_message_is_kept(self) -> None:
        with mock.patch("PIL.Image.open", side_effect=OSError("image file is truncated")):
            with self.assertRaises(images.EngineError) as ctx:
                images.load_bitmap(self.path)
        self.assertIn("image file is truncated", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
