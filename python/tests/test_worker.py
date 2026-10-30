import json
import os
import subprocess
import sys
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def tiny_png(path: str, width: int = 800, height: int = 600) -> str:
    import struct
    import zlib

    raw = b""
    for _ in range(height):
        raw += b"\x00" + b"\xff\xff\xff" * width

    def chunk(kind: bytes, data: bytes) -> bytes:
        body = kind + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

    png = b"\x89PNG\r\n\x1a\n"
    png += chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
    png += chunk(b"IDAT", zlib.compress(raw))
    png += chunk(b"IEND", b"")
    with open(path, "wb") as fh:
        fh.write(png)
    return path


class WorkerProtocolTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="ocr-worker-test-")
        self.image = tiny_png(os.path.join(self.tmp, "page.png"))
        self.env = dict(os.environ, PYTHONPATH=ROOT)
        self.proc = subprocess.Popen(
            [sys.executable, "-m", "ocr_worker", "--model", "mock", "--engine", "mock", "--models-dir", self.tmp],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            cwd=ROOT,
            env=self.env,
        )

    def tearDown(self):
        try:
            self.proc.stdin.close()  # type: ignore[union-attr]
        except Exception:
            pass
        try:
            self.proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            self.proc.kill()
        for stream in (self.proc.stdout, self.proc.stderr):
            if stream:
                stream.close()

    def send(self, payload):
        assert self.proc.stdin is not None
        self.proc.stdin.write((json.dumps(payload, ensure_ascii=False) + "\n").encode("utf-8"))
        self.proc.stdin.flush()

    def read_until_result(self, request_id: str):
        assert self.proc.stdout is not None
        events = []
        while True:
            line = self.proc.stdout.readline()
            if not line:
                stderr = self.proc.stderr.read().decode("utf-8", "replace") if self.proc.stderr else ""
                self.fail(f"worker 提前退出：{stderr}")
            payload = json.loads(line.decode("utf-8"))
            if payload.get("id") == request_id and payload.get("type") in ("result", "error"):
                return payload, events
            events.append(payload)

    def test_info_and_recognize(self):
        self.send({"id": "1", "cmd": "info"})
        info, _ = self.read_until_result("1")
        self.assertTrue(info["ok"])
        self.assertEqual(info["engine"], "mock")
        self.assertIn("mock", info["available_engines"])

        self.send({"id": "2", "cmd": "load"})
        loaded, _ = self.read_until_result("2")
        self.assertTrue(loaded["ok"])

        self.send({"id": "3", "cmd": "recognize", "file": self.image, "options": {"lines": 2}})
        result, events = self.read_until_result("3")
        self.assertTrue(result["ok"], result)
        self.assertEqual(len(result["pages"]), 1)
        page = result["pages"][0]
        self.assertEqual((page["width"], page["height"]), (800, 600))
        self.assertEqual(len(page["blocks"]), 2)
        self.assertIn("OCR Manager", page["text"])
        self.assertIn("markdown", page)
        self.assertTrue(any(ev.get("type") == "progress" for ev in events))

    def test_unknown_engine_reports_error_without_dying(self):
        self.send({"id": "1", "cmd": "load"})
        self.read_until_result("1")
        self.send({"id": "2", "cmd": "nope"})
        err, _ = self.read_until_result("2")
        self.assertFalse(err["ok"])
        self.assertIn("未知命令", err["message"])

        self.send({"id": "3", "cmd": "ping"})
        pong, _ = self.read_until_result("3")
        self.assertTrue(pong["pong"])

    def test_missing_file_is_reported(self):
        self.send({"id": "1", "cmd": "recognize", "file": os.path.join(self.tmp, "nope.png")})
        err, _ = self.read_until_result("1")
        self.assertFalse(err["ok"])
        self.assertIn("打不开图片", err["message"])


if __name__ == "__main__":
    unittest.main()
