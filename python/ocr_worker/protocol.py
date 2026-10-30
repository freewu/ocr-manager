"""JSONL 协议：一行一条 JSON，stdout 只走协议。

第三方库（tqdm、transformers、paddle）都会往 stdout 打日志，一旦混进协议流，
Go 侧就会看到半行 JSON。所以进程一开始就把 fd 1 换成 stderr，只留一个私有 fd
给协议用（`make_transport`）。
"""

from __future__ import annotations

import json
import os
import sys
import threading
from typing import Any, BinaryIO


class Transport:
    """线程安全的 JSONL 写出端。"""

    def __init__(self, stream: BinaryIO) -> None:
        self._stream = stream
        self._lock = threading.Lock()

    def send(self, payload: dict[str, Any]) -> None:
        data = json.dumps(payload, ensure_ascii=False)
        line = data.encode("utf-8", "replace") + b"\n"
        with self._lock:
            try:
                self._stream.write(line)
                self._stream.flush()
            except (BrokenPipeError, ValueError):
                # 父进程没了，进程跟着退就行
                raise SystemExit(0) from None


def make_transport() -> Transport:
    """抢下 stdout 的协议通道，并把 fd 1 改成 stderr。"""
    proto_fd = os.dup(1)
    try:
        os.dup2(2, 1)
    except OSError:
        pass
    sys.stdout = sys.stderr
    stream = os.fdopen(proto_fd, "wb", buffering=0)
    return Transport(stream)


class ProtocolWriter:
    """把事件 + 请求回复写成一行的辅助类。"""

    def __init__(self, transport: Transport, request_id: str | None = None) -> None:
        self.transport = transport
        self.request_id = request_id

    def result(self, payload: dict[str, Any]) -> None:
        body = {"id": self.request_id, "type": "result", "ok": True}
        body.update(payload)
        self.transport.send(body)

    def error(self, message: str, kind: str = "error") -> None:
        self.transport.send(
            {
                "id": self.request_id,
                "type": "error",
                "ok": False,
                "message": message,
                "kind": kind,
            }
        )

    def progress(self, stage: str, percent: float | None, message: str = "") -> None:
        body: dict[str, Any] = {
            "id": self.request_id,
            "type": "progress",
            "stage": stage,
            "message": message,
        }
        if percent is not None:
            body["percent"] = max(0.0, min(100.0, float(percent)))
        self.transport.send(body)

    def log(self, level: str, message: str) -> None:
        self.transport.send({"type": "log", "level": level, "message": message})


def read_requests(stream: BinaryIO):
    """逐行读请求；空行跳过，坏行报一条 error 但不退出。"""
    for raw in stream:
        line = raw.decode("utf-8", "replace").strip()
        if not line:
            continue
        try:
            value = json.loads(line)
        except json.JSONDecodeError as exc:
            yield {"id": None, "cmd": "__bad__", "raw": line, "error": str(exc)}
            continue
        if isinstance(value, dict):
            yield value
