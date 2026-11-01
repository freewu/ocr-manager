"""external：把独立命令行（MinerU / Surya）当引擎跑。

这类工具依赖装在**应用自己的 venv** 里（extras：`surya` / `mineru`），
「安装命令」装完就能启用；识别时我们把每一页写成一个图片文件交给 CLI，
再把它吐出的 JSON / Markdown 摊成 Block。

支持的 CLI 按命令名分派：
- `surya_ocr <img> --output_dir <out>`：结果在 `out/<name>/results.json` 的 `text_lines`
- `mineru -p <input> -o <out>`：结果在 `out/**/<name>.md`
认不出来的命令走通用模式：把 `{file}`（或 `<image>`）换成图片路径，stdout 当文本。
"""

from __future__ import annotations

import glob
import json
import os
import shutil
import subprocess
import sys
from typing import Any

from ..images import Bitmap, EngineError
from .base import Block, Engine, Page, register

# 输出里最多留多少字给报错（stderr 可能几百行）
TAIL = 600


@register
class ExternalEngine(Engine):
    name = "external"

    def __init__(self, ctx) -> None:
        super().__init__(ctx)
        self._path = ""
        self._cli = ""

    # ---------------------------------------------------------------- 生命周期

    def load(self) -> None:
        name = self._command_name()
        if not name:
            raise EngineError(
                f"「{self.ctx.model_id}」的清单里没写 options.command，不知道要调哪个命令行"
            )
        path = find_command(name)
        if not path:
            raise EngineError(
                f"「{self.ctx.model_id}」的命令行 {name} 还没装："
                f"在模型卡上点「安装命令」（会装进应用自己的 python 环境），装好后点卡片上的「重新检测」"
            )
        self._path = path
        self._cli = os.path.basename(path).lower()
        # CLI 需要 PIL / pypdfium2 干活；extras 里带了，缺了就在启用时就报清楚
        try:
            import PIL  # noqa: F401
        except ImportError as exc:  # pragma: no cover - 依赖缺失时才走到
            raise EngineError(
                f"{name} 的 python 依赖不完整（缺 Pillow）：在模型卡上点「重新安装命令」"
            ) from exc
        self.loaded = True
        self.report("load", 100.0, f"{name} 就绪（{path}）")

    # ---------------------------------------------------------------- 识别

    def recognize(self, pages: list[Bitmap], options: dict[str, Any]) -> list[Page]:
        if not self.loaded:
            self.load()

        total = max(1, len(pages))
        results: list[Page] = []
        for position, bitmap in enumerate(pages):
            self.report("ocr", position / total * 100.0, f"识别第 {position + 1}/{total} 页")
            blocks, markdown = self._run_page(bitmap, options)
            page = Page(
                index=bitmap.index,
                width=bitmap.width,
                height=bitmap.height,
                blocks=blocks,
                markdown=markdown,
            )
            results.append(page.finish())
            self.report("ocr", (position + 1) / total * 100.0, f"已识别 {position + 1}/{total} 页")
        return results

    def _run_page(self, bitmap: Bitmap, options: dict[str, Any]) -> tuple[list[Block], str | None]:
        import tempfile

        with tempfile.TemporaryDirectory(prefix="ocr-cli-") as workdir:
            source = _materialize(bitmap, workdir)
            out = os.path.join(workdir, "out")
            os.makedirs(out, exist_ok=True)

            if self._cli.startswith("surya"):
                args = [self._path, source, "--output_dir", out]
                self._exec(args, options)
                return _parse_surya(out, source)
            if self._cli.startswith("mineru"):
                args = [self._path, "-p", source, "-o", out]
                self._exec(args, options)
                return _parse_mineru(out, source)
            return self._run_generic(source, out, options)

    # ---------------------------------------------------------------- 执行

    def _exec(self, args: list[str], options: dict[str, Any]) -> str:
        timeout = float(options.get("cli_timeout") or 1800)
        self.log(f"执行：{subprocess.list2cmdline(args)}")
        env = os.environ.copy()
        # 权重也归到应用的权重目录，别散落到用户家目录
        env.setdefault("HF_HOME", self.ctx.models_dir or "")
        if self.ctx.hf_endpoint:
            env.setdefault("HF_ENDPOINT", self.ctx.hf_endpoint)
        try:
            proc = subprocess.run(  # noqa: S603 - 命令来自模型清单，不是用户输入
                args,
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                timeout=timeout,
                env=env,
            )
        except FileNotFoundError as exc:
            raise EngineError(
                f"找不到命令 {args[0]}：在模型卡上点「安装命令」重新装一遍"
            ) from exc
        except subprocess.TimeoutExpired as exc:
            raise EngineError(
                f"{os.path.basename(args[0])} 跑了 {int(timeout)} 秒还没结束，已放弃（大文件可以调大模型选项里的 cli_timeout）"
            ) from exc
        if proc.returncode != 0:
            tail = (proc.stderr or proc.stdout or "").strip()[-TAIL:]
            raise EngineError(
                f"{os.path.basename(args[0])} 退出码 {proc.returncode}：{tail or '（没有输出）'}"
            )
        if proc.stderr and proc.stderr.strip():
            # 很多 CLI 把进度写 stderr，留着方便排查
            self.log(proc.stderr.strip()[-TAIL:], "warn")
        return proc.stdout or ""

    def _run_generic(self, source: str, out: str, options: dict[str, Any]) -> tuple[list[Block], None]:
        template = str(self.options.get("args") or "{file}")
        args = [self._path] + [
            part.replace("{file}", source).replace("<image>", source).replace("<input>", source).replace("{out}", out)
            for part in _split_template(template)
        ]
        text = self._exec(args, options).strip()
        blocks = [Block(text=line.strip()) for line in text.splitlines() if line.strip()]
        return blocks, text or None

    # ---------------------------------------------------------------- 工具

    def _command_name(self) -> str:
        raw = str(self.options.get("command") or "").strip()
        fields = raw.split()
        return fields[0] if fields else ""


def find_command(name: str) -> str:
    """先在 worker 自己的 venv 里找（pip 装的命令行就在这里），再退回 PATH。"""
    scripts = os.path.dirname(os.path.abspath(sys.executable))
    suffixes = ["", ".exe", ".bat", ".cmd"] if os.name == "nt" else [""]
    for suffix in suffixes:
        candidate = os.path.join(scripts, name + suffix)
        if os.path.isfile(candidate):
            return candidate
    return shutil.which(name) or ""


def _split_template(template: str) -> list[str]:
    return [part for part in template.split() if part]


def _materialize(bitmap: Bitmap, workdir: str) -> str:
    """CLI 要一个真实文件路径：原图能直接用就直接用，否则把位图存成 PNG。"""
    path = getattr(bitmap, "path", "") or ""
    if path and os.path.isfile(path):
        return path
    if bitmap.image is None:
        raise EngineError("这一页没有像素数据，命令行工具没法处理")
    target = os.path.join(workdir, f"page-{bitmap.index + 1}.png")
    bitmap.image.save(target)
    return target


# ---------------------------------------------------------------- 输出解析


def _parse_surya(out: str, source: str) -> tuple[list[Block], None]:
    payload = _load_json_results(out, source)
    if payload is None:
        raise EngineError("surya_ocr 没吐出 results.json：看日志里它的输出（多半是权重没下下来）")
    return _surya_blocks(payload), None


def _surya_blocks(payload: Any) -> list[Block]:
    if isinstance(payload, list):
        blocks: list[Block] = []
        for item in payload:
            blocks.extend(_surya_blocks(item))
        return blocks
    if not isinstance(payload, dict):
        return []

    lines = payload.get("text_lines") or payload.get("lines") or []
    blocks = []
    for line in lines:
        if isinstance(line, str):
            blocks.append(Block(text=line))
            continue
        if not isinstance(line, dict):
            continue
        text = str(line.get("text") or "").strip()
        if not text:
            continue
        blocks.append(
            Block(
                text=text,
                box=_box(line.get("polygon") or line.get("bbox") or line.get("box")),
                confidence=_float(line.get("confidence") or line.get("score")),
            )
        )
    return blocks


def _parse_mineru(out: str, source: str) -> tuple[list[Block], str | None]:
    markdown = _newest_markdown(out)
    if markdown is None:
        raise EngineError("mineru 没产出 markdown：看日志里它的输出（多半是模型权重没下完）")
    blocks = [Block(text=line.strip()) for line in markdown.splitlines() if line.strip()]
    return blocks, markdown


def _load_json_results(out: str, source: str) -> Any:
    stem = os.path.splitext(os.path.basename(source))[0]
    candidates = [
        os.path.join(out, stem, "results.json"),
        os.path.join(out, "results.json"),
    ]
    candidates.extend(sorted(glob.glob(os.path.join(out, "**", "results.json"), recursive=True)))
    for candidate in candidates:
        if not os.path.isfile(candidate):
            continue
        try:
            with open(candidate, encoding="utf-8") as fh:
                return json.load(fh)
        except (OSError, json.JSONDecodeError):
            continue
    return None


def _newest_markdown(out: str) -> str | None:
    files = glob.glob(os.path.join(out, "**", "*.md"), recursive=True)
    if not files:
        return None
    files.sort(key=os.path.getmtime, reverse=True)
    for path in files:
        try:
            with open(path, encoding="utf-8") as fh:
                text = fh.read()
        except OSError:
            continue
        if text.strip():
            return text
    return None


def _box(value: Any) -> list[list[float]]:
    if not value:
        return []
    try:
        points = list(value)
        if len(points) == 4 and all(isinstance(item, (int, float)) for item in points):
            # bbox 形式：[x0, y0, x1, y1] → 四个角
            x0, y0, x1, y1 = (float(item) for item in points)
            return [[x0, y0], [x1, y0], [x1, y1], [x0, y1]]
        return [[float(point[0]), float(point[1])] for point in points]
    except (TypeError, ValueError, IndexError):
        return []


def _float(value: Any) -> float | None:
    try:
        return float(value)
    except (TypeError, ValueError):
        return None
