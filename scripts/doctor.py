#!/usr/bin/env python3
"""环境自检：Go / Node / Python / 目录 / 端口，一条命令看清「缺什么」。

    python scripts/doctor.py
"""

from __future__ import annotations

import json
import os
import shutil
import socket
import subprocess
import sys
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PORT = int(os.environ.get("OCR_PORT", "8899"))


def head(title: str) -> None:
    print(f"\n== {title} ==")


def ok(label: str, detail: str) -> None:
    print(f"  ✓ {label:<14} {detail}")


def warn(label: str, detail: str) -> None:
    print(f"  ! {label:<14} {detail}")


def bad(label: str, detail: str) -> None:
    print(f"  ✗ {label:<14} {detail}")


def version(cmd: list[str]) -> str:
    try:
        out = subprocess.run(cmd, capture_output=True, text=True, timeout=20)
        return (out.stdout or out.stderr).strip().splitlines()[0]
    except Exception:
        return ""


def main() -> int:
    problems = 0

    head("工具链")
    go = shutil.which(os.environ.get("OCR_GO", "go"))
    if go:
        ok("go", version([go, "version"]))
    else:
        bad("go", "没找到，安装 Go 1.25+ 并加入 PATH")
        problems += 1

    node = shutil.which("node")
    npm = shutil.which("npm")
    if node:
        ok("node", version([node, "--version"]))
    else:
        bad("node", "没找到，安装 Node 20+ 并加入 PATH")
        problems += 1
    if npm:
        ok("npm", version([npm, "--version"]))
    else:
        bad("npm", "没找到（前端构建需要）")
        problems += 1

    py = sys.executable
    ok("python", f"{version([py, '--version'])}  ({py})")
    try:
        import PIL  # noqa: F401

        ok("pillow", "已安装（图标生成可用）")
    except ImportError:
        warn("pillow", "没装，`just icon` 会用不了（python3 -m pip install pillow）")

    head("工程结构")
    for label, path in [
        ("后端", ROOT / "cmd" / "ocr-server"),
        ("前端", ROOT / "web" / "package.json"),
        ("桌面壳", ROOT / "desktop" / "nsgo.yaml"),
        ("python", ROOT / "python" / "pyproject.toml"),
        ("模型清单", ROOT / "config" / "models.toml"),
    ]:
        if path.exists():
            ok(label, str(path.relative_to(ROOT)))
        else:
            bad(label, f"缺失：{path}")
            problems += 1

    if (ROOT / "web" / "dist" / "index.html").exists():
        ok("web/dist", "已构建")
    else:
        warn("web/dist", "还没构建：跑 `just web-build`（或直接 `just dev`）")
    if not (ROOT / "web" / "node_modules").exists():
        warn("node_modules", "还没装依赖：跑 `just setup`")

    head("后端状态")
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/api/health", timeout=2) as res:
            health = json.loads(res.read().decode("utf-8"))
        if health.get("service") != "ocr-server":
            raise SystemExit(
                f"端口 {PORT} 上跑的是别的服务（{health.get('service') or '未知'}），"
                "换成 `OCR_PORT=8687 just dev` 之类的空闲端口"
            )
        ok("服务", f"http://127.0.0.1:{PORT} 正在运行")
        with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/api/info", timeout=10) as res:
            info = json.loads(res.read().decode("utf-8"))
        for key, value in info["dirs"].items():
            ok(key, value)
        env = info["env"]
        (ok if env["state"] == "ready" else warn)("venv", f"{env['state']} {env['message']}")
        gpu = info["gpu"]
        (ok if gpu["available"] else warn)(
            "GPU", "可用" if gpu["available"] else (gpu.get("error") or "未检测到（VLM 模型跑不了）")
        )
    except Exception:
        warn("服务", f"没在跑（`just dev` 或 `just server` 启动；桌面壳会自己拉）")

    sock = socket.socket()
    sock.settimeout(1)
    busy = sock.connect_ex(("127.0.0.1", PORT)) == 0
    sock.close()
    if busy:
        ok("端口", f"{PORT} 已被监听")
    else:
        warn("端口", f"{PORT} 空闲")

    print()
    if problems:
        print(f"✗ 有 {problems} 个问题需要处理")
        return 1
    print("✓ 环境看起来没问题")
    return 0


if __name__ == "__main__":
    sys.exit(main())
