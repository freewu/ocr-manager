"""OCR Manager 内置 worker。

这个包被 Go 二进制 `go:embed` 进发布物，首次启动解包到 `$OCR_HOME/runtime/python`。
只依赖标准库就能跑 `mock` 引擎；其余引擎按需安装 extras（见 `pyproject.toml`）。
"""

__version__ = "0.1.0"
