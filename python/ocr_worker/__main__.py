"""worker 入口：读 JSONL 请求，按 cmd 分派，结果写回 stdout。

启动参数（Go 侧 `internal/pyenv.WorkerCommand` 负责拼）：
    python -m ocr_worker --model rapidocr --engine rapidocr --repo "" \
        --options '{"lang":"ch"}' --models-dir <dir> --hf-endpoint <url>

设计约束：
- 一条请求失败不能带崩进程：引擎报错 → 回 error，继续等下一条；
- stdout 只有协议（见 protocol.py），所有日志走 stderr；
- `load` 只加载一次，`recognize` 复用同一个引擎实例。
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import traceback
from typing import Any

from . import __version__
from .engines import Context, EngineError, create
from .images import Bitmap, load_bitmap
from .protocol import ProtocolWriter, make_transport, read_requests

TRANSPORT = make_transport()

# 当前请求的写回通道：Context 的 report/log 闭包用它，每次请求更新。
CURRENT: dict[str, ProtocolWriter | None] = {"writer": None}


def report(stage: str, percent: float | None, message: str = "") -> None:
    writer = CURRENT["writer"]
    if writer is not None:
        writer.progress(stage, percent, message)


def log(level: str, message: str) -> None:
    TRANSPORT.send({"type": "log", "level": level, "message": message})


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(prog="ocr_worker", add_help=True)
    parser.add_argument("--model", default="")
    parser.add_argument("--engine", default="mock")
    parser.add_argument("--repo", default="")
    parser.add_argument("--options", default="{}")
    parser.add_argument("--models-dir", default="")
    parser.add_argument("--hf-endpoint", default="")
    parser.add_argument("--offline", action="store_true")
    args, _unknown = parser.parse_known_args(argv)
    return args


def main(argv: list[str] | None = None) -> int:
    args = parse_args(list(sys.argv[1:] if argv is None else argv))
    try:
        options = json.loads(args.options or "{}")
        if not isinstance(options, dict):
            options = {}
    except json.JSONDecodeError:
        options = {}

    if args.models_dir:
        os.environ.setdefault("HF_HOME", args.models_dir)
    if args.offline:
        os.environ["HF_HUB_OFFLINE"] = "1"

    context = Context(
        model_id=args.model,
        engine=args.engine,
        repo=args.repo,
        models_dir=args.models_dir,
        hf_endpoint=args.hf_endpoint,
        options=options,
        report=report,
        log=log,
    )

    state: dict[str, Any] = {"engine": None}

    def engine():
        if state["engine"] is None:
            instance = create(args.engine, context)
            instance.load()
            state["engine"] = instance
        return state["engine"]

    log("info", f"worker 启动：model={args.model} engine={args.engine} python={sys.version.split()[0]}")

    for request in read_requests(sys.stdin.buffer):
        request_id = request.get("id")
        cmd = str(request.get("cmd") or "")
        writer = ProtocolWriter(TRANSPORT, request_id)
        CURRENT["writer"] = writer

        if cmd == "__bad__":
            writer.error(f"请求不是合法 JSON：{request.get('error')}")
            continue

        started = time.time()
        try:
            if cmd == "shutdown":
                writer.result({"stopped": True})
                log("info", "worker 收到 shutdown，退出")
                return 0

            if cmd == "ping":
                writer.result({"pong": True, "version": __version__})
                continue

            if cmd == "info":
                writer.result(
                    {
                        "version": __version__,
                        "engine": args.engine,
                        "model": args.model,
                        "python": sys.version.split()[0],
                        "executable": sys.executable,
                        "loaded": state["engine"] is not None,
                        "available_engines": _available_engines(),
                    }
                )
                continue

            if cmd == "load":
                reporter = reporter_for(writer)
                context.report = reporter
                engine()
                writer.result({"loaded": True, "engine": args.engine, "duration_ms": _elapsed_ms(started)})
                continue

            if cmd == "prepare":
                reporter = reporter_for(writer)
                context.report = reporter
                instance = engine()
                context.report = reporter
                info = instance.prepare() or {}
                writer.result(
                    {
                        "ready": True,
                        "engine": args.engine,
                        "path": info.get("path", ""),
                        "bytes": info.get("bytes", 0),
                        "files": info.get("files", 0),
                        "duration_ms": _elapsed_ms(started),
                    }
                )
                continue

            if cmd == "recognize":
                reporter = reporter_for(writer)
                context.report = reporter
                instance = engine()
                context.report = reporter
                payload = recognize(instance, request, args.engine)
                payload["duration_ms"] = _elapsed_ms(started)
                writer.result(payload)
                continue

            writer.error(f"未知命令：{cmd}")
        except EngineError as exc:
            writer.error(str(exc), kind="engine")
            log("warn", f"引擎报错：{exc}")
        except KeyboardInterrupt:
            return 130
        except Exception as exc:  # noqa: BLE001 - 一条请求不能带崩 worker
            writer.error(f"{type(exc).__name__}: {exc}")
            log("error", "".join(traceback.format_exception_only(type(exc), exc)).strip())
            log("error", traceback.format_exc(limit=6))
        finally:
            CURRENT["writer"] = None

    return 0


def reporter_for(writer: ProtocolWriter):
    def reporter(stage: str, percent: float | None, message: str = "") -> None:
        writer.progress(stage, percent, message)

    return reporter


def recognize(instance, request: dict[str, Any], engine_name: str) -> dict[str, Any]:
    options = instance.ctx.merged(request.get("options") if isinstance(request.get("options"), dict) else None)

    files = request.get("files")
    if not files:
        single = request.get("file")
        files = [single] if single else []
    files = [str(item) for item in files if item]
    if not files:
        raise EngineError("没有要识别的文件")

    max_pages = int(request.get("max_pages") or options.get("max_pages") or 0)
    max_side = int(options.get("max_side") or 0)
    need_pixels = engine_name != "mock"

    bitmaps = []
    for path in files:
        try:
            if path.lower().endswith(".pdf"):
                from . import pdf

                pages = pdf.render(path, max_pages=max_pages, dpi=int(options.get("pdf_dpi") or 200))
                for page in pages:
                    if need_pixels:
                        bitmaps.append(page)
                    else:
                        # mock 只需要尺寸：丢掉 PIL 图像，省内存
                        bitmaps.append(Bitmap(index=page.index, path=page.path, width=page.width, height=page.height))
            else:
                bitmaps.append(load_bitmap(path, index=len(bitmaps), max_side=max_side, need_pixels=need_pixels))
        except EngineError:
            raise
        except Exception as exc:  # noqa: BLE001
            raise EngineError(f"读取 {path} 失败：{exc}") from exc

    if not bitmaps:
        raise EngineError("文件里没有可识别的页面")

    pages = instance.recognize(bitmaps, options)
    # 引擎只管画页，不知道页从哪个文件来；这里按顺序补上来源文件，
    # 前端才能按文件分 tab（一个 PDF 的多页共用一个 source）。
    for page, bitmap in zip(pages, bitmaps):
        path = getattr(bitmap, "path", "") or ""
        if path:
            page.source = os.path.basename(path)
    return {
        "engine": instance.name,
        "pages": [page.as_dict() for page in pages],
        "meta": {
            "model": instance.ctx.model_id,
            "languages": instance.ctx.options.get("languages") or [],
            "options": {key: value for key, value in options.items() if isinstance(value, (str, int, float, bool))},
            "pages": len(pages),
        },
    }


def _available_engines() -> list[str]:
    try:
        from .engines import available

        return available()
    except Exception:  # noqa: BLE001
        return []


def _elapsed_ms(started: float) -> int:
    return int((time.time() - started) * 1000)


if __name__ == "__main__":
    raise SystemExit(main())
