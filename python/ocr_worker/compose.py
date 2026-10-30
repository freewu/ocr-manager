"""把逐块结果拼成纯文本 / Markdown。

引擎只负责「这一块是什么、在哪、多确定」；怎么排成给用户看的文字是这一层的事，
Go 侧只落盘，不再各写一份拼接逻辑。
"""

from __future__ import annotations

from typing import Iterable, Protocol


class SupportsBlock(Protocol):
    text: str
    kind: str


TITLE_KINDS = {"title", "heading"}
HEADING_PREFIX = {
    "title": "## ",
    "heading": "## ",
    "subheading": "### ",
    "list": "- ",
    "formula": "",
}


def text_of(blocks: Iterable[SupportsBlock]) -> str:
    """纯文本：一行一块，块之间空行只在段落类型变化时补。"""
    lines: list[str] = []
    previous = ""
    for block in blocks:
        text = (block.text or "").strip()
        if not text:
            continue
        if lines and block.kind != previous and block.kind in TITLE_KINDS | {"table", "formula"}:
            lines.append("")
        lines.append(text)
        previous = block.kind
    return "\n".join(lines).strip()


def markdown_of(blocks: Iterable[SupportsBlock], image_blocks: bool = True) -> str:
    """Markdown：标题加 `#`，表格与公式原样保留，图片块留占位。"""
    parts: list[str] = []
    for block in blocks:
        text = (block.text or "").strip()
        kind = block.kind or "text"
        if kind == "image":
            if image_blocks and text:
                parts.append(f"![{text}]()")
            continue
        if not text:
            continue
        if kind == "table":
            parts.append(text)
            continue
        if kind == "formula":
            body = text
            if not (body.startswith("$$") or body.startswith("$")):
                body = "$$\n" + body + "\n$$"
            parts.append(body)
            continue
        prefix = HEADING_PREFIX.get(kind, "")
        if prefix and not text.startswith(("#", "-")):
            # 多行块只在第一行加前缀，剩下的缩进对齐
            first, _, rest = text.partition("\n")
            text = prefix + first + (("\n" + rest) if rest else "")
        parts.append(text)
    return "\n\n".join(parts).strip()


def sort_blocks(blocks: list) -> list:
    """按阅读顺序排：先上后下（行容忍），同一行再从左到右。"""
    if not blocks:
        return blocks

    def key(block):
        box = getattr(block, "box", None) or []
        if not box:
            return (0.0, 0.0)
        ys = [point[1] for point in box]
        xs = [point[0] for point in box]
        height = max(1.0, max(ys) - min(ys))
        # 以框中心 y 归行，容忍 60% 行高的抖动
        return (round(min(ys) / (height * 0.6 + 1e-6)), min(xs))

    return sorted(blocks, key=key)
