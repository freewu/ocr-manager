"""HuggingFace 权重下载 + 进度上报。

`snapshot_download` 没有回调，只能挂 tqdm：把 huggingface_hub 里那几处
`tqdm` 引用换成我们的子类，按「所有进度条加起来」算总百分比。
挂不上也不致命 —— 最坏情况只是进度条变成「不确定态」。
"""

from __future__ import annotations

import contextlib
import os
import threading
from typing import Callable, Iterator

Report = Callable[[str, "float | None", str], None]


class _Aggregate:
    """把所有进度条的 (n, total) 加总。"""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._bars: dict[int, tuple[int, int]] = {}
        self._seq = 0
        self._last_percent = -1.0

    def register(self, total: int) -> int:
        with self._lock:
            self._seq += 1
            self._bars[self._seq] = (0, max(0, total))
            return self._seq

    def update(self, bar: int, done: int, total: int) -> float | None:
        with self._lock:
            current = self._bars.get(bar)
            self._bars[bar] = (done, max(total, current[1] if current else 0))
            done_sum = sum(value[0] for value in self._bars.values())
            total_sum = sum(value[1] for value in self._bars.values())
        if total_sum <= 0:
            return None
        percent = min(100.0, done_sum * 100.0 / total_sum)
        if abs(percent - self._last_percent) < 0.5:
            return None
        self._last_percent = percent
        return percent

    def reset(self) -> None:
        with self._lock:
            self._bars.clear()
            self._last_percent = -1.0


def _make_tqdm(base, aggregate: _Aggregate, report: Report, stage: str):
    class ReportingTqdm(base):  # type: ignore[misc, valid-type]
        def __init__(self, *args, **kwargs):
            super().__init__(*args, **kwargs)
            self._bar_id = aggregate.register(int(self.total or 0))

        def update(self, n=1):  # noqa: D102
            value = super().update(n)
            percent = aggregate.update(self._bar_id, int(self.n or 0), int(self.total or 0))
            if percent is not None:
                report(stage, percent, "")
            return value

        def close(self):  # noqa: D102
            try:
                super().close()
            finally:
                percent = aggregate.update(self._bar_id, int(self.total or 0), int(self.total or 0))
                if percent is not None:
                    report(stage, percent, "")

    return ReportingTqdm


@contextlib.contextmanager
def progress_hook(report: Report, stage: str = "download") -> Iterator[None]:
    """在 with 块里把 huggingface_hub / tqdm 的进度接到 report 上。"""
    aggregate = _Aggregate()
    patched: list[tuple[object, str, object]] = []
    try:
        import tqdm as tqdm_module

        reporting = _make_tqdm(tqdm_module.tqdm, aggregate, report, stage)
        for module in _tqdm_modules():
            if module is None:
                continue
            original = getattr(module, "tqdm", None)
            if original is None:
                continue
            patched.append((module, "tqdm", original))
            setattr(module, "tqdm", reporting)
    except Exception:  # noqa: BLE001 - 进度上报失败不影响下载
        pass

    try:
        yield
    finally:
        for module, name, original in patched:
            try:
                setattr(module, name, original)
            except Exception:  # noqa: BLE001
                pass


def _tqdm_modules() -> list[object]:
    modules: list[object] = []
    for name in ("tqdm", "tqdm.auto", "huggingface_hub.file_download", "huggingface_hub._snapshot_download"):
        try:
            modules.append(__import__(name, fromlist=["tqdm"]))
        except Exception:  # noqa: BLE001
            modules.append(None)  # type: ignore[arg-type]
    return modules


def configure_hub(models_dir: str, endpoint: str, offline: bool = False) -> None:
    """把 HF 的缓存 / 镜像 / 离线开关塞进环境变量（子进程里生效）。"""
    os.environ.setdefault("HF_HOME", models_dir)
    os.environ.setdefault("HF_HUB_CACHE", os.path.join(models_dir, "hub"))
    if endpoint:
        os.environ["HF_ENDPOINT"] = endpoint
    if offline:
        os.environ["HF_HUB_OFFLINE"] = "1"


def download_repo(
    repo: str,
    target_dir: str,
    *,
    endpoint: str = "",
    models_dir: str = "",
    offline: bool = False,
    report: Report | None = None,
    allow_patterns: list[str] | None = None,
    ignore_patterns: list[str] | None = None,
) -> dict:
    """把 `owner/name` 拉到 target_dir，返回 {path, bytes, files}。"""
    if not repo:
        raise RuntimeError("这个模型没有配置 HuggingFace 仓库，无法下载")

    if models_dir:
        configure_hub(models_dir, endpoint, offline)

    try:
        from huggingface_hub import snapshot_download
    except ImportError as exc:  # pragma: no cover
        raise RuntimeError("缺少 huggingface_hub：在「设置 → Python 环境」安装依赖后再下载权重") from exc

    os.makedirs(target_dir, exist_ok=True)
    token = os.environ.get("HF_TOKEN") or None
    hook = progress_hook(report, "download") if report else contextlib.nullcontext()
    with hook:
        path = snapshot_download(
            repo_id=repo,
            local_dir=target_dir,
            resume=True,
            token=token,
            allow_patterns=allow_patterns,
            ignore_patterns=ignore_patterns,
        )
    size, files = dir_stats(path)
    return {"path": path, "bytes": size, "files": files}


def dir_stats(root: str) -> tuple[int, int]:
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


def repo_dir_name(repo: str) -> str:
    """和 HF 缓存一致的目录名，`owner/name` → `models--owner--name`。"""
    owner, _, name = repo.partition("/")
    if not owner or not name:
        return repo.replace("/", "--")
    return f"models--{owner}--{name}"
