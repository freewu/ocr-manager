package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bluefrog/ocr-manager/internal/events"
	"github.com/bluefrog/ocr-manager/internal/history"
	"github.com/bluefrog/ocr-manager/internal/models"
	"github.com/bluefrog/ocr-manager/internal/paths"
	"github.com/bluefrog/ocr-manager/internal/worker"
)

// 任务状态。
const (
	JobRunning  = "running"
	JobDone     = "done"
	JobError    = "error"
	JobCanceled = "canceled"
)

// Input 是一个待识别的文件（API 层已经落到临时目录）。
type Input struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// RecognizeRequest 是一次识别请求。
type RecognizeRequest struct {
	Model   string         `json:"model"`
	Inputs  []Input        `json:"inputs"`
	Options map[string]any `json:"options"`
}

// Job 是一次识别的进度快照（列表页 / SSE 补拉都用它）。
type Job struct {
	ID         string         `json:"id"`
	Model      string         `json:"model"`
	ModelName  string         `json:"model_name"`
	Status     string         `json:"status"`
	Stage      string         `json:"stage"`
	Message    string         `json:"message"`
	Progress   float64        `json:"progress"`
	SourceName string         `json:"source_name"`
	SourceKind string         `json:"source_kind"`
	Pages      int            `json:"pages"`
	Blocks     int            `json:"blocks"`
	Chars      int            `json:"chars"`
	DurationMS int64          `json:"duration_ms"`
	Error      string         `json:"error,omitempty"`
	Entry      *history.Entry `json:"entry,omitempty"`
	CreatedAt  int64          `json:"created_at"`
	FinishedAt int64          `json:"finished_at"`

	cancel context.CancelFunc
}

// StartRecognize 建任务并异步跑，立刻返回任务快照（前端靠 SSE 追进度）。
func (c *Core) StartRecognize(req RecognizeRequest) (*Job, error) {
	spec, ok := c.Spec(req.Model)
	if !ok {
		return nil, fmt.Errorf("未知模型：%s", req.Model)
	}
	if err := c.unavailableGate(spec, "识别"); err != nil {
		return nil, err
	}
	if len(req.Inputs) == 0 {
		return nil, fmt.Errorf("没有要识别的文件")
	}
	if !c.extrasReady(spec) && spec.Engine != "mock" {
		if spec.Engine == "external" {
			return nil, fmt.Errorf("「%s」的命令行还没装：先点模型卡上的「安装命令」", spec.Name)
		}
		return nil, fmt.Errorf("「%s」的依赖没装好：先点模型卡上的「安装依赖」", spec.Name)
	}
	if err := c.weightsGate(spec); err != nil {
		return nil, err
	}

	primary := req.Inputs[0]
	ctx, cancel := context.WithCancel(context.Background())
	job := &Job{
		ID:         paths.NewJobID(),
		Model:      spec.ID,
		ModelName:  spec.Name,
		Status:     JobRunning,
		Stage:      "queued",
		Message:    "排队中",
		SourceName: primary.Name,
		SourceKind: kindOf(primary.Name),
		CreatedAt:  history.NowMilli(),
		cancel:     cancel,
	}

	// 输入副本同步落到产物目录：上传的临时文件在 HTTP handler 返回后就会被删掉，
	// 所以必须在这之前拷完，异步任务只操作自己的副本。
	var files []string
	var firstInput string
	var firstBytes int64
	for index, input := range req.Inputs {
		name, bytes, err := history.SaveInput(c.Paths, job.ID, index, input.Path, input.Name)
		if err != nil {
			cancel()
			_ = os.RemoveAll(c.Paths.JobDir(job.ID))
			return nil, fmt.Errorf("保存输入副本失败：%w", err)
		}
		if firstInput == "" {
			firstInput, firstBytes = name, bytes
		}
		files = append(files, filepath.Join(c.Paths.JobDir(job.ID), name))
	}
	options := c.modelOptions(spec.ID, req.Options)

	c.mu.Lock()
	c.jobs[job.ID] = job
	c.mu.Unlock()

	go c.runJob(ctx, job, spec, files, firstInput, firstBytes, options)
	return cloneJob(job), nil
}

func (c *Core) runJob(ctx context.Context, job *Job, spec models.ModelSpec, files []string, firstInput string, firstBytes int64, options map[string]any) {
	started := time.Now()
	defer job.cancel()

	fail := func(err error) {
		status := JobError
		if ctx.Err() != nil {
			status = JobCanceled
		}
		c.finishJob(job, status, err.Error(), history.Entry{}, history.Result{}, 0, 0, 0, time.Since(started))
	}

	c.updateJob(job.ID, func(j *Job) {
		j.Stage = "load"
		j.Message = "准备模型"
		j.Progress = 2
	})

	w, err := c.acquireWorker(spec, job.ID)
	if err != nil {
		fail(err)
		return
	}

	if len(files) == 0 {
		fail(fmt.Errorf("没有可识别的文件"))
		return
	}

	c.updateJob(job.ID, func(j *Job) {
		j.Stage = "ocr"
		j.Message = "开始识别"
		j.Progress = 5
	})

	payload, err := w.Call(ctx, "recognize", map[string]any{
		"files":   files,
		"options": options,
	}, &worker.Callbacks{Progress: c.jobProgress(job.ID)})
	if err != nil {
		if ctx.Err() != nil {
			// 取消时把 worker 一起关掉：请求已经没人等了，继续占显存没意义
			w.Shutdown(2 * time.Second)
			c.dropWorker(spec.ID, w)
		}
		fail(err)
		return
	}

	result, err := decodeResult(payload)
	if err != nil {
		fail(err)
		return
	}

	pages, blocks, chars, preview := history.Summary(result)
	entry := history.Entry{
		ID:         job.ID,
		Model:      spec.ID,
		ModelName:  spec.Name,
		Engine:     spec.Engine,
		Status:     history.StatusDone,
		SourceName: job.SourceName,
		SourceKind: job.SourceKind,
		InputFile:  firstInput,
		Bytes:      firstBytes,
		CreatedAt:  job.CreatedAt,
		DurationMS: time.Since(started).Milliseconds(),
		Pages:      pages,
		Blocks:     blocks,
		Chars:      chars,
		Preview:    preview,
		Width:      firstPageWidth(result),
		Height:     firstPageHeight(result),
		Options:    options,
	}
	result.DurationMS = entry.DurationMS
	if err := history.Write(c.Paths, entry, result); err != nil {
		fail(fmt.Errorf("写结果文件失败：%w", err))
		return
	}

	c.finishJob(job, JobDone, "", entry, result, pages, blocks, chars, time.Since(started))
	c.Bus.Info(spec.ID, fmt.Sprintf("识别完成：%d 页 / %d 块 / %.1fs", pages, blocks, time.Since(started).Seconds()))
}

// finishJob 收尾（错误也走这里，保证前端一定能收到终态）。
func (c *Core) finishJob(job *Job, status, message string, entry history.Entry, result history.Result, pages, blocks, chars int, took time.Duration) {
	c.mu.Lock()
	current, ok := c.jobs[job.ID]
	if !ok {
		current = job
		c.jobs[job.ID] = current
	}
	current.Status = status
	current.Message = message
	current.FinishedAt = history.NowMilli()
	current.DurationMS = took.Milliseconds()
	if status == JobDone {
		current.Progress = 100
		current.Stage = "done"
		current.Message = "识别完成"
		current.Pages = pages
		current.Blocks = blocks
		current.Chars = chars
		snapshot := entry
		current.Entry = &snapshot
	}
	if status == JobError {
		current.Error = message
		current.Stage = "error"
	}
	if status == JobCanceled {
		current.Stage = "canceled"
		current.Message = "已取消"
		current.Error = message
	}
	snapshot := cloneJob(current)
	c.mu.Unlock()

	if status == JobError {
		c.Bus.Error(job.Model, "识别失败："+message)
	}
	c.Bus.Emit(jobEvent(snapshot))
}

// jobEvent 是一类特殊事件：`type=job`，前端据此刷新任务卡片。
func jobEvent(job *Job) events.Event {
	return events.Event{Type: "job", Ts: history.NowMilli(), Data: job}
}

func (c *Core) updateJob(id string, mutate func(*Job)) {
	c.mu.Lock()
	job, ok := c.jobs[id]
	if ok {
		mutate(job)
	}
	c.mu.Unlock()
}

// jobProgress 把 worker 进度映射到任务上（顺便发 progress + job 事件）。
func (c *Core) jobProgress(id string) func(worker.Progress) {
	return func(p worker.Progress) {
		c.updateJob(id, func(j *Job) {
			if p.Stage != "" {
				j.Stage = p.Stage
			}
			if p.Percent != nil {
				// worker 的进度是 0-100，任务整体留 5% 给收尾
				j.Progress = 5 + (*p.Percent)*0.9
			}
			if p.Message != "" {
				j.Message = p.Message
			}
		})

		message := p.Message
		c.Bus.Emit(events.Event{
			Type:    events.TypeProgress,
			Ts:      history.NowMilli(),
			Job:     id,
			Stage:   p.Stage,
			Percent: p.Percent,
			Message: &message,
			Bytes:   p.Bytes,
		})

		if snapshot, ok := c.Job(id); ok {
			c.Bus.Emit(jobEvent(&snapshot))
		}
	}
}

// Job 返回任务快照。
func (c *Core) Job(id string) (Job, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	job, ok := c.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *cloneJob(job), true
}

// Jobs 返回最近的任务（按创建时间倒序）。
func (c *Core) Jobs(limit int) []Job {
	c.mu.Lock()
	out := make([]Job, 0, len(c.jobs))
	for _, job := range c.jobs {
		out = append(out, *cloneJob(job))
	}
	c.mu.Unlock()
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt > out[b].CreatedAt })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// CancelJob 取消一个跑着的任务。
func (c *Core) CancelJob(id string) error {
	c.mu.Lock()
	job, ok := c.jobs[id]
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("未知任务：%s", id)
	}
	if job.Status != JobRunning {
		return fmt.Errorf("任务已结束：%s", job.Status)
	}
	if job.cancel != nil {
		job.cancel()
	}
	return nil
}

// PruneJobs 清掉内存里已完成的任务（历史记录在磁盘上，不受影响）。
func (c *Core) PruneJobs(keep int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	done := make([]string, 0, len(c.jobs))
	for id, job := range c.jobs {
		if job.Status != JobRunning {
			done = append(done, id)
		}
	}
	sort.Slice(done, func(a, b int) bool {
		return c.jobs[done[a]].CreatedAt < c.jobs[done[b]].CreatedAt
	})
	removed := 0
	for _, id := range done {
		if len(c.jobs) <= keep {
			break
		}
		delete(c.jobs, id)
		removed++
	}
	return removed
}

// History 返回历史记录读取器。
func (c *Core) HistoryList(limit int) []history.Entry { return history.List(c.Paths, limit) }

// HistoryGet 读一条历史。
func (c *Core) HistoryGet(id string) (history.Entry, history.Result, error) {
	return history.Get(c.Paths, id)
}

// HistoryDelete 删一条历史。
func (c *Core) HistoryDelete(id string) error { return history.Delete(c.Paths, id) }

// ExportXLSX 把一次识别导出成 Excel 字节流（桌面壳存盘、浏览器直接下载）。
func (c *Core) ExportXLSX(id string) ([]byte, error) { return history.ExportXLSX(c.Paths, id) }

// HistoryFile 返回历史目录里的可下载文件。
func (c *Core) HistoryFile(id, name string) (string, error) { return history.File(c.Paths, id, name) }

func decodeResult(payload map[string]any) (history.Result, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return history.Result{}, fmt.Errorf("结果序列化失败：%w", err)
	}
	var result history.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return history.Result{}, fmt.Errorf("结果解析失败：%w", err)
	}
	for index := range result.Pages {
		if result.Pages[index].Blocks == nil {
			result.Pages[index].Blocks = []history.Block{}
		}
	}
	return result, nil
}

func cloneJob(job *Job) *Job {
	copied := *job
	copied.cancel = nil
	return &copied
}

func kindOf(name string) string {
	if strings.EqualFold(filepath.Ext(name), ".pdf") {
		return "pdf"
	}
	return "image"
}

func firstPageWidth(result history.Result) int {
	if len(result.Pages) == 0 {
		return 0
	}
	return result.Pages[0].Width
}

func firstPageHeight(result history.Result) int {
	if len(result.Pages) == 0 {
		return 0
	}
	return result.Pages[0].Height
}

// SaveUpload 把上传上来的临时文件转成 Input（API 层用）。
func SaveUpload(path, name string) (Input, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Input{}, err
	}
	return Input{Path: path, Name: name, Size: info.Size()}, nil
}
