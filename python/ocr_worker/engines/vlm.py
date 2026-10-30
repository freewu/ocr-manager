"""transformers 跑的文档解析 VLM（dots.ocr / DeepSeek-OCR / 同类）。

这类模型不返回逐行检测框，而是直接吐 Markdown / 阅读顺序文本，所以整页当一块。
权重是标准 HF 仓库：`prepare` 用 snapshot_download 拉到 `<models>/weights/<repo>`，
`load` 优先用本地目录，这样离线也能起来。
"""

from __future__ import annotations

import os
from typing import Any

from ..hub_utils import download_repo, repo_dir_name
from ..images import Bitmap, EngineError
from .base import Block, Engine, Page, register

DEFAULT_PROMPT = "请把这张图完整转成 Markdown，保留标题层级、表格和公式。"


@register
class VlmEngine(Engine):
    name = "vlm"
    requires_gpu = True

    def __init__(self, ctx) -> None:
        super().__init__(ctx)
        self._model = None
        self._processor = None
        self._device = "cpu"
        self._local_dir = ""

    # ---------------------------------------------------------------- 权重

    def weights_dir(self) -> str:
        return os.path.join(self.ctx.models_dir, "weights", repo_dir_name(self.ctx.repo))

    def prepare(self) -> None:
        if not self.ctx.repo:
            raise EngineError("这个 VLM 模型没有配置 repo，无法下载权重")
        target = self.weights_dir()
        if os.path.isdir(target) and os.listdir(target):
            size, files = _dir_stats(target)
            self.report("download", 100.0, f"权重已在本地（{files} 个文件）")
            return {"path": target, "bytes": size, "files": files}
        self.report("download", 0.0, f"开始下载 {self.ctx.repo}")
        result = download_repo(
            self.ctx.repo,
            target,
            endpoint=self.ctx.hf_endpoint,
            models_dir=self.ctx.models_dir,
            report=self.ctx.report,
        )
        self.report("download", 100.0, "权重下载完成")
        return result

    # ---------------------------------------------------------------- 加载

    def load(self) -> None:
        try:
            import torch
            from transformers import AutoProcessor
        except ImportError as exc:
            raise EngineError("缺少 torch / transformers：在「模型」页点这个模型的「下载」，或在「设置 → Python 环境」安装依赖") from exc

        local = self.weights_dir()
        source = local if os.path.isdir(local) and os.listdir(local) else self.ctx.repo
        if not source:
            raise EngineError("这个 VLM 模型既没有本地权重也没有 repo")

        if torch.cuda.is_available():
            self._device = "cuda"
        elif torch.backends.mps.is_available():  # type: ignore[attr-defined]
            self._device = "mps"
        else:
            raise EngineError(
                "VLM 引擎需要 GPU：本机没检测到 CUDA。"
                "CPU 上这些模型一页要几分钟，不如下载 RapidOCR 做 CPU 基线。"
            )

        dtype_name = str(self.options.get("dtype") or "bfloat16")
        dtype = getattr(torch, dtype_name, torch.bfloat16)
        self.report("load", 5.0, f"加载 {source}")
        try:
            from transformers import AutoModelForVision2Seq

            model_cls = AutoModelForVision2Seq
        except ImportError:  # 老版本 transformers 没有这个基类
            from transformers import AutoModel

            model_cls = AutoModel

        try:
            self._processor = AutoProcessor.from_pretrained(source, trust_remote_code=True)
            self._model = model_cls.from_pretrained(
                source,
                trust_remote_code=True,
                torch_dtype=dtype,
                device_map="auto",
                low_cpu_mem_usage=True,
            )
        except Exception as exc:  # noqa: BLE001 - 上游报错五花八门
            raise EngineError(f"加载 {source} 失败：{exc}") from exc

        self._model.eval()
        self._local_dir = local if source == local else ""
        self.loaded = True
        self.report("load", 100.0, "VLM 就绪")

    # ---------------------------------------------------------------- 识别

    def recognize(self, pages: list[Bitmap], options: dict[str, Any]) -> list[Page]:
        if self._model is None:
            self.load()
        prompt = str(options.get("prompt") or self.options.get("prompt") or DEFAULT_PROMPT)
        max_new_tokens = int(options.get("max_new_tokens") or self.options.get("max_new_tokens") or 4096)
        total = max(1, len(pages))
        results: list[Page] = []

        for position, bitmap in enumerate(pages):
            if bitmap.image is None:
                raise EngineError("VLM 需要像素数据，拿到的是空位图")
            self.report("ocr", position / total * 100.0, f"推理第 {position + 1}/{total} 页")
            text = self._generate(bitmap, prompt, max_new_tokens)
            blocks = [Block(text=text, kind="text")] if text.strip() else []
            page = Page(index=bitmap.index, width=bitmap.width, height=bitmap.height, blocks=blocks)
            page.markdown = text.strip()
            results.append(page.finish())
            self.report("ocr", (position + 1) / total * 100.0, f"已推理 {position + 1}/{total} 页")
        return results

    def _generate(self, bitmap: Bitmap, prompt: str, max_new_tokens: int) -> str:
        import torch

        processor, model = self._processor, self._model
        assert processor is not None and model is not None
        image = bitmap.image

        try:
            conversation = [
                {
                    "role": "user",
                    "content": [
                        {"type": "image", "image": image},
                        {"type": "text", "text": prompt},
                    ],
                }
            ]
            templated = processor.apply_chat_template(conversation, add_generation_prompt=True, tokenize=False)
            inputs = processor(text=[templated], images=[image], return_tensors="pt")
        except Exception:  # noqa: BLE001 - 退回最朴素的「图片 + 提示词」
            inputs = processor(images=image, text=prompt, return_tensors="pt")

        device = getattr(model, "device", self._device)
        inputs = {key: value.to(device) if hasattr(value, "to") else value for key, value in inputs.items()}
        prompt_len = inputs["input_ids"].shape[-1]
        with torch.inference_mode():
            generated = model.generate(**inputs, max_new_tokens=max_new_tokens, do_sample=False)
        decoded = processor.batch_decode(generated[:, prompt_len:], skip_special_tokens=True)
        return decoded[0] if decoded else ""


def _dir_stats(root: str) -> tuple[int, int]:
    total = 0
    files = 0
    for dirpath, _dirnames, filenames in os.walk(root):
        for name in filenames:
            try:
                total += os.path.getsize(os.path.join(dirpath, name))
                files += 1
            except OSError:
                continue
    return total, files
