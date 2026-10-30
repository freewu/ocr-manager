#!/usr/bin/env python3
"""命令行入口：所有模型 / 环境 / 识别 / 历史命令都走这里。

CLI 只跟正在运行的 ocr-server 说话（HTTP + JSON），不直接碰 python 协议，
这样界面和命令行看到的状态永远一致。服务没起来时会提示先跑 `just dev`。
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request

BASE = os.environ.get("OCR_API", f"http://127.0.0.1:{os.environ.get('OCR_PORT', '8899')}")


def call(method: str, path: str, body: object | None = None) -> object:
    data = None if body is None else json.dumps(body).encode("utf-8")
    req = urllib.request.Request(
        BASE + path,
        data=data,
        method=method,
        headers={"Content-Type": "application/json"} if data else {},
    )
    try:
        with urllib.request.urlopen(req, timeout=600) as res:
            raw = res.read().decode("utf-8")
    except urllib.error.HTTPError as exc:  # 服务端把失败原因放在 error 字段
        raw = exc.read().decode("utf-8", "replace")
        try:
            message = json.loads(raw).get("error") or raw
        except json.JSONDecodeError:
            message = raw
        raise SystemExit(f"✗ {message}") from None
    except urllib.error.URLError as exc:
        raise SystemExit(
            f"✗ 连不上后端 {BASE}（{exc.reason}）\n  先跑 `just dev` 或 `just server`，或用 OCR_API 指定地址"
        ) from None
    return json.loads(raw) if raw.strip() else {}


def human(n: float | int) -> str:
    for unit in ("B", "KiB", "MiB", "GiB"):
        if abs(n) < 1024:
            return f"{n:.0f} {unit}" if unit == "B" else f"{n:.1f} {unit}"
        n /= 1024
    return f"{n:.1f} TiB"


def cmd_info(_: argparse.Namespace) -> None:
    info = call("GET", "/api/info")
    print(f"版本      {info['version']}")
    print(f"数据目录  {info['root']}")
    for key, value in info["dirs"].items():
        print(f"  {key:<8} {value}")
    env = info["env"]
    print(f"python    {env['state']}  {env['message']}")
    gpu = info["gpu"]
    print(f"GPU       {'可用' if gpu['available'] else '未检测到'} {gpu.get('error') or ''}")
    print(f"模型      {info['models']} 个（可见 {info['visible']}）")


def cmd_list(args: argparse.Namespace) -> None:
    payload = call("GET", "/api/models" + ("?refresh=1" if args.refresh else ""))
    for m in payload["models"]:
        if args.visible_only and m.get("hidden"):
            continue
        st = m["status"]
        weights = st["weights"]
        marks = []
        if st["weights_ready"]:
            marks.append("权重✓")
        elif st["needs_download"]:
            marks.append("需下载")
        if st["loaded"]:
            marks.append("已加载")
        if m["requires_gpu"]:
            marks.append("GPU")
        extra = " ".join(marks)
        print(f"{m['id']:<24} {st['state']:<12} {m['name']:<26} {extra}")
        if weights["files"]:
            print(f"{'':<24} 权重 {weights['files']} 个文件 {human(weights['bytes'])} @ {weights['path']}")


def cmd_prepare(args: argparse.Namespace) -> None:
    print(f"准备 {args.model}（装依赖 + 下权重，可能要几分钟）…")
    call("POST", f"/api/models/{args.model}/prepare" + ("?force=1" if args.force else ""))
    print("✓ 已就绪")


def cmd_activate(args: argparse.Namespace) -> None:
    call("POST", f"/api/models/{args.model}/activate")
    print(f"✓ {args.model} 已加载")


def cmd_deactivate(args: argparse.Namespace) -> None:
    if args.model:
        call("POST", f"/api/models/{args.model}/deactivate")
        print(f"✓ {args.model} 已卸载")
        return
    for m in call("GET", "/api/models")["models"]:
        if m["status"]["loaded"]:
            call("POST", f"/api/models/{m['id']}/deactivate")
            print(f"✓ {m['id']} 已卸载")


def cmd_env(args: argparse.Namespace) -> None:
    if args.model:
        payload = call("GET", "/api/models")["models"]
        spec = next((m for m in payload if m["id"] == args.model), None)
        if spec is None:
            raise SystemExit(f"✗ 没有这个模型：{args.model}")
        extras = spec["extras"]
    elif args.setup:
        extras = []
    else:
        env = call("GET", "/api/env")["env"]
        print(f"状态    {env['state']}  {env['message']}")
        print(f"venv    {env['venv_python'] or '（未创建）'}")
        base = env["base"] or "（未探测到）"
        print(f"基础    {base}")
        for name, ok in sorted((env.get("extras") or {}).items()):
            print(f"  {name:<10} {'✓' if ok else '✗'}")
        return
    print(f"安装依赖 extras={extras or '全部'}…")
    call("POST", "/api/env/install", {"extras": extras})
    print("✓ 完成")


def cmd_recognize(args: argparse.Namespace) -> None:
    options = {"max_pages": args.pages, "pdf_dpi": args.dpi}
    job = call("POST", "/api/recognize", {"model": args.model, "paths": args.files, "options": options})["job"]
    print(f"任务 {job['id']} 已提交（{job['model_name']}）")
    import time

    while True:
        job = call("GET", f"/api/jobs/{job['id']}")["job"]
        if job["status"] != "running":
            break
        print(f"\r  {job['progress'] * 100:5.1f}% {job['stage']} {job['message']}", end="", flush=True)
        time.sleep(0.5)
    print()
    if job["status"] != "done":
        raise SystemExit(f"✗ {job['status']}：{job.get('error') or job.get('message')}")
    print(f"✓ {job['pages']} 页 / {job['blocks']} 块 / {job['chars']} 字 / {job['duration_ms']} ms")
    detail = call("GET", f"/api/history/{job['id']}")
    page = (detail.get("result") or {}).get("pages") or [{}]
    text = page[0].get("text") or ""
    if text:
        print("-" * 60)
        print(text[:2000])
        if len(text) > 2000:
            print(f"…（还有 {len(text) - 2000} 字，产物在 {args.out or '产物目录'}）")


def cmd_history(args: argparse.Namespace) -> None:
    if args.delete:
        call("DELETE", f"/api/history/{args.delete}")
        print(f"✓ 已删除 {args.delete}")
        return
    if args.show:
        detail = call("GET", f"/api/history/{args.show}")
        entry = detail["entry"]
        print(f"{entry['id']}  {entry['model_name']}  {entry['status']}")
        for page in (detail.get("result") or {}).get("pages") or []:
            print(f"--- 第 {page['index'] + 1} 页 ---")
            print(page.get("text") or page.get("markdown") or "")
        return
    for e in call("GET", f"/api/history?limit={args.limit}")["items"]:
        print(f"{e['id']}  {e['status']:<8} {e['model_name']:<22} {e['source_name']:<28} "
              f"{e['pages']}页 {e['chars']}字 {e['duration_ms']}ms")


def cmd_settings(args: argparse.Namespace) -> None:
    if args.set:
        patch = {}
        for pair in args.set:
            key, _, value = pair.partition("=")
            if not key or value == "":
                raise SystemExit(f"✗ 用法：--set key=value（收到 {pair}）")
            try:
                patch[key] = int(value)
            except ValueError:
                patch[key] = value if value.lower() not in ("true", "false") else value.lower() == "true"
        data = call("POST", "/api/settings", patch)["settings"]
    else:
        data = call("GET", "/api/settings")["settings"]
    print(json.dumps(data, indent=2, ensure_ascii=False))


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="ocr", description="OCR Manager 命令行")
    sub = parser.add_subparsers(dest="cmd", required=True)

    sub.add_parser("info", help="运行时信息").set_defaults(fn=cmd_info)

    p = sub.add_parser("list", help="列出模型")
    p.add_argument("--refresh", action="store_true", help="重新探测仓库")
    p.add_argument("--visible-only", action="store_true", help="不显示隐藏模型")
    p.set_defaults(fn=cmd_list)

    p = sub.add_parser("prepare", help="装依赖 + 下载权重")
    p.add_argument("model")
    p.add_argument("--force", action="store_true", help="已有权重也重下")
    p.set_defaults(fn=cmd_prepare)

    p = sub.add_parser("activate", help="启用模型")
    p.add_argument("model")
    p.set_defaults(fn=cmd_activate)

    p = sub.add_parser("deactivate", help="卸载模型（不填则全部）")
    p.add_argument("model", nargs="?", default="")
    p.set_defaults(fn=cmd_deactivate)

    p = sub.add_parser("env", help="查看 / 安装 python 依赖")
    p.add_argument("--setup", action="store_true", help="安装全部依赖")
    p.add_argument("--model", default="", help="只装这个模型的 extras")
    p.set_defaults(fn=cmd_env)

    p = sub.add_parser("recognize", help="识别图片 / PDF")
    p.add_argument("files", nargs="+")
    p.add_argument("--model", default="")
    p.add_argument("--pages", type=int, default=20)
    p.add_argument("--dpi", type=int, default=200)
    p.add_argument("-o", "--out", default="")
    p.set_defaults(fn=cmd_recognize)

    p = sub.add_parser("history", help="历史记录")
    p.add_argument("--limit", type=int, default=20)
    p.add_argument("--show", default="")
    p.add_argument("--delete", default="")
    p.set_defaults(fn=cmd_history)

    p = sub.add_parser("settings", help="查看 / 修改设置")
    p.add_argument("--set", action="append", metavar="key=value")
    p.set_defaults(fn=cmd_settings)

    args = parser.parse_args(argv)
    args.fn(args)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
