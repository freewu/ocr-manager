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

import utf8_console  # noqa: F401  统一 UTF-8 输出，Windows 控制台打 ✓ 不会崩

import os
import platform
import re
import shutil
import subprocess
import sys
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DESKTOP = ROOT / "desktop"
WEB = ROOT / "web"
BIN = ROOT / "bin"
DIST = ROOT / "dist"
GO = os.environ.get("OCR_GO", "go")
NSGO = "github.com/oneo-me/nsgo/cmd/nsgo"

# 放进发布包里的说明书：用户拿到的是一个 exe，得告诉他接下来点哪里。
RELEASE_README = """OCR Manager
====================

双击 ocr-manager 就能用：不用装 Go / Node / just，也不用把仓库带在身上。
后端已经编进这个 exe，第一次启动会自己解开。

首次启动要做两件事（界面上都有提示）：

  1. 设置 → Python 环境 → 「安装依赖」
     在应用自己的数据目录里建 venv（不污染系统 Python）。
     机器上需要先有 Python 3.10+：https://www.python.org/downloads/
  2. 模型 → 挑一个模型 → 「下载」
     权重也落在同一个数据目录里，下载完就能在「识别」页用。

MinerU / Surya 这类引擎不是 pip 包，模型卡上点「安装命令」即可，
依赖同样装在应用自己的环境里。

数据（日志 / 模型 / 结果 / venv）都在应用的数据目录：
Windows 默认 %AppData%\\ocr-manager，macOS 是
~/Library/Application Support/ocr-manager，Linux 是 ~/.local/share/ocr-manager。
完整路径在「设置」页能看到；想清空重来，就删掉这个目录。

想把它当纯后端用（浏览器打开 http://127.0.0.1:8899）：
设环境变量 OCR_NO_SPAWN=1 自己拉 ocr-server，桌面壳会连现成的那台。
"""


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


def host_goos() -> str:
    """Go 的 GOOS（sys.platform 里 darwin 就是 darwin，win32 才是 windows）。"""
    return {"win32": "windows", "darwin": "darwin"}.get(sys.platform, "linux")


def host_goarch() -> str:
    return "arm64" if platform.machine().lower() in ("arm64", "aarch64") else "amd64"


def version() -> str:
    """版本号以 assets.go 的 `var Version` 为准（后端的 /api/health 报的就是它）。"""
    assets = ROOT / "assets.go"
    try:
        found = re.search(r'var Version = "([^"]+)"', assets.read_text(encoding="utf-8"))
    except OSError:
        found = None
    return found.group(1) if found else "0.0.0"


def embed_server(exe_suffix: str) -> int:
    """把 bin/ocr-server 塞进 desktop/embedded/，让发布物只有一个 exe。

    内置的那份只在「旁边没有 ocr-server」时才用（见 desktop/embedded.go），
    所以开发时留不留着都行；打包前必须刷新，否则发布包里带的是旧后端。
    """
    src = server_exe()
    if not src.exists():
        print(f"✗ 找不到 {src}，先编后端（just build-go）", file=sys.stderr)
        return 1
    dest = DESKTOP / "embedded"
    dest.mkdir(parents=True, exist_ok=True)
    # 换平台打包时把上次那份删掉，别让旧 exe 留在目录里被编进去
    for old in dest.glob("ocr-server*"):
        old.unlink()
    shutil.copy2(src, dest / ("ocr-server" + exe_suffix))
    # 平台标记：桌面壳只在 GOOS/GOARCH 对得上时才用内置后端
    (dest / "target.txt").write_text(f"{host_goos()}/{host_goarch()}\n", encoding="utf-8")
    return 0


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
    if embed_server(exe_suffix) != 0:
        return 1
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

    # 分发成品：dist/ocr-manager-<版本>-<平台>/ 里只有跑起来需要的东西。
    # 后端已经编进 exe（desktop/embedded.go），所以正常情况下就一个 exe。
    release = DIST / f"ocr-manager-{version()}-{target}"
    if release.exists():
        shutil.rmtree(release)
    release.mkdir(parents=True)
    shutil.copy2(binary, release / f"ocr-manager{exe_suffix}")
    for extra in src.iterdir():
        if extra.name == binary.name:
            continue
        dest = release / extra.name
        if extra.is_dir():
            shutil.copytree(extra, dest, dirs_exist_ok=True)
        else:
            shutil.copy2(extra, dest)
    readme = release / "使用说明.txt"
    readme.write_text(RELEASE_README, encoding="utf-8")

    archive = DIST / (release.name + ".zip")
    if archive.exists():
        archive.unlink()
    with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as zf:
        for item in sorted(release.rglob("*")):
            if item.is_file():
                zf.write(item, item.relative_to(release.parent))

    size_mb = target_exe.stat().st_size / 1048576
    print(f"✓ 已打包 {target_exe}（{size_mb:.1f} MB）", flush=True)
    print(f"  独立可执行程序：{release / ('ocr-manager' + exe_suffix)}", flush=True)
    print(f"  分发压缩包：{archive}（{archive.stat().st_size / 1048576:.1f} MB）", flush=True)
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
