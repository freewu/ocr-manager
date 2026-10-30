"""PDF → 位图。

优先 pypdfium2（pip 就能装、无外部依赖），退到 PyMuPDF；两个都没有就明确报错，
免得用户对着「识别 0 页」猜。
"""

from __future__ import annotations

from .images import Bitmap, EngineError


def render(path: str, max_pages: int = 0, dpi: int = 200) -> list[Bitmap]:
    pages = _render_pdfium(path, max_pages, dpi)
    if pages is not None:
        return pages
    pages = _render_fitz(path, max_pages, dpi)
    if pages is not None:
        return pages
    raise EngineError(
        "识别 PDF 需要 pypdfium2 或 PyMuPDF：在「设置 → Python 环境」安装依赖（或手动 pip install pypdfium2）"
    )


def page_count(path: str) -> int:
    try:
        import pypdfium2 as pdfium

        document = pdfium.PdfDocument(path)
        try:
            return len(document)
        finally:
            document.close()
    except Exception:  # noqa: BLE001
        pass
    try:
        import fitz

        with fitz.open(path) as document:
            return document.page_count
    except Exception:  # noqa: BLE001
        return 0


def _render_pdfium(path: str, max_pages: int, dpi: int) -> list[Bitmap] | None:
    try:
        import pypdfium2 as pdfium
    except ImportError:
        return None

    scale = dpi / 72.0
    out: list[Bitmap] = []
    document = pdfium.PdfDocument(path)
    try:
        total = len(document)
        limit = total if max_pages <= 0 else min(total, max_pages)
        for index in range(limit):
            page = document[index]
            bitmap = page.render(scale=scale)
            image = bitmap.to_pil().convert("RGB")
            out.append(
                Bitmap(index=index, path=f"{path}#page={index + 1}", width=image.width, height=image.height, image=image)
            )
            bitmap.close()
    finally:
        document.close()
    return out


def _render_fitz(path: str, max_pages: int, dpi: int) -> list[Bitmap] | None:
    try:
        import fitz
    except ImportError:
        return None

    out: list[Bitmap] = []
    from PIL import Image
    import io

    with fitz.open(path) as document:
        limit = document.page_count if max_pages <= 0 else min(document.page_count, max_pages)
        for index in range(limit):
            page = document.load_page(index)
            pixmap = page.get_pixmap(dpi=dpi)
            image = Image.open(io.BytesIO(pixmap.tobytes("png"))).convert("RGB")
            out.append(
                Bitmap(index=index, path=f"{path}#page={index + 1}", width=image.width, height=image.height, image=image)
            )
    return out
