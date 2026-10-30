#!/usr/bin/env python3
"""构建后端 + 前端（产物统一落在 bin/ 与 web/dist/）。

    python scripts/build.py            # 前端 + 后端
    python scripts/build.py --skip-web # 只编 Go
    python scripts/build.py fmt|lint|test

WSL 里默认用 Linux 版 go（前端开发够用）；要出 Windows 原生程序时设置
OCR_GO 指向 Windows 的 go.exe，或直接在 Windows 侧跑同一条命令。
"""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BIN = ROOT / "bin"
WEB = ROOT / "web"
GO = os.environ.get("OCR_GO", "go")


def run(cmd: list[str], cwd: Path = ROOT) -> int:
    print("$", " ".join(cmd), flush=True)
    return subprocess.run(cmd, cwd=cwd).returncode


def which(name: str) -> str:
    path = shutil.which(name)
    if not path:
        raise SystemExit(f"✗ 找不到 {name}，请先安装并确保它在 PATH 里。")
    return path


def build_web() -> int:
    if not (WEB / "node_modules").exists():
        print("[build] 首次构建，安装前端依赖…", flush=True)
        if run([which("npm"), "install", "--no-fund", "--no-audit"], WEB) != 0:
            return 1
    return run([which("npm"), "run", "build"], WEB)


def build_go() -> int:
    BIN.mkdir(exist_ok=True)
    suffix = ".exe" if os.name == "nt" else ""
    return run([GO, "build", "-o", str(BIN / f"ocr-server{suffix}"), "./cmd/ocr-server"])


def main(argv: list[str]) -> int:
    task = argv[0] if argv else "build"
    skip_web = "--skip-web" in argv

    if task == "fmt":
        return run([GO, "fmt", "./..."])
    if task == "lint":
        if run([GO, "vet", "./..."]) != 0:
            return 1
        return run([GO, "vet", "./..."], ROOT / "desktop")
    if task == "test":
        if run([GO, "test", "./..."]) != 0:
            return 1
        # 桌面壳是独立 module，要单独跑（nsgotest 无头渲染真实布局）
        return run([GO, "test", "./..."], ROOT / "desktop")
    if task == "desktop":
        return run([sys.executable, str(ROOT / "scripts" / "desktop.py"), "build"])

    if not skip_web and build_web() != 0:
        print("✗ 前端构建失败", file=sys.stderr)
        return 1
    if build_go() != 0:
        print("✗ 后端编译失败", file=sys.stderr)
        return 1
    print(f"✓ 构建完成：{BIN}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
