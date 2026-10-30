# OCR Manager

多引擎 OCR 管理工具：**Go 后端 + NSGo（Go 原生桌面壳）+ React Web 前端 + 内置 Python worker**。
图片 / PDF → `text` + `markdown` + `blocks`（框、置信度、类型），带模型清单、权重下载、
Python venv 管理、产物历史。

同一套后端既给浏览器用，也给桌面壳用；桌面壳启动时会自己去找（必要时拉起）`ocr-server`。

浏览器版走上传（`multipart`），桌面壳直接把本机路径交给后端拷副本，所以大文件不占内存。
导出 Excel 走 `GET /api/history/{id}/export.xlsx`，工作簿由标准库现拼（不引第三方表格库）。

![logo](logo.png)

## 特性

- **多引擎**：RapidOCR（CPU 基线）、Tesseract 5、PaddleOCR PP-OCRv5、dots.ocr / DeepSeek-OCR（VLM，需 GPU）、MinerU / Surya（外部登记）
- **三种权重策略**：`hub`（worker 用 `snapshot_download` 拉）、`auto`（引擎自己管）、`none`（mock / 系统 tesseract）
- **不许踩坑的报错**：每个失败都会告诉下一步该干什么，例如「先跑 `just env-model rapidocr`」
- **批量输入**：一次选多张图片 + 多个 PDF，左边文件列表 / 右边预览可拖动分栏，预览能收起
- **按文件看结果**：结果区一个输入文件一个 tab，tab 内再切 text / markdown / 分块
- **导出 Excel**：`分块`表（文件 / 页 / 序号 / 类型 / 置信度 / 坐标 / 文本）+ `全文`表（纯文本 + Markdown）
- **产物可追溯**：每次识别落一个目录（`meta.json` + 输入副本 + `result.json/.md/.txt`），多文件按 `01-原始名` 排序落盘
- **venv 隔离**：依赖装在数据目录下，按模型 extras 增量安装；基础解释器变了会自动重建
- **同一时刻只加载一个引擎**：本地单卡场景，切换模型时自动卸载上一个

## 快速开始

```bash
just doctor      # 环境自检
just setup       # 装前端依赖
just dev         # 后端 8899 + 前端 5273（Ctrl-C 一起退出）
```

浏览器打开 http://127.0.0.1:5273 。第一次用建议先点一个模型的「下载」：

```bash
just env-model rapidocr   # 只装 RapidOCR 的依赖
just prepare rapidocr     # 装依赖 + 下载权重
just activate rapidocr    # 加载引擎
just ocr page.png         # 命令行识别
```

桌面壳：

```bash
just desktop         # 编后端 + 前端，然后开窗口
just desktop-build   # 打包到 bin/ocr-manager（exe 里带应用图标）
```

桌面壳带 Windows 系统托盘：右键托盘图标可以「打开主窗口 / 打开数据目录 / 退出」，
单机或双击图标也能把窗口调出来。窗口 / 任务栏 / 托盘的图标都来自仓库根的
`logo.png`：`just icon` 生成 `desktop/assets/Icon.png`，打包时 NSGo 会把它编成
exe 的 Win32 图标资源（不打包、直接 `go run` 的话 exe 没有这段资源，系统只能
给你默认图标——用 `just desktop` 跑就会自动走 `nsgo dev` 带上图标）。

## 目录结构

```
cmd/ocr-server/     Go 服务入口（net/http + go:embed）
internal/
  core/             唯一门面：模型状态、venv、识别任务、设置
  api/              HTTP / SSE 路由
  events/           事件总线（progress / status / log / job）+ 日志文件
  models/ pyenv/    模型清单、Python 环境
  worker/ hub/      worker 进程协议、HuggingFace 探测
  history/ gpu/ paths/
python/             内置 worker（JSONL stdio 协议，每模型一进程）
  ocr_worker/engines/  mock / rapidocr / tesseract / paddle / vlm / external
desktop/            NSGo 桌面壳（独立 Go module）
web/                React 18 + TS + Vite 前端
config/models.toml  模型清单（可被仓库 / 环境变量 / 用户目录覆盖）
scripts/            just 配方背后的 python（build/dev/desktop/cli/doctor/make_icon）
```

## 数据目录

`OCR_HOME` > 系统数据目录 > `.runtime`，可以用 `OCR_HOME=/path/to/dir` 改。
里面有 `config/settings.json`、`venv/`、`models/`（权重）、`out/`（产物）、`logs/`。

## 常用环境变量

| 变量 | 作用 |
| --- | --- |
| `OCR_HOME` | 数据根目录 |
| `OCR_PORT` / `OCR_API` | 服务端口 / 桌面壳连的地址 |
| `OCR_PYTHON` | 基础 Python 解释器 |
| `OCR_MODELS_FILE` | 额外模型清单（覆盖内置） |
| `HF_ENDPOINT` | HuggingFace 镜像 |
| `OCR_HF_OFFLINE` / `OCR_NO_PREWARM` | 离线 / 不预探测仓库 |
| `OCR_NO_SPAWN` | 桌面壳不自动拉起后端 |
| `OCR_NO_TRAY` | 桌面壳不开系统托盘 |

## 开发命令

```bash
just check        # fmt + lint + test + py-test + web-check
just py-test      # python worker 测试
just web-check    # 前端类型检查
just build        # 前端 + 后端到 bin/
just icon         # 从 logo.png 生成桌面图标与 favicon
```

## 协议要点

worker 用 JSONL：一行一个请求 / 响应，`{"id":..,"cmd":"recognize",...}` → `{"id":..,"ok":true,...}` 或
`{"id":..,"ok":false,"error":"..."}`；进度与日志走独立的 `progress` / `log` 行，单次请求失败不会带崩进程。

## 许可

见 [LICENSE](LICENSE)。各 OCR 引擎与权重有各自的许可，界面上会标出来（非商用 / GPL 等），
模型清单里的 `homepage` 指向官方仓库。
