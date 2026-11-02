"""把标准输出切成 UTF-8。

Windows 控制台默认是 GBK/CP936，`print("✓ 已打包 …")` 会直接抛
UnicodeEncodeError 把脚本打死（输出被 just / IDE 接走时尤其必现）。

用法：脚本开头 `import utf8_console  # noqa: F401`（scripts/ 就是 sys.path[0]，
`python scripts/build.py` 这种跑法直接能 import 到）。
"""

from __future__ import annotations

import sys

for _stream in (sys.stdout, sys.stderr):
    _encoding = (getattr(_stream, "encoding", "") or "").lower()
    if _encoding in ("utf-8", "utf8"):
        continue
    try:
        _stream.reconfigure(encoding="utf-8", errors="replace")
    except (AttributeError, ValueError):
        # 老解释器 / 被换成了别的流对象：那就按原样输出，起码别因为这句话崩
        pass
