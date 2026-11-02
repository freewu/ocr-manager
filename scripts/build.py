#!/usr/bin/env python3
"""构建后端 + 前端 + 桌面壳（产物落在 bin/ 与 dist/）。

    python scripts/build.py              # 前端 + 后端 + 独立可执行程序（含 zip）
    python scripts/build.py --skip-web   # 只编后端（bin/ocr-server）
    python scripts/build.py --skip-desktop # 前端 + 后端，不打发布包
    python scripts/build.py fmt|lint|test

WSL 里默认用 Linux 版 go（前端开发够用）；要出 Windows 原生程序时设置
OCR_GO 指向 Windows 的 go.exe，或直接在 Windows 侧跑同一条命令。
"""

from __future__ import annotations

import utf8_console  # noqa: F401  统一 UTF-8 输出，Windows 控制台打 ✓ 不会崩

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
    # -trimpath：不要把自己的源码路径写进二进制。
    # assets.go 用 runtime.Caller 找「开发态的 python/ 目录」，不裁剪路径的话
    # 打包出来的 exe 会指向构建机上的仓库（在本机跑就一直用仓库源码，拷到别人
    # 机器上才走内置解包）—— 两套行为不一致，很难查。裁掉之后发布包只认内置资源。
    return run([GO, "build", "-trimpath", "-o", str(BIN / f"ocr-server{suffix}"), "./cmd/ocr-server"])


def main(argv: list[str]) -> int:
    task = argv[0] if argv else "build"
    skip_web = "--skip-web" in argv
    # 只编后端时不打包（没前端产物、也没想发布）
    skip_desktop = "--skip-desktop" in argv or skip_web

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
    if skip_desktop:
        print(f"✓ 构建完成：{BIN}")
        return 0

    # 桌面壳把后端编进自己肚子里，发布物就一个 exe；具体步骤在 scripts/desktop.py
    desktop = ROOT / "scripts" / "desktop.py"
    if run([sys.executable, str(desktop), "build"]) != 0:
        print("✗ 桌面壳打包失败", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
