#!/usr/bin/env python3
"""一条命令跑起后端 + Vite 前端（Ctrl-C 一起退出）。

    python scripts/dev.py                 # 后端 8899 + 前端 5273
    python scripts/dev.py --skip-web      # 只跑后端
    python scripts/dev.py --no-open       # 不自动开浏览器
"""

from __future__ import annotations

import argparse
import os
import shutil
import signal
import socket
import subprocess
import sys
import time
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
WEB = ROOT / "web"
WEB_PORT = 5273  # 与 web/vite.config.ts 保持一致
BIN = ROOT / "bin"


def which(name: str) -> str:
    path = shutil.which(name)
    if not path:
        raise SystemExit(f"✗ 找不到 {name}，请先安装并确保它在 PATH 里（Go 1.25+ / Node 20+）。")
    return path


def healthy(url: str) -> bool:
    try:
        with urllib.request.urlopen(url, timeout=1.5) as res:
            return res.status == 200
    except Exception:
        return False


def pending(port: int) -> bool:
    """端口上有人在听（不管它是不是我们的服务）。"""
    sock = socket.socket()
    sock.settimeout(1)
    try:
        return sock.connect_ex(("127.0.0.1", port)) == 0
    finally:
        sock.close()


def wait_health(url: str, proc: subprocess.Popen, timeout: float = 120) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if proc.poll() is not None:
            raise SystemExit("✗ 后端进程已退出，看上面的编译/启动日志")
        if healthy(url):
            return True
        time.sleep(0.4)
    return False


def open_browser(url: str) -> None:
    try:
        if os.name == "nt":
            os.startfile(url)  # type: ignore[attr-defined]
            return
        if shutil.which("cmd.exe"):  # WSL：开 Windows 侧浏览器
            subprocess.Popen(["cmd.exe", "/c", "start", "", url],
                             stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                             stderr=subprocess.DEVNULL)
            return
        opener = shutil.which("xdg-open") or shutil.which("open")
        if opener:
            subprocess.Popen([opener, url], stdin=subprocess.DEVNULL,
                             stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    except Exception as exc:  # 打不开浏览器不影响服务
        print(f"[dev] 打开浏览器失败（手动访问 {url}）：{exc}", file=sys.stderr)


def terminate(proc: subprocess.Popen) -> None:
    if proc.poll() is not None:
        return
    if os.name == "nt":
        subprocess.run(["taskkill", "/F", "/T", "/PID", str(proc.pid)], capture_output=True)
        return
    proc.terminate()
    try:
        proc.wait(timeout=8)
    except subprocess.TimeoutExpired:
        proc.kill()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="OCR Manager 开发模式")
    parser.add_argument("--port", type=int, default=int(os.environ.get("OCR_PORT", "8899")))
    parser.add_argument("--skip-web", action="store_true", help="只跑后端")
    parser.add_argument("--no-open", dest="open", action="store_false",
                        default=os.environ.get("OCR_NO_OPEN", "") == "",
                        help="不自动打开浏览器")
    args = parser.parse_args(argv)

    def _term(_signum: int, _frame: object) -> None:
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, _term)

    npm = which("npm")
    if not (WEB / "node_modules").exists():
        print("[dev] 首次运行，安装前端依赖（npm install）…", flush=True)
        if subprocess.run([npm, "install", "--no-fund", "--no-audit"], cwd=WEB).returncode != 0:
            return 1

    if pending(args.port):
        print(f"[dev] 端口 {args.port} 已被占用：先关掉占用的进程（netstat -ano | findstr {args.port} 或 lsof -i:{args.port}）", file=sys.stderr)
        return 1

    BIN.mkdir(exist_ok=True)
    suffix = ".exe" if os.name == "nt" else ""
    server_exe = BIN / f"ocr-server{suffix}"
    print("[dev] 编译后端…", flush=True)
    if subprocess.run([which("go"), "build", "-o", str(server_exe), "./cmd/ocr-server"], cwd=ROOT).returncode != 0:
        print("✗ 后端编译失败", file=sys.stderr)
        return 1

    api = f"http://127.0.0.1:{args.port}"
    env = os.environ.copy()
    env["OCR_PORT"] = str(args.port)
    print(f"[dev] 启动后端 {api}", flush=True)
    procs: list[subprocess.Popen] = [subprocess.Popen([str(server_exe), "-addr", f"127.0.0.1:{args.port}"], cwd=ROOT, env=env)]

    try:
        if not wait_health(f"{api}/api/health", procs[0]):
            print(f"✗ 后端健康检查超时（{api}/api/health）", file=sys.stderr)
            return 1
        print(f"[dev] 后端就绪 {api}", flush=True)
        if args.skip_web:
            procs[0].wait()
            return procs[0].returncode or 0

        if pending(WEB_PORT):
            print(f"[dev] 端口 {WEB_PORT} 已被占用（可能是上次的 vite）", file=sys.stderr)
            return 1

        web_env = env.copy()
        web_env["OCR_API"] = api
        print(f"[dev] 启动前端 http://127.0.0.1:{WEB_PORT}（Vite HMR）", flush=True)
        web = subprocess.Popen([npm, "run", "dev"], cwd=WEB, env=web_env)
        procs.append(web)

        if args.open:
            deadline = time.time() + 30
            while time.time() < deadline and not pending(WEB_PORT):
                time.sleep(0.3)
            if pending(WEB_PORT):
                open_browser(f"http://127.0.0.1:{WEB_PORT}/")

        while True:
            if procs[0].poll() is not None and not healthy(f"{api}/api/health"):
                print("[dev] 后端已退出", file=sys.stderr)
                return procs[0].returncode or 0
            if web.poll() is not None and not pending(WEB_PORT):
                print("[dev] 前端已退出", file=sys.stderr)
                return web.returncode or 0
            time.sleep(0.5)
    except KeyboardInterrupt:
        print("\n[dev] 收到 Ctrl-C，正在退出…", flush=True)
        return 0
    finally:
        for proc in reversed(procs):
            terminate(proc)


if __name__ == "__main__":
    sys.exit(main())
