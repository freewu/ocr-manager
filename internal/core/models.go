package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bluefrog/ocr-manager/internal/events"
	"github.com/bluefrog/ocr-manager/internal/hub"
	"github.com/bluefrog/ocr-manager/internal/models"
	"github.com/bluefrog/ocr-manager/internal/pyenv"
	"github.com/bluefrog/ocr-manager/internal/worker"
)

// 模型状态机。
const (
	StateUnavailable = "unavailable" // 本机跑不了（缺显卡 / 外部工具）
	StateIdle        = "idle"        // 可启用，但没加载
	StatePreparing   = "preparing"   // 装依赖 / 下权重
	StateLoading     = "loading"     // 读进内存
	StateReady       = "ready"       // 已加载，可以识别
	StateRunning     = "running"     // 正在识别
	StateError       = "error"
)

// WeightsInfo 是权重落盘情况（`ready.json` 的内容）。
type WeightsInfo struct {
	Ready bool   `json:"ready"`
	Path  string `json:"path"`
	Bytes uint64 `json:"bytes"`
	Files int    `json:"files"`
	At    int64  `json:"at"`
}

// ModelStatus 是一条模型的运行时状态。
type ModelStatus struct {
	ID      string        `json:"id"`
	State   string        `json:"state"`
	Message string        `json:"message"`
	Error   string        `json:"error,omitempty"`
	Loaded  bool          `json:"loaded"`
	Ready   bool          `json:"weights_ready"`
	NeedsDl bool          `json:"needs_download"`
	Extras  []string      `json:"extras"`
	ExtOK   bool          `json:"extras_ready"`
	GPUNeed bool          `json:"requires_gpu"`
	GPUOK   bool          `json:"gpu_ok"`
	Workers int           `json:"workers"`
	Weights WeightsInfo   `json:"weights"`
	Repo    *hub.RepoInfo `json:"repo,omitempty"`
	Updated int64         `json:"updated_at"`
}

// ModelView 是列表页的一行：清单 + 状态。
type ModelView struct {
	models.ModelSpec
	Status ModelStatus `json:"status"`
}

// Models 返回可见模型 + 状态；refresh=true 时重新探测 Hub 元数据。
func (c *Core) Models(refresh bool) []ModelView {
	c.mu.Lock()
	catalog := c.Catalog
	c.mu.Unlock()

	visible := catalog.Visible()
	out := make([]ModelView, 0, len(visible))
	for _, spec := range visible {
		out = append(out, ModelView{ModelSpec: spec, Status: c.statusOf(spec, refresh)})
	}
	return out
}

// Model 返回单条模型（含状态）。
func (c *Core) Model(modelID string, refresh bool) (ModelView, bool) {
	spec, ok := c.Catalog.Get(modelID)
	if !ok {
		return ModelView{}, false
	}
	return ModelView{ModelSpec: spec, Status: c.statusOf(spec, refresh)}, true
}

// Spec 按 id 取清单条目。
func (c *Core) Spec(modelID string) (models.ModelSpec, bool) {
	c.mu.Lock()
	catalog := c.Catalog
	c.mu.Unlock()
	return catalog.Get(modelID)
}

// refreshAll 重算状态（prewarm=false 时不联网）。
func (c *Core) refreshAll(prewarm bool) {
	c.mu.Lock()
	catalog := c.Catalog
	c.mu.Unlock()

	for _, spec := range catalog.All() {
		c.statusOf(spec, false)
		if prewarm && spec.Probe && spec.Repo != "" {
			go func(id, repo string) {
				info := c.Hub.Info(repo, false)
				c.mu.Lock()
				if cached, ok := c.status[id]; ok {
					cached.Repo = info
					cached.Updated = time.Now().UnixMilli()
				}
				c.mu.Unlock()
				if info != nil && info.Error != nil {
					c.Bus.Warn(id, *info.Error)
				}
			}(spec.ID, spec.Repo)
		}
	}
}

// statusOf 计算一条模型的状态（refresh 只影响 Hub 探测是否走缓存）。
func (c *Core) statusOf(spec models.ModelSpec, refresh bool) ModelStatus {
	c.mu.Lock()
	cached, ok := c.status[spec.ID]
	current := ModelStatus{}
	if ok {
		current = *cached
	}
	w := c.workers[spec.ID]
	c.mu.Unlock()

	status := ModelStatus{
		ID:      spec.ID,
		Extras:  pyenv.ForModel(spec),
		GPUNeed: spec.RequiresGPU,
		GPUOK:   !spec.RequiresGPU || c.GPU.Available,
		NeedsDl: spec.NeedsDownload(),
		Updated: time.Now().UnixMilli(),
	}
	status.Weights = c.weightsInfo(spec)
	status.Ready = status.Weights.Ready

	// 依赖是否齐（探测要起 python，缓存状态里已经有就别重复算）
	if envStatus, err := c.envStatusCached(); err == nil {
		status.ExtOK = true
		for _, extra := range status.Extras {
			if !envStatus.Extras[extra] {
				status.ExtOK = false
				break
			}
			// tesseract 还要系统二进制
			if extra == "tesseract" && !envStatus.Extras["tesseract_binary"] {
				status.ExtOK = false
				break
			}
		}
		if len(status.Extras) == 0 {
			status.ExtOK = true
		}
	}

	if w != nil {
		status.Workers = 1
		status.Loaded = w.Ready()
	}

	// 状态优先级：跑着的 > 报错的 > 准备中 > 就绪 > 空闲
	switch {
	case !status.GPUOK:
		status.State = StateUnavailable
		status.Message = "本机没有 NVIDIA 显卡，这个模型跑不动（装驱动后重新检测）"
	case spec.Engine == "external":
		status.State = StateUnavailable
		status.Message = "外部工具：本应用只登记入口，不代管运行环境"
	case current.State == StatePreparing:
		status.State = StatePreparing
		status.Message = orString(current.Message, "正在准备…")
		status.Error = current.Error
	case current.State == StateLoading:
		status.State = StateLoading
		status.Message = orString(current.Message, "正在加载模型…")
	case status.Loaded:
		status.State = StateReady
		status.Message = "已加载，可以识别"
	case current.State == StateError:
		status.State = StateError
		status.Message = orString(current.Message, "上次操作失败")
		status.Error = current.Error
	case status.NeedsDl && !status.Ready:
		status.State = StateIdle
		status.Message = "还没下载权重"
	case !status.ExtOK:
		status.State = StateIdle
		status.Message = "还没安装依赖"
	default:
		status.State = StateIdle
		status.Message = "可以启用"
	}

	if refresh && spec.Probe && spec.Repo != "" && spec.Engine != "external" {
		status.Repo = c.Hub.Info(spec.Repo, true)
	} else {
		status.Repo = current.Repo
	}

	c.mu.Lock()
	c.status[spec.ID] = &status
	c.mu.Unlock()

	// 状态事件只在真正变化时发，避免前端抖动
	if ok {
		if cached.State != status.State || cached.Message != status.Message {
			message := status.Message
			c.Bus.Status(spec.ID, status.State, &message)
		}
	}
	return status
}

// envStatusCached 缓存 python 探测结果（起子进程，别每次刷新都跑）。
func (c *Core) envStatusCached() (pyenv.EnvStatus, error) {
	c.mu.Lock()
	cached := c.envStatus
	fresh := cached != nil && time.Since(c.envAt) < 30*time.Second
	c.mu.Unlock()
	if cached != nil && fresh {
		return *cached, nil
	}
	// 装依赖 / 下权重期间 venv 正在被改：起子进程探测又慢又会误报，直接用上一份结果。
	if cached != nil && c.op.busy() {
		return *cached, nil
	}

	status := c.Env.Status()
	c.mu.Lock()
	c.envStatus = &status
	c.envAt = time.Now()
	c.mu.Unlock()
	return status, nil
}

// EnvStatus 返回 python 环境状态（30 秒内的缓存直接用，别再起子进程）。
func (c *Core) EnvStatus() pyenv.EnvStatus {
	status, _ := c.envStatusCached()
	return status
}

// EnvStatusFresh 丢掉缓存重新探测（用户在界面上点「重新检测」时用）。
func (c *Core) EnvStatusFresh() pyenv.EnvStatus {
	c.mu.Lock()
	c.envStatus = nil
	c.mu.Unlock()
	status, _ := c.envStatusCached()
	return status
}

// weightsInfo 读权重标记文件。
func (c *Core) weightsInfo(spec models.ModelSpec) WeightsInfo {
	switch spec.WeightsMode() {
	case models.WeightsNone:
		return WeightsInfo{Ready: true, Path: "-", At: 0}
	case models.WeightsAuto:
		// 引擎自己管权重（onnx 包内置 / 首次推理自己拉）：装了依赖就算可用。
		// 这里不能落到下面去读标记文件——auto 的模型永远不会写标记，
		// 读不到就会一直显示「还没权重」，界面上就只剩「安装依赖」这一个按钮。
		return WeightsInfo{Ready: true, Path: "-", At: 0}
	}

	raw, err := os.ReadFile(c.Paths.ModelReadyMarker(spec.ID))
	if err != nil {
		return WeightsInfo{}
	}
	var info WeightsInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return WeightsInfo{}
	}
	info.Ready = true
	return info
}

func (c *Core) writeWeightsMarker(spec models.ModelSpec, info WeightsInfo) error {
	dir := c.Paths.ModelStateDir(spec.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	info.Ready = true
	info.At = time.Now().UnixMilli()
	raw, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.Paths.ModelReadyMarker(spec.ID), raw, 0o644)
}

// Prepare 装依赖 + 下载权重（幂等；force 重新下载）。
func (c *Core) Prepare(modelID string, force bool) error {
	spec, ok := c.Spec(modelID)
	if !ok {
		return fmt.Errorf("未知模型：%s", modelID)
	}
	if spec.Engine == "external" {
		return fmt.Errorf("「%s」是外部工具，本应用不代管它的环境", spec.Name)
	}
	if spec.RequiresGPU && !c.GPU.Available {
		return fmt.Errorf("本机没有 NVIDIA 显卡，无法准备「%s」", spec.Name)
	}

	c.prepareMu.Lock()
	defer c.prepareMu.Unlock()

	if force {
		c.Bus.Info(modelID, "按请求重新准备（重装依赖 / 重新下载权重）")
	}

	c.setState(modelID, StatePreparing, "正在安装 python 依赖…")
	extras := pyenv.ForModel(spec)
	if err := c.Env.Install(c.Bus, extras); err != nil {
		c.failModel(modelID, err)
		return err
	}
	c.mu.Lock()
	c.envStatus = nil
	c.mu.Unlock()

	if spec.NeedsDownload() {
		if force {
			_ = os.Remove(c.Paths.ModelReadyMarker(modelID))
		}
		if !c.weightsInfo(spec).Ready || force {
			c.setState(modelID, StatePreparing, "正在下载权重…")
			w, err := c.spawnWorker(spec)
			if err != nil {
				c.failModel(modelID, err)
				return err
			}
			result, err := w.Call(context.Background(), "prepare", nil, &worker.Callbacks{
				Progress: c.progressCallback(modelID, ""),
			})
			w.Shutdown(5 * time.Second)
			c.dropWorker(modelID, w)
			if err != nil {
				c.failModel(modelID, err)
				return err
			}
			info := WeightsInfo{Path: stringField(result, "path")}
			if bytes, ok := numberField(result, "bytes"); ok {
				info.Bytes = uint64(bytes)
			}
			if files, ok := numberField(result, "files"); ok {
				info.Files = int(files)
			}
			if err := c.writeWeightsMarker(spec, info); err != nil {
				c.failModel(modelID, err)
				return err
			}
			c.Bus.Info(modelID, "权重就绪："+info.Path)
		}
	}

	c.mu.Lock()
	c.status[modelID] = &ModelStatus{ID: modelID, State: StateIdle, Message: "准备完成，可以启用"}
	c.mu.Unlock()
	c.refreshAll(false)
	message := "准备完成"
	c.Bus.Status(modelID, StateIdle, &message)
	return nil
}

// Activate 加载模型（同一时间只保留一个激活模型，其余 worker 会被关掉）。
func (c *Core) Activate(modelID string) error {
	spec, ok := c.Spec(modelID)
	if !ok {
		return fmt.Errorf("未知模型：%s", modelID)
	}
	if spec.Engine == "external" {
		return fmt.Errorf("「%s」是外部工具，本应用不代管它的运行环境", spec.Name)
	}
	if spec.RequiresGPU && !c.GPU.Available {
		return fmt.Errorf("本机没有 NVIDIA 显卡，无法启用「%s」", spec.Name)
	}
	if err := c.weightsGate(spec); err != nil {
		return err
	}
	if !c.extrasReady(spec) {
		return fmt.Errorf("「%s」的 python 依赖还没装：点模型卡上的「安装依赖」，或在「设置 → Python 环境」里装", spec.Name)
	}

	c.setState(modelID, StateLoading, "正在加载模型…")
	if _, err := c.acquireWorker(spec, ""); err != nil {
		c.failModel(modelID, err)
		return err
	}
	c.setState(modelID, StateReady, "已加载，可以识别")
	c.SetActiveModel(modelID)
	return nil
}

// Deactivate 卸载模型（关掉 worker，释放显存）。
func (c *Core) Deactivate(modelID string) error {
	c.mu.Lock()
	w := c.workers[modelID]
	delete(c.workers, modelID)
	if c.active == modelID {
		c.active = ""
	}
	c.mu.Unlock()
	if w == nil {
		c.setState(modelID, StateIdle, "没有正在运行的 worker")
		return nil
	}
	w.Shutdown(5 * time.Second)
	c.Bus.Info(modelID, "已卸载")
	c.mu.Lock()
	delete(c.status, modelID)
	c.mu.Unlock()
	c.refreshAll(false)
	return nil
}

// needsPrepare 表示必须走一次「下载」才能用。
func (c *Core) needsPrepare(spec models.ModelSpec) bool {
	return len(pyenv.ForModel(spec)) > 0 || spec.NeedsDownload()
}

// weightsGate 是「权重」这一关：只有 hub 模式要等下载完成，
// auto / none 由引擎自己管（weightsInfo 直接算就绪），不会卡在这里。
//
// 启用与识别共用这一处，报错文案才跟界面上的按钮名对得上（「下载权重」）。
func (c *Core) weightsGate(spec models.ModelSpec) error {
	if !spec.NeedsDownload() || c.weightsInfo(spec).Ready {
		return nil
	}
	return fmt.Errorf("「%s」还没下载权重：先点模型卡上的「下载权重」", spec.Name)
}

func (c *Core) extrasReady(spec models.ModelSpec) bool {
	extras := pyenv.ForModel(spec)
	if len(extras) == 0 {
		return true
	}
	status, err := c.envStatusCached()
	if err != nil {
		return false
	}
	for _, extra := range extras {
		if extra == "tesseract" {
			if !status.Extras["tesseract"] || !status.Extras["tesseract_binary"] {
				return false
			}
			continue
		}
		if !status.Extras[extra] {
			return false
		}
	}
	return true
}

// acquireWorker 返回已加载的 worker（没有就拉起来并 load）。
func (c *Core) acquireWorker(spec models.ModelSpec, jobID string) (*worker.Worker, error) {
	c.mu.Lock()
	w := c.workers[spec.ID]
	c.mu.Unlock()
	if w != nil && w.Ready() && w.Alive() {
		return w, nil
	}
	if w != nil {
		w.Shutdown(time.Second)
		c.dropWorker(spec.ID, w)
	}

	// 只保留一个激活模型：把别的 worker 关掉，省显存
	c.mu.Lock()
	others := map[string]*worker.Worker{}
	for id, other := range c.workers {
		others[id] = other
		delete(c.workers, id)
	}
	c.active = ""
	c.mu.Unlock()
	for id, other := range others {
		c.Bus.Info(id, "切换到 "+spec.Name+"，卸载上一个模型")
		other.Shutdown(3 * time.Second)
	}

	spawned, err := c.spawnWorker(spec)
	if err != nil {
		return nil, err
	}
	c.setState(spec.ID, StateLoading, "正在加载模型…")
	if err := spawned.Load(context.Background(), &worker.Callbacks{
		Progress: c.progressCallback(spec.ID, jobID),
		Log:      c.logCallback(spec.ID),
	}); err != nil {
		spawned.Shutdown(3 * time.Second)
		c.dropWorker(spec.ID, spawned)
		return nil, err
	}
	c.mu.Lock()
	c.workers[spec.ID] = spawned
	c.active = spec.ID
	c.mu.Unlock()
	c.Bus.Info(spec.ID, "模型已加载")
	return spawned, nil
}

func (c *Core) spawnWorker(spec models.ModelSpec) (*worker.Worker, error) {
	modelsDir := c.Paths.ModelsDir()
	return worker.Spawn(spec, c.Env, modelsDir, hub.Endpoint(), c.Bus)
}

func (c *Core) dropWorker(modelID string, w *worker.Worker) {
	c.mu.Lock()
	if c.workers[modelID] == w {
		delete(c.workers, modelID)
	}
	c.mu.Unlock()
}

// progressCallback 把 worker 进度转成事件（job 不为空时带上任务 id，前端按任务归并）。
func (c *Core) progressCallback(source, jobID string) func(worker.Progress) {
	return func(p worker.Progress) {
		message := p.Message
		c.Bus.Emit(events.Event{
			Type:    events.TypeProgress,
			Ts:      time.Now().UnixMilli(),
			Source:  source,
			Job:     jobID,
			Stage:   p.Stage,
			Percent: p.Percent,
			Message: &message,
			Bytes:   p.Bytes,
		})
	}
}

func (c *Core) logCallback(source string) func(string, string) {
	return func(level, message string) {
		// worker 自己的 log 已经由 worker 包转发到 bus；这里只兜底去重
		_ = level
		_ = message
		_ = source
	}
}

func (c *Core) setState(modelID, state, message string) {
	c.mu.Lock()
	previous := c.status[modelID]
	if previous == nil {
		previous = &ModelStatus{ID: modelID}
	}
	previous.State = state
	previous.Message = message
	previous.Updated = time.Now().UnixMilli()
	if state != StateError {
		previous.Error = ""
	}
	c.status[modelID] = previous
	c.mu.Unlock()
	c.Bus.Status(modelID, state, &message)
}

func (c *Core) failModel(modelID string, err error) {
	c.mu.Lock()
	previous := c.status[modelID]
	if previous == nil {
		previous = &ModelStatus{ID: modelID}
	}
	previous.State = StateError
	previous.Message = err.Error()
	previous.Error = err.Error()
	previous.Updated = time.Now().UnixMilli()
	c.status[modelID] = previous
	c.mu.Unlock()
	c.Bus.Error(modelID, err.Error())
}

// ActiveModel 是当前加载的模型 id（没有就是空串）。
func (c *Core) ActiveModel() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

func stringField(payload map[string]any, key string) string {
	if value, ok := payload[key].(string); ok {
		return value
	}
	return ""
}

func numberField(payload map[string]any, key string) (float64, bool) {
	switch value := payload[key].(type) {
	case float64:
		return value, true
	case string:
		out, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err == nil {
			return out, true
		}
	}
	return 0, false
}

func orString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// ensureDir 是到处都要用的 mkdir -p。
func ensureDir(dir string) error { return os.MkdirAll(filepath.Clean(dir), 0o755) }
