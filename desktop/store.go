package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/oneo-me/nsgo/core"
)

// logLine 是日志列表里的一行。
type logLine struct {
	key     string
	at      time.Time
	level   string
	source  string
	message string
	// 进度事件额外带上这些：模型卡上要画百分比和已下字节数
	stage      string
	detail     string
	percent    *float64
	bytesDone  uint64
	bytesTotal uint64
}

// store 是 UI 的全部数据。只在引擎 goroutine 上读写：
// 其它 goroutine 通过 ui.post 回到引擎再改。
type store struct {
	connected bool
	status    string
	connErr   string
	info      *AppInfo
	models    []ModelInfo
	env       EnvStatus
	logs      []logLine
	history   []HistoryEntry
	job       *Job
	result    *OcrResult
	// files 是识别页选进来的输入（图片 / PDF，可混选多个）
	files []pickedFile
	// previewData 是预览面板要显示的图片字节；previewNote 说明为什么没图
	previewData []byte
	previewNote string
	// detail 是历史页选中的那一条（entry + 结果）。
	detail  *historyDetail
	busy    string
	notice  string
	err     string
	toastAt int64
	// op 是后端正在跑的后台任务（装依赖 / 下权重），界面拿它画进度
	op *Op
	// fp 是上一次渲染内容指纹：轮询时没变就不重绘整棵树
	fp string
	// expanded 记哪些模型卡展开了详情：默认收着，只露摘要
	expanded map[string]bool
	// needPaste 只有在系统里弹不出文件对话框时才为真：那时才把「粘贴路径」框露出来
	needPaste bool
}

type historyDetail struct {
	entry  HistoryEntry
	result OcrResult
}

// say 弹一条成功/信息类提示。
func (s *store) say(msg string) {
	s.notice, s.err, s.toastAt = msg, "", nowMilli()
}

// fail 弹一条错误提示。
func (s *store) fail(msg string) {
	s.err, s.notice, s.toastAt = msg, "", nowMilli()
}

func (s *store) clearToast() {
	s.err, s.notice, s.toastAt = "", "", 0
}

// ui 把信号、数据和动作绑在一起。
type ui struct {
	cx  *core.Scope
	ctx context.Context
	cl  *client
	st  *store
	rev *core.Signal[int]

	page  *core.Signal[string]
	model *core.Signal[string]
	// pickPath 是「手动添加路径」输入框
	pickPath *core.Signal[string]
	// selFile 是左侧列表里选中的文件（预览跟着它走）
	selFile *core.Signal[string]
	// previewOpen 控制右侧预览面板显不显示（可以整块关掉，列表就占满宽度）
	previewOpen *core.Signal[bool]
	// resultFile 是结果区选中的文件 tab
	resultFile *core.Signal[string]
	maxPages   *core.Signal[string]
	pdfDPI     *core.Signal[string]
	filter     *core.Signal[string]
	histSel    *core.Signal[string]
	resultTab  *core.Signal[string]
	pageIdx    *core.Signal[int]
	basePy     *core.Signal[string]
	modelsDir  *core.Signal[string]
	outDir     *core.Signal[string]
	hfEp       *core.Signal[string]
	maxSide    *core.Signal[string]

	// previewZoom 是预览的缩放倍数（1 = 适应面板），previewPan 是拖出来的位移
	previewZoom *core.Signal[float32]
	previewPan  *core.Signal[core.Point]
	// previewBox 是预览面板的实际尺寸，previewNatW/H 是图片原始像素尺寸
	previewBox  core.Bounds
	previewNatW int
	previewNatH int
	panBase     core.Point
	pinchBase   float32

	refreshing atomic.Bool
}

func newUI(cx *core.Scope) *ui {
	u := &ui{
		cx:          cx,
		ctx:         cx.Context(),
		cl:          newClient(apiBase()),
		st:          &store{status: "正在启动…", expanded: map[string]bool{}},
		rev:         core.NewSignal(cx, 0),
		page:        core.NewSignal(cx, "recognize"),
		model:       core.NewSignal(cx, ""),
		pickPath:    core.NewSignal(cx, ""),
		selFile:     core.NewSignal(cx, ""),
		previewOpen: core.NewSignal(cx, true),
		resultFile:  core.NewSignal(cx, ""),
		maxPages:    core.NewSignal(cx, "20"),
		pdfDPI:      core.NewSignal(cx, "200"),
		filter:      core.NewSignal(cx, filterAll),
		histSel:     core.NewSignal(cx, ""),
		resultTab:   core.NewSignal(cx, "text"),
		pageIdx:     core.NewSignal(cx, 0),
		basePy:      core.NewSignal(cx, ""),
		modelsDir:   core.NewSignal(cx, ""),
		outDir:      core.NewSignal(cx, ""),
		hfEp:        core.NewSignal(cx, ""),
		maxSide:     core.NewSignal(cx, ""),
		previewZoom: core.NewSignal(cx, previewZoomFit),
		previewPan:  core.NewSignal(cx, core.Point{}),
	}
	cx.Cleanup(stopServer)
	return u
}

// data 读取时让当前绑定跟着 rev 一起更新。
func (u *ui) data() *store {
	u.rev.Get()
	return u.st
}

// bump 触发所有绑定重新求值。
func (u *ui) bump() { u.rev.Set(u.rev.Get() + 1) }

// post 把一段改动送回 UI 线程执行（后台 goroutine 里改数据一律走它）。
func (u *ui) post(fn func()) { u.cx.Post(fn) }

// connect 连接后端：先探本地端口，连不上就自己拉起 ocr-server。
func (u *ui) connect() {
	u.post(func() {
		u.st.status = "正在连接后端…"
		u.st.connErr = ""
		u.bump()
	})
	go func() {
		err := startServer(u.ctx, u.cl, func(msg string) {
			u.post(func() {
				u.st.status = msg
				u.bump()
			})
		})
		if err != nil {
			u.post(func() {
				u.st.status = "后端没起来"
				u.st.connErr = err.Error()
				u.bump()
			})
			return
		}
		u.post(func() {
			u.st.connected = true
			u.st.status = "已连接 " + u.cl.base
			u.bump()
		})
		u.refresh()
	}()
}

// refresh 拉一次全量状态（识别任务在跑时顺带追进度）。
func (u *ui) refresh() {
	if u.refreshing.Swap(true) {
		return
	}
	go func() {
		defer u.refreshing.Store(false)
		info, err := u.cl.info(u.ctx)
		if err != nil {
			u.post(func() {
				u.st.connected = false
				u.st.status = "连接断了"
				u.st.connErr = err.Error()
				u.bump()
			})
			return
		}
		models, err := u.cl.models(u.ctx, false)
		if err != nil {
			u.post(func() { u.st.fail(err.Error()) })
			return
		}
		env, _ := u.cl.env(u.ctx, false)
		events, _ := u.cl.logs(u.ctx, 200)

		var job *Job
		if id := u.currentJobID(); id != "" {
			if snapshot, err := u.cl.job(u.ctx, id); err == nil {
				job = &snapshot
			}
		}

		// 识别在跑的时候不去翻 100 份产物目录：磁盘 I/O 会拖慢进度刷新，
		// 反正任务结束时还会刷新一轮。
		running := job != nil && job.Status == "running"
		var history []HistoryEntry
		if !running {
			history, _ = u.cl.history(u.ctx, 100)
		}

		u.post(func() {
			u.st.connected = true
			u.st.status = "已连接 " + u.cl.base
			u.st.connErr = ""
			u.st.info = &info
			u.st.op = info.Op
			u.st.models = models
			u.st.env = env
			u.st.logs = toLogLines(events)
			if !running {
				u.st.history = history
			}
			if job != nil {
				u.applyJob(*job)
			}
			// 模型下拉没选就落到默认模型 / 第一个可用模型
			if u.model.Get() == "" {
				u.model.Set(pickModel(models, info.Settings.DefaultModel))
			}
			u.maxPages.Set(strconv.Itoa(info.Settings.MaxPages))
			u.pdfDPI.Set(strconv.Itoa(info.Settings.PDFDPI))
			if u.hfEp.Get() == "" {
				u.hfEp.Set(info.Settings.HFEndpoint)
			}
			if u.maxSide.Get() == "" {
				u.maxSide.Set(strconv.Itoa(info.Settings.MaxSide))
			}
			if u.basePy.Get() == "" && info.Env.Base != nil {
				u.basePy.Set(*info.Env.Base)
			}
			if info.Dirs != nil {
				if u.modelsDir.Get() == "" {
					u.modelsDir.Set(info.Dirs["models"])
				}
				if u.outDir.Get() == "" {
					u.outDir.Set(info.Dirs["output"])
				}
			}
			// 轮询只做「真的变了才重画」：后台下载进度在动的时候才有必要重画，
			// 闲着时候每秒重建整棵树会让主页面看起来在抖。
			if fp := u.st.fingerprint(); fp != u.st.fp {
				u.st.fp = fp
				u.bump()
			}
		})
	}()
}

// applyJob 把任务快照写进 store（完成时顺便拉结果）。
func (u *ui) applyJob(job Job) {
	prev := u.st.job
	u.st.job = &job
	if job.Status == "done" && (prev == nil || prev.ID != job.ID || prev.Status != "done") {
		u.loadResult(job.ID)
	}
}

// loadResult 从历史产物里读回 result.json。
func (u *ui) loadResult(id string) {
	go func() {
		entry, result, err := u.cl.historyItem(u.ctx, id)
		if err != nil {
			u.post(func() { u.st.fail("读识别结果失败：" + err.Error()) })
			return
		}
		u.post(func() {
			u.st.result = &result
			u.st.detail = &historyDetail{entry: entry, result: result}
			u.pageIdx.Set(0)
			u.bump()
		})
	}()
}

func (u *ui) currentJobID() string {
	u.rev.Get()
	if u.st.job == nil {
		return ""
	}
	if u.st.job.Status == "done" || u.st.job.Status == "error" || u.st.job.Status == "canceled" {
		return ""
	}
	return u.st.job.ID
}

// ------------------------------------------------------------------ 动作

// guard 包一层「忙 + 报错 + 刷新」的公共流程。
func (u *ui) guard(label string, fn func(ctx context.Context) error) {
	u.post(func() {
		u.st.busy = label
		u.st.clearToast()
		u.bump()
	})
	go func() {
		err := fn(u.ctx)
		u.post(func() {
			u.st.busy = ""
			if err != nil {
				u.st.fail(err.Error())
			}
			u.bump()
		})
		if err == nil {
			time.Sleep(200 * time.Millisecond)
			u.refresh()
		}
	}()
}

func (u *ui) prepare(id string, force bool) {
	// 先本地翻成「准备中」：不等后端回话界面就有反应，下载本体全在后台。
	u.markPreparing(id)
	u.startOp(func(ctx context.Context) error { return u.cl.prepare(ctx, id, force) })
}

// markPreparing 把某条模型本地改成「准备中」，省得用户重复点下载。
func (u *ui) markPreparing(id string) {
	u.post(func() {
		for i := range u.st.models {
			if u.st.models[i].ID != id {
				continue
			}
			u.st.models[i].Status.State = "preparing"
			u.st.models[i].Status.Message = "正在后台准备…"
		}
		u.st.clearToast()
		u.bump()
	})
}

// startOp 发一个后台长任务（装依赖 / 下权重）：立刻返回，进度靠轮询推进。
//
// 不用 guard：那是给「几毫秒就回来」的动作用的，长任务一挂上 busy 会把
// 主页面的按钮全部置灰，看着像卡死了。
func (u *ui) startOp(call func(ctx context.Context) error) {
	go func() {
		err := call(u.ctx)
		u.post(func() {
			if err != nil {
				u.st.fail(err.Error())
			} else {
				u.st.say("已经在后台开始，进度见模型卡与日志")
			}
			u.bump()
		})
		if err == nil {
			u.refresh()
		}
	}()
}

func (u *ui) activate(id string) {
	u.guard("启用 "+id, func(ctx context.Context) error { return u.cl.activate(ctx, id) })
}

func (u *ui) deactivate(id string) {
	u.guard("卸载 "+id, func(ctx context.Context) error { return u.cl.deactivate(ctx, id) })
}

// recognize 提交识别：路径来自识别页的文本框（每行一个）。
func (u *ui) recognize() {
	paths := make([]string, 0, len(u.st.files))
	for _, file := range u.st.files {
		paths = append(paths, file.Path)
	}
	if len(paths) == 0 {
		u.post(func() { u.st.fail("先选要识别的图片 / PDF（点「选择文件」，或手动粘贴路径）") })
		return
	}
	model := u.model.Get()
	if model == "" {
		u.post(func() { u.st.fail("先选一个 OCR 模型") })
		return
	}
	options := map[string]any{
		"max_pages": intOr(u.maxPages.Get(), 20),
		"pdf_dpi":   intOr(u.pdfDPI.Get(), 200),
	}
	u.post(func() {
		u.st.busy = "提交识别"
		u.st.result = nil
		u.st.clearToast()
		u.bump()
	})
	go func() {
		job, err := u.cl.recognize(u.ctx, model, paths, options)
		u.post(func() {
			u.st.busy = ""
			if err != nil {
				u.st.fail(err.Error())
			} else {
				u.applyJob(job)
			}
			u.bump()
		})
		u.refresh()
	}()
}

func (u *ui) cancelJob() {
	job := u.data().job
	if job == nil {
		return
	}
	u.guard("取消任务", func(ctx context.Context) error { return u.cl.cancelJob(ctx, job.ID) })
}

func (u *ui) installEnv(extras []string) {
	u.startOp(func(ctx context.Context) error { return u.cl.installEnv(ctx, extras) })
}

func (u *ui) recreateEnv() {
	u.startOp(func(ctx context.Context) error { return u.cl.recreateEnv(ctx) })
}

func (u *ui) selectBasePython(base string) {
	u.guard("切换基础解释器", func(ctx context.Context) error { return u.cl.selectBasePython(ctx, base) })
}

// saveSettings 把识别默认参数 + 目录覆盖一起提交。
func (u *ui) saveSettings() {
	settings := Settings{}
	if info := u.data().info; info != nil {
		settings = info.Settings
	}
	settings.MaxPages = intOr(u.maxPages.Get(), settings.MaxPages)
	settings.PDFDPI = intOr(u.pdfDPI.Get(), settings.PDFDPI)
	settings.MaxSide = intOr(u.maxSide.Get(), settings.MaxSide)
	settings.HFEndpoint = strings.TrimSpace(u.hfEp.Get())
	// 一个模型都没启用时下拉是空的，别把默认模型一起清掉
	if id := u.model.Get(); id != "" {
		settings.DefaultModel = id
	}
	u.guard("保存设置", func(ctx context.Context) error {
		if _, err := u.cl.saveSettings(ctx, settings); err != nil {
			return err
		}
		modelsDir, outDir := strings.TrimSpace(u.modelsDir.Get()), strings.TrimSpace(u.outDir.Get())
		if modelsDir == "" && outDir == "" {
			return nil
		}
		return u.cl.saveLayout(ctx, modelsDir, outDir)
	})
}

// deleteHistory 删掉一条历史产物。
func (u *ui) deleteHistory(id string) {
	u.guard("删除记录", func(ctx context.Context) error { return u.cl.deleteHistory(ctx, id) })
}

// selectHistory 选中一条历史记录并拉回结果。
func (u *ui) selectHistory(id string) {
	u.histSel.Set(id)
	u.loadResult(id)
}

// copyText 把一段文字放进剪贴板。
func (u *ui) copyText(text, what string) {
	u.post(func() {
		u.cx.SetClipboard(text)
		u.st.say("已复制" + what)
		u.bump()
	})
}

// openResultFile 用系统浏览器打开产物文件（.md / .txt / .json）。
func (u *ui) openResultFile(id, name string) {
	if err := openURL(u.cl.fileURL(id, name)); err != nil {
		u.post(func() { u.st.fail(err.Error()) })
	}
}

// openDir 在文件管理器里打开目录。
func (u *ui) openDir(dir string) {
	if err := openPath(dir); err != nil {
		u.post(func() { u.st.fail(err.Error()) })
	}
}

// openModelHome 打开模型官网。
func (u *ui) openModelHome(url string) {
	if err := openURL(url); err != nil {
		u.post(func() { u.st.fail(err.Error()) })
	}
}

// ------------------------------------------------------------------ 工具

func nowMilli() int64 { return time.Now().UnixMilli() }

func toLogLines(events []Event) []logLine {
	lines := make([]logLine, 0, len(events))
	for _, ev := range events {
		line := logLine{
			key:     fmt.Sprintf("%d-%d", ev.Ts, len(lines)),
			at:      time.UnixMilli(ev.Ts),
			level:   ev.Level,
			source:  ev.Source,
			message: ev.Message,
		}
		switch ev.Type {
		case "progress":
			line.level = "progress"
			line.stage = ev.Stage
			line.detail = ev.Message
			line.percent = ev.Percent
			if ev.Bytes != nil {
				line.bytesDone, line.bytesTotal = ev.Bytes.Done, ev.Bytes.Total
			}
			pct := ""
			if ev.Percent != nil {
				pct = fmt.Sprintf("%.0f%% ", *ev.Percent)
			}
			line.message = strings.TrimSpace(pct + ev.Stage + " " + ev.Message)
			if line.source == "" {
				line.source = ev.Job
			}
		case "status":
			line.level = "status"
			line.source = "status"
			line.message = ev.Model + " → " + ev.State + " " + ev.Message
		case "env":
			line.level = "env"
			line.source = "env"
			line.message = ev.State + " " + ev.Message
		case "job":
			line.level = "status"
			line.source = "job"
			if ev.Data != nil {
				line.message = ev.Data.ID + " " + ev.Data.Status + " " + ev.Data.Message
			}
		}
		if line.source == "" {
			line.source = "app"
		}
		if line.level == "" {
			line.level = "info"
		}
		lines = append(lines, line)
	}
	return lines
}

// fingerprint 给「界面会显示的东西」算个指纹：轮询时没变就不重画整棵树。
func (s *store) fingerprint() string {
	var b strings.Builder
	if s.info != nil {
		fmt.Fprintf(&b, "v:%s|a:%s|e:%s:%s", s.info.Version, s.info.Active, s.info.Env.State, s.info.Env.Message)
		set := s.info.Settings
		fmt.Fprintf(&b, "|set:%s|%d|%d|%d|%s|%v", set.DefaultModel, set.MaxPages, set.PDFDPI, set.MaxSide, set.HFEndpoint, set.AutoLoad)
	}
	if s.op != nil {
		// 带上已经跑了几秒：不然后台任务的「已跑 12s」会停在开始那一刻
		fmt.Fprintf(&b, "|op:%s:%s:%d:%d", s.op.Kind, s.op.Model, s.op.Started, s.op.ElapsedMS()/1000)
	}
	fmt.Fprintf(&b, "|env:%s:%s", s.env.State, s.env.Message)
	for _, m := range s.models {
		st := m.Status
		fmt.Fprintf(&b, "|m:%s:%s:%s:%s:%v:%v:%v:%d:%d:%d", m.ID, st.State, st.Message, st.Error,
			st.Ready, st.ExtOK, st.Loaded, st.Workers, st.Weights.Files, st.Weights.Bytes)
	}
	if s.job != nil {
		fmt.Fprintf(&b, "|j:%s:%s:%.1f:%s:%d:%d:%d", s.job.ID, s.job.Status, s.job.Progress,
			s.job.Message, s.job.Pages, s.job.Blocks, s.job.Chars)
	}
	if s.result != nil {
		fmt.Fprintf(&b, "|r:%d:%s", len(s.result.Pages), s.result.Engine)
	}
	fmt.Fprintf(&b, "|log:%d", len(s.logs))
	if n := len(s.logs); n > 0 {
		b.WriteString(s.logs[n-1].key)
		b.WriteString(s.logs[n-1].message)
	}
	fmt.Fprintf(&b, "|hist:%d", len(s.history))
	if len(s.history) > 0 {
		b.WriteString(s.history[0].ID)
	}
	fmt.Fprintf(&b, "|conn:%v:%s:%s", s.connected, s.status, s.connErr)
	return b.String()
}

// modelProgress 从最近的事件里翻出这条模型的下载 / 加载进度。
func (u *ui) modelProgress(id string) (*float64, string) {
	for i := len(u.st.logs) - 1; i >= 0; i-- {
		line := u.st.logs[i]
		if line.level != "progress" || line.source != id {
			continue
		}
		note := strings.TrimSpace(line.detail)
		if note == "" {
			note = strings.TrimSpace(line.stage)
		}
		if line.bytesTotal > 0 {
			size := fmt.Sprintf("%s / %s", fmtBytes(line.bytesDone), fmtBytes(line.bytesTotal))
			if note == "" {
				note = size
			} else {
				note += " · " + size
			}
		}
		return line.percent, note
	}
	return nil, ""
}

// reprobeEnv 丢掉后端缓存、真探一遗 python 环境（界面上的「重新检测」）。
func (u *ui) reprobeEnv() {
	go func() {
		env, err := u.cl.env(u.ctx, true)
		u.post(func() {
			if err != nil {
				u.st.fail(err.Error())
			} else {
				u.st.env = env
				u.st.say("环境已重新检测：" + env.State)
			}
			u.bump()
		})
	}()
}

// opBusy 报告后端是否有后台长任务在跑（装依赖 / 下权重 / 重建 venv）。
func (u *ui) opBusy() bool { return u.st.op != nil }

// opLabel 是后台上台任务的一句话说明（侧边栏底部用）。
func (u *ui) opLabel() string {
	op := u.st.op
	if op == nil {
		return ""
	}
	return fmt.Sprintf("%s（后台 %ds）", op.Label, op.ElapsedMS()/1000)
}

// pickModel 选出识别页默认模型：清单默认的已启用模型 → 随便一个已启用的。
//
// 下拉里只列「已启用」的模型，所以这里也只能在它们里面挑；一个都没启用就
// 返回空串（下次轮询有新模型启用了再挑）。
func pickModel(models []ModelInfo, preferred string) string {
	enabled := func(m ModelInfo) bool {
		return m.Status.State == "ready" || m.Status.State == "running"
	}
	for _, m := range models {
		if m.ID == preferred && enabled(m) {
			return m.ID
		}
	}
	for _, m := range models {
		if enabled(m) {
			return m.ID
		}
	}
	return ""
}

// splitPaths 把多行文本拆成路径列表（去掉空行和首尾空白）。
func splitPaths(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func intOr(raw string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v <= 0 {
		if fallback > 0 {
			return fallback
		}
		return 1
	}
	return v
}
