#!/usr/bin/env python3
"""NSGo 桌面壳的跑 / 编 / 检查。

    python scripts/desktop.py run     # 编译运行（带图标、改代码自动重建）
    python scripts/desktop.py build   # 打包到 bin/（exe 里带应用图标）
    python scripts/desktop.py fmt|lint

桌面壳是独立 Go module（desktop/），依赖 NSGo；它自己会去找（或在需要时拉起）
ocr-server，所以只要 bin/ocr-server 存在就能跑。

用 NSGo 自带的 `nsgo dev` / `nsgo build`，而不是裸 `go build`：Windows 的窗口 /
任务栏 / 托盘图标是链接进 exe 的 Win32 资源（`assets/Icon.png` 生成 .syso），
裸 go build 出来的 exe 没有这段资源，系统只能显示默认图标。
"""

from __future__ import annotations

import os
import platform
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DESKTOP = ROOT / "desktop"
WEB = ROOT / "web"
BIN = ROOT / "bin"
DIST = ROOT / "dist"
GO = os.environ.get("OCR_GO", "go")
NSGO = "github.com/oneo-me/nsgo/cmd/nsgo"


def which(name: str) -> str:
    path = shutil.which(name)
    if not path:
        raise SystemExit(f"✗ 找不到 {name}，请先安装并确保它在 PATH 里。")
    return path


def run(cmd: list[str], cwd: Path) -> int:
    print("$", " ".join(cmd), flush=True)
    return subprocess.run(cmd, cwd=cwd).returncode


def newest_mtime(paths: list[Path]) -> float:
    """这些文件 / 目录里最新的修改时间（不存在的当作 0）。"""
    newest = 0.0
    for path in paths:
        if not path.exists():
            continue
        if path.is_file():
            newest = max(newest, path.stat().st_mtime)
            continue
        for child in path.rglob("*"):
            # 测试跑出来的 __pycache__ / .pyc 不算源码改动
            if "__pycache__" in child.parts or child.suffix == ".pyc":
                continue
            if child.is_file():
                newest = max(newest, child.stat().st_mtime)
    return newest


def out_of_date(target: Path, sources: list[Path]) -> bool:
    """target 不存在、或比 sources 旧 → 需要重建。

    「编译产物存在就跳过」是踩过的坑：改了后端代码再 `just desktop`，窗口里
    还是旧的 ocr-server，但界面是新的——症状看起来就是「修好的功能又坏了」
    或者「报错说的按钮跟界面对不上」。所以这里按 mtime 判，不按存在判。
    """
    if not target.exists():
        return True
    return newest_mtime(sources) > target.stat().st_mtime


def ensure_web() -> int:
    """桌面壳托管的是 web/dist：没有、或前端源码比它新就先构建一份。"""
    sources = [
        WEB / "src", WEB / "public",
        WEB / "index.html", WEB / "package.json", WEB / "vite.config.ts",
        WEB / "tsconfig.json", WEB / "tsconfig.node.json",
    ]
    if not out_of_date(WEB / "dist" / "index.html", sources):
        return 0
    npm = which("npm")
    if not (WEB / "node_modules").exists():
        if run([npm, "install", "--no-fund", "--no-audit"], WEB) != 0:
            return 1
    return run([npm, "run", "build"], WEB)


def server_exe() -> Path:
    return BIN / ("ocr-server.exe" if os.name == "nt" else "ocr-server")


def ensure_server() -> int:
    """bin/ocr-server 比后端源码旧就重编（go build 有缓存，很快）。"""
    sources = [
        ROOT / "cmd", ROOT / "internal", ROOT / "python", ROOT / "config",
        ROOT / "assets.go", ROOT / "catalog.go", ROOT / "go.mod", ROOT / "go.sum",
    ]
    target = server_exe()
    if not out_of_date(target, sources):
        return 0
    BIN.mkdir(exist_ok=True)
    return run([GO, "build", "-o", str(target), "./cmd/ocr-server"], ROOT)


def host_target() -> str:
    """当前平台对应的 nsgo 目标名，例如 windows_x64 / linux_x64 / macos_arm64。"""
    system = {"win32": "windows", "darwin": "macos"}.get(sys.platform, "linux")
    machine = platform.machine().lower()
    arch = "arm64" if machine in ("arm64", "aarch64") else "x64"
    return f"{system}_{arch}"


def project_name() -> str:
    """nsgo.yaml 里的 name，打包目录就按它命名。"""
    for line in (DESKTOP / "nsgo.yaml").read_text(encoding="utf-8").splitlines():
        if line.startswith("name:"):
            return line.split(":", 1)[1].strip().strip("'\"")
    return "app"


def package(exe_suffix: str) -> int:
    target = host_target()
    name = project_name()
    if run([GO, "run", NSGO, "build", "-target", target, "-output", str(DIST), "."], DESKTOP) != 0:
        return 1

    produced = sorted(DIST.glob(f"{name}-*-{target}"))
    if not produced:
        print(f"✗ 没找到 {target} 的打包目录（{DIST}/{name}-*-{target}）", file=sys.stderr)
        return 1
    src = produced[-1]
    BIN.mkdir(exist_ok=True)

    # 桌面壳和 ocr-server 放同一目录：桌面壳先找 exe 同级，用户拿到就是一个文件夹。
    binary = src / f"{name}{exe_suffix}"
    if not binary.exists():
        print(f"✗ 打包产物里没有 {binary.name}", file=sys.stderr)
        return 1
    target_exe = BIN / f"ocr-manager{exe_suffix}"
    shutil.copy2(binary, target_exe)
    for extra in src.iterdir():
        if extra.name != binary.name:
            dest = BIN / extra.name
            if extra.is_dir():
                shutil.copytree(extra, dest, dirs_exist_ok=True)
            else:
                shutil.copy2(extra, dest)

    # 后端也放进打包目录：dist/ 里那个文件夹本身就是一份可直接分发的成品。
    shutil.copy2(server_exe(), src / server_exe().name)

    size_mb = target_exe.stat().st_size / 1048576
    print(f"✓ 已打包 {target_exe}（{size_mb:.1f} MB）", flush=True)
    print(f"  分发目录：{src}（含 {server_exe().name}）", flush=True)
    return 0


def main(argv: list[str]) -> int:
    task = argv[0] if argv else "run"
    if task == "fmt":
        return run([GO, "fmt", "./..."], DESKTOP)
    if task == "lint":
        return run([GO, "vet", "./..."], DESKTOP)

    if ensure_web() != 0:
        print("✗ 前端构建失败", file=sys.stderr)
        return 1
    if ensure_server() != 0:
        print("✗ 后端编译失败", file=sys.stderr)
        return 1

    if task == "run":
        # nsgo dev：把图标链进临时 exe 再跑，改 .go 文件会重建重启
        return run([GO, "run", NSGO, "dev", "."], DESKTOP)

    if task == "build":
        suffix = ".exe" if os.name == "nt" else ""
        return package(suffix)

    print(f"✗ 未知任务 {task}（可用：run / build / fmt / lint）", file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
