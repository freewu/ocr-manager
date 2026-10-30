# OCR Manager —— Go 后端 + NSGo(Go) 原生桌面 + React Web + 内置 Python worker
#
# 跨平台：Windows 用 cmd，其余用 bash。需要脚本逻辑的配方都收敛到 scripts/*.py。
#
#   just --list    查看全部命令
#   just doctor    环境自检

set shell := ["bash", "-uc"]
set windows-shell := ["cmd", "/c"]

WEB := "web"
PYDIR := "python"
PORT := env("OCR_PORT", "8899")
API := "http://127.0.0.1:" + PORT
PY := env("OCR_PY", if os() == "windows" { "python" } else { "python3" })

# 所有模型 / 环境 / 识别命令都走这个入口（只跟运行中的服务说话）
CLI := "{{PY}} scripts/cli.py"

# ---------------------------------------------------------------- 默认

# 列出所有命令
default:
    @just --list --unsorted

# 环境自检（Go / Node / Python / 目录状态）
doctor:
    {{PY}} scripts/doctor.py

# ---------------------------------------------------------------- 依赖

# 装前端依赖
setup: web-install

# 安装前端依赖
web-install:
    cd {{WEB}} && npm install --no-fund --no-audit

# 重装前端依赖（Windows / WSL 来回切后原生包不匹配时用）
web-reinstall:
    {{PY}} -c "import shutil;shutil.rmtree('{{WEB}}/node_modules',ignore_errors=True)"
    cd {{WEB}} && npm install --no-fund --no-audit

# 创建 python venv 并安装全部引擎依赖（第一次比较久）
env:
    {{CLI}} env --setup

# 只为某个模型装依赖，例如 just env-model rapidocr
env-model model:
    {{CLI}} env --model {{model}}

# 查看 venv 状态与依赖探测结果
env-status:
    {{CLI}} env

# ---------------------------------------------------------------- 开发

# 一条命令跑起后端 + Vite 前端（Ctrl-C 一起退出）
dev *ARGS:
    {{PY}} scripts/dev.py --port {{PORT}} {{ARGS}}

# 只跑后端（会托管 web/dist）
server *ARGS:
    {{PY}} scripts/dev.py --port {{PORT}} --skip-web --no-open {{ARGS}}

# 只跑前端（Vite 代理到 OCR_PORT；后端要自己开着）
web-dev:
    cd {{WEB}} && OCR_API={{API}} npm run dev

# 构建前端到 web/dist（后端直接托管）
web-build:
    cd {{WEB}} && npm run build

# NSGo 原生桌面壳：装好前端、必要时补编后端，再开窗口
desktop:
    {{PY}} scripts/desktop.py run

# 打包桌面壳到 bin/（exe 里带应用图标）
desktop-build: build
    {{PY}} scripts/desktop.py build

# ---------------------------------------------------------------- 模型

# 列出模型（含权重状态）
list *ARGS:
    {{CLI}} list {{ARGS}}

# 准备模型：装依赖 + 下载权重（就是界面上的「下载」）
prepare model:
    {{CLI}} prepare {{model}}

# 启用模型（同一时刻只保留一个）
activate model:
    {{CLI}} activate {{model}}

# 卸载模型（不填则全部卸载）
deactivate model="":
    {{CLI}} deactivate {{model}}

# 识别：just ocr page.png report.pdf
ocr *FILES:
    {{CLI}} recognize {{FILES}}

# 识别并指定模型：just recognize page.png --model rapidocr
recognize *ARGS:
    {{CLI}} recognize {{ARGS}}

# 历史记录
history *ARGS:
    {{CLI}} history {{ARGS}}

# 查看 / 修改设置：just settings --set max_pages=30
settings *ARGS:
    {{CLI}} settings {{ARGS}}

# 运行时信息（数据目录、venv、GPU）
info:
    {{CLI}} info

# 从 logo.png 生成各端图标（desktop/assets/Icon.png + web/public/*）
icon:
    {{PY}} scripts/make_icon.py

# ---------------------------------------------------------------- 构建 / 检查

# 构建前端 + 后端
build:
    {{PY}} scripts/build.py

# 只编后端，不碰前端
build-go:
    {{PY}} scripts/build.py --skip-web

# 格式化后端
fmt:
    {{PY}} scripts/build.py fmt

# 后端静态检查（含桌面壳）
lint:
    {{PY}} scripts/build.py lint

# 后端测试
test:
    {{PY}} scripts/build.py test

# python worker 测试
py-test:
    cd {{PYDIR}} && {{PY}} -m unittest discover -s tests

# 前端类型检查
web-check:
    cd {{WEB}} && npm run check

# 全量检查
check: fmt lint test py-test web-check
