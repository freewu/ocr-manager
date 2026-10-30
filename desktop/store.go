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
	// detail 是历史页选中的那一条（entry + 结果）。
	detail  *historyDetail
	busy    string
	notice  string
	err     string
	toastAt int64
	// expanded 记哪些模型卡展开了详情：默认收着，只露摘要
	expanded map[string]bool
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
	// paths 是识别页的输入路径（每行一个：图片或 PDF）
	paths     *core.Signal[string]
	maxPages  *core.Signal[string]
	pdfDPI    *core.Signal[string]
	filter    *core.Signal[string]
	histSel   *core.Signal[string]
	resultTab *core.Signal[string]
	pageIdx   *core.Signal[int]
	basePy    *core.Signal[string]
	modelsDir *core.Signal[string]
	outDir    *core.Signal[string]
	hfEp      *core.Signal[string]
	maxSide   *core.Signal[string]

	refreshing atomic.Bool
}

func newUI(cx *core.Scope) *ui {
	u := &ui{
		cx:        cx,
		ctx:       cx.Context(),
		cl:        newClient(apiBase()),
		st:        &store{status: "正在启动…", expanded: map[string]bool{}},
		rev:       core.NewSignal(cx, 0),
		page:      core.NewSignal(cx, "recognize"),
		model:     core.NewSignal(cx, ""),
		paths:     core.NewSignal(cx, ""),
		maxPages:  core.NewSignal(cx, "20"),
		pdfDPI:    core.NewSignal(cx, "200"),
		filter:    core.NewSignal(cx, filterAll),
		histSel:   core.NewSignal(cx, ""),
		resultTab: core.NewSignal(cx, "text"),
		pageIdx:   core.NewSignal(cx, 0),
		basePy:    core.NewSignal(cx, ""),
		modelsDir: core.NewSignal(cx, ""),
		outDir:    core.NewSignal(cx, ""),
		hfEp:      core.NewSignal(cx, ""),
		maxSide:   core.NewSignal(cx, ""),
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
		env, _ := u.cl.env(u.ctx)
		events, _ := u.cl.logs(u.ctx, 200)
		history, _ := u.cl.history(u.ctx, 100)

		var job *Job
		if id := u.currentJobID(); id != "" {
			if snapshot, err := u.cl.job(u.ctx, id); err == nil {
				job = &snapshot
			}
		}

		u.post(func() {
			u.st.connected = true
			u.st.status = "已连接 " + u.cl.base
			u.st.connErr = ""
			u.st.info = &info
			u.st.models = models
			u.st.env = env
			u.st.logs = toLogLines(events)
			u.st.history = history
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
			u.bump()
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
	u.guard("准备 "+id, func(ctx context.Context) error { return u.cl.prepare(ctx, id, force) })
}

func (u *ui) activate(id string) {
	u.guard("启用 "+id, func(ctx context.Context) error { return u.cl.activate(ctx, id) })
}

func (u *ui) deactivate(id string) {
	u.guard("卸载 "+id, func(ctx context.Context) error { return u.cl.deactivate(ctx, id) })
}

// recognize 提交识别：路径来自识别页的文本框（每行一个）。
func (u *ui) recognize() {
	paths := splitPaths(u.paths.Get())
	if len(paths) == 0 {
		u.post(func() { u.st.fail("先填要识别的图片 / PDF 路径（每行一个）") })
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
	label := "安装依赖 " + strings.Join(extras, ",")
	if len(extras) == 0 {
		label = "安装全部依赖"
	}
	u.guard(label, func(ctx context.Context) error { return u.cl.installEnv(ctx, extras) })
}

func (u *ui) recreateEnv() {
	u.guard("重建 venv", func(ctx context.Context) error { return u.cl.recreateEnv(ctx) })
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
	settings.DefaultModel = u.model.Get()
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

// pickModel 选出识别页默认模型：清单默认 → 已就绪 → 第一个可用。
func pickModel(models []ModelInfo, preferred string) string {
	for _, m := range models {
		if m.ID == preferred && m.Status.State != "unavailable" {
			return m.ID
		}
	}
	for _, m := range models {
		if m.Status.State == "ready" || m.Status.State == "running" {
			return m.ID
		}
	}
	for _, m := range models {
		if m.Status.State != "unavailable" && m.Weights != "hub" {
			return m.ID
		}
	}
	if len(models) > 0 {
		return models[0].ID
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
