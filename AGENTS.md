# AGENTS.md

给在这个仓库里干活的编码助手（也给我自己）的约定。改代码前先看一眼。

## 提交与推送（每次开发任务结束都要做）

任务做完 ≠ 交付完成，代码要落到远程：

1. `git status` + `git diff` 看清楚到底改了什么，别把 `bin/`、`dist/`、`node_modules/` 之类带上。
2. 跑一遍检查（见「检查清单」），有失败先修，别把红的推上去。
3. `git add -A` 然后 `git commit`，说明用中文、一句话讲清「做了什么」：
   - 前缀：`feat:` 新功能 / `fix:` 修问题 / `chore:` 杂活 / `docs:` 文档 / `refactor:` 重构 / `build:` 构建打包
   - 例：`feat: 桌面壳加系统托盘与打包图标`
   - 一个任务一个提交；无关的顺手改动单独提。
4. `git push origin HEAD`（远程 `origin` = https://github.com/freewu/ocr-manager.git）。
5. 推送失败（没凭据 / 网络 / 需要 rebase）要如实说明，不要假装推过了。

## 技术栈与结构

Go 后端 + NSGo（Go 原生桌面壳）+ React/TS/Vite（浏览器前端）+ 内置 Python worker。

```
cmd/ocr-server/   Go 服务入口（net/http + go:embed）
internal/         core 门面 / api / pyenv / worker / hub / history / models / paths / gpu
python/           ocr_worker：JSONL stdio 协议，每个模型一个进程
desktop/          独立 Go module 的 NSGo 桌面壳（nsgo.yaml、托盘、打包）
web/              React 前端（dev 端口 5273，代理 /api）
scripts/*.py      just 配方背后的真逻辑；justfile 只是入口
config/models.toml 模型清单（同时嵌进二进制做默认值）
```

## 常用命令

```bash
just check          # fmt + lint + test + py-test + web-check（提交前必跑）
just dev            # 后端 8899 + 前端 5273
just desktop        # 编译并开桌面窗口（nsgo dev，带图标、自动重建）
just desktop-build  # 打包到 bin/ 与 dist/（exe 含 Win32 图标资源 + ocr-server）
just icon           # 从 logo.png 生成 desktop/assets/Icon.png 与 web/public/*
just cli info       # 跟运行中的服务对话（模型 / 环境 / 日志）
```

## 约定

- **端口**：服务默认 `8899`，Vite dev `5273`，`OCR_API` 显式设置优先级最高。改端口要一次替换全链路（`internal/api`、`cmd/ocr-server`、`desktop/backend.go`、`web/vite.config.ts`、`justfile`、`scripts/*.py`、README）。
- **UI 文案不许出现 `just ...`**：这是要独立发布的应用，错误提示一律指向界面操作（「设置 → Python 环境」点「安装依赖」、模型卡点「下载」）。
- **中文注释、中文文案**：报错要能直接告诉用户下一步点哪里。
- **NSGo 的坑**：没有 `Scope.UseEffect`（用 goroutine + `time.NewTimer`）；没有托盘 API（`desktop/tray_windows.go` 直接用 Win32）；组件在 `base` 包不在 `core`；`fill()` 容器记得 `FlexShrink: 1`；`nsgotest` 判存在用 `app.Exists`。
- **桌面壳的图标**来自 `desktop/assets/Icon.png`，由 NSGo 打包时编成 exe 资源——所以要用 `just desktop` / `just desktop-build`，裸 `go run` 出来的窗口没有图标。
- **worker 协议稳定**：dup fd1 做协议通道、stdout 重定向 stderr，单请求失败只回 error 不崩进程，同 worker 请求串行。
- **health 自报家门**：`/api/health` 必须带 `"service":"ocr-server"`，客户端和 doctor 都校验（历史上因为端口串台误连过 tts-server）。
- **输入文件按 `01-原始名` 落盘**：多文件一次识别时副本名必须带序号（`history.SaveInput`），否则同名文件互相覆盖；`Page.Source` 记副本名，前端靠它按文件分 tab（`displaySource()` 去掉序号前缀再显示）。
- **文件对话框自己实现**：`desktop/filedialog_windows.go` 用 `comdlg32` 的 `GetOpenFileNameW`/`GetSaveFileNameW`（多选返回「目录\0文件名\0…」），Linux 走 zenity/kdialog、macOS 走 osascript；都没有就提示手动粘贴路径。别让它跑在 UI 线程上等。
- **Excel 导出不引第三方库**：`internal/xlsx` 用 `archive/zip` 现拼 OOXML（inlineStr + 数值单元格 + 样式表），`history.ExportXLSX` 出「分块」「全文」两张表，接口是 `GET /api/history/{id}/export.xlsx`。
- **生成物不入库**：`bin/`、`dist/`、`web/node_modules`、`web/dist`、`__pycache__`、`*.syso`、`*.tsbuildinfo`。

## 检查清单

提交前：

```bash
just check        # 或分开跑：go vet/test（根 + desktop）、python3 -m unittest、npm run check/build
```

改桌面壳 / 打包脚本时另外确认：

```bash
cd desktop && GOOS=windows go build ./...   # 托盘代码只在 Windows 编译，必须交叉编译验过
python3 scripts/desktop.py build            # 产物里有 .rsrc 图标资源、exe 同级有 ocr-server
```
