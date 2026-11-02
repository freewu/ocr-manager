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
just build          # 打包独立可执行程序：dist/ocr-manager-<版本>-<平台>/ + .zip（单文件 exe）
just build-go       # 只编后端到 bin/ocr-server
just icon           # 从 logo.png 生成 desktop/assets/Icon.png 与 web/public/*
just cli info       # 跟运行中的服务对话（模型 / 环境 / 日志）
```

## 约定

- **端口**：服务默认 `8899`，Vite dev `5273`，`OCR_API` 显式设置优先级最高。改端口要一次替换全链路（`internal/api`、`cmd/ocr-server`、`desktop/backend.go`、`web/vite.config.ts`、`justfile`、`scripts/*.py`、README）。
- **UI 文案不许出现 `just ...`**：这是要独立发布的应用，错误提示一律指向界面操作（「设置 → Python 环境」点「安装依赖」、模型卡点「下载」）。
- **中文注释、中文文案**：报错要能直接告诉用户下一步点哪里。
- **NSGo 的坑**：没有 `Scope.UseEffect`（用 goroutine + `time.NewTimer`）；没有托盘 API（`desktop/tray_windows.go` 直接用 Win32）；组件在 `base` 包不在 `core`；`fill()` 容器记得 `FlexShrink: 1`；`nsgotest` 判存在用 `app.Exists`。
- **`core.Scroll` 一律带 `HideIndicator: true`**：NSGo 的 Windows 滚动条拖拽会 nil 解引用 panic（内部 `pressBar → dragBar` 里 `w.barDrag` 已被清空，整个窗口直接挂），而**淡出后看不见的指示条照样能命中**，等于右侧边缘随便一点就崩。关掉指示器后 `barIn` 不把它当命中区域，这条路就走不到了（滚轮 / 拖动 / 键盘照常滚）。新增滚动容器走 `scrollList()`，别手写 `core.Scroll{}` 忘了这个字段。上游 v0.0.0-20261031 仍有此 bug，升级也修不了。
- **跨行选中文字靠祖先 `View.Selectable: true`**：`Style.UserSelect: UserSelectText` 只让**每段文字各自**可选，鼠标拖到下一行就断了；要像网页那样跨元素拖选，得把容器写成 `core.View{Selectable: true, ...}`（NSGo 内部叫 SelectionArea），再用 `Ctrl+C` 复制。日志页、结果正文 / 分块都已经开了，`desktop/page_logs_test.go` 卡住这个字段。反过来：选区里有 `OnPress` 的 View 会把选区挡掉（按钮、可点行要放在选区外）。
- **桌面壳的图标**来自 `desktop/assets/Icon.png`，由 NSGo 打包时编成 exe 资源——所以要用 `just desktop` / `just desktop-build`，裸 `go run` 出来的窗口没有图标。
- **worker 协议稳定**：dup fd1 做协议通道、stdout 重定向 stderr，单请求失败只回 error 不崩进程，同 worker 请求串行。
- **health 自报家门**：`/api/health` 必须带 `"service":"ocr-server"`，客户端和 doctor 都校验（历史上因为端口串台误连过 tts-server）。
- **输入文件按 `01-原始名` 落盘**：多文件一次识别时副本名必须带序号（`history.SaveInput`），否则同名文件互相覆盖；`Page.Source` 记副本名，前端靠它按文件分 tab（`displaySource()` 去掉序号前缀再显示）。
- **文件对话框自己实现**：`desktop/filedialog_windows.go` 用 `comdlg32` 的 `GetOpenFileNameW`/`GetSaveFileNameW`（多选返回「目录\0文件名\0…」），Linux 走 zenity/kdialog、macOS 走 osascript；都没有就提示手动粘贴路径。别让它跑在 UI 线程上等。
- **Excel 导出不引第三方库**：`internal/xlsx` 用 `archive/zip` 现拼 OOXML（inlineStr + 数值单元格 + 样式表），`history.ExportXLSX` 出「分块」「全文」两张表，接口是 `GET /api/history/{id}/export.xlsx`。
- **生成物不入库**：`bin/`、`dist/`、`web/node_modules`、`web/dist`、`__pycache__`、`*.syso`、`*.tsbuildinfo`、`desktop/embedded/ocr-server*`、`desktop/embedded/target.txt`。
- **发布物是一个 exe**：`scripts/desktop.py build` 先把 `bin/ocr-server` 复制到 `desktop/embedded/` 并写 `target.txt`（`goos/goarch`），桌面壳用 `//go:embed embedded` 把它编进去；运行时旁边找不到 `ocr-server` 才解包内置那份到 `$OCR_HOME/runtime/ocr-server-<哈希>.exe`（内容哈希命名，避开 Windows 覆盖运行中 exe）。开发时 `bin/ocr-server` 优先，所以改了后端立刻生效；`target.txt` 对不上就忽略内置那份，交叉编不会拿错平台的二进制。
- **发布构建一律 `-trimpath`**：`assets.go` 用 `runtime.Caller` 找「开发态的 `python/` 目录」，不裁路径的话打包出来的二进制里写的是构建机上的仓库路径——在本机跑发布包就一直用仓库源码，拷到别人机器上才走内置解包，两套行为不一致，线上问题永远复现不出来。裁掉之后 `Caller` 给的是模块路径（非绝对），`devPythonDirs` / `ResolveWebDir` 会跳过它。
- **`//go:embed` 不能写成目录**：`//go:embed python` 会跳过 `_` 开头的文件，`__init__.py` / `__main__.py` 全被漏掉，打包出来的应用一启用模型就 `ImportError: cannot import name '__version__' from 'ocr_worker'`（开发态读仓库目录，看不出来）。现在写成显式 glob（`python/ocr_worker/*.py`、`python/ocr_worker/engines/*.py`、`python/tests/*.py`、`python/pyproject.toml`），`assets_test.go` 会拿仓库清单比对，改了 python 目录结构要同步改 embed 行。
- **动 venv 前先停 worker**：装依赖 / 重建 venv 会替换 venv 里的包（pillow、opencv、onnxruntime…），跑着的 worker 已经把它们加载进内存，替换后再用就报些看不懂的错（踩过：识别直接失败，错误信息是「打不开图片 xxx：」后面什么都没有）。`Core.stopWorkers()` 在 `StartEnvInstall` / `StartEnvRecreate` 开头调，装完用户点识别会自动重新拉起。

## 检查清单

提交前：

```bash
just check        # 或分开跑：go vet/test（根 + desktop）、python3 -m unittest、npm run check/build
```

改桌面壳 / 打包脚本时另外确认：

```bash
cd desktop && GOOS=windows go build ./...   # 托盘代码只在 Windows 编译，必须交叉编译验过
python3 scripts/desktop.py build            # dist/ocr-manager-<版本>-<平台>/ 里是单个 exe（图标资源已链进去）
```
