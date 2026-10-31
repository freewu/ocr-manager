package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ------------------------------------------------------------------ API 类型
//
// 与 ocr-server 的 JSON 保持一一对应（internal/models、internal/core、internal/history）。

// ModelSpec 是 config/models.toml 里的一条定义（服务端 flatten 展开）。
type ModelSpec struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Engine      string         `json:"engine"`
	Repo        string         `json:"repo"`
	Homepage    string         `json:"homepage"`
	Description string         `json:"description"`
	Languages   []string       `json:"languages"`
	Tasks       []string       `json:"tasks"`
	Weights     string         `json:"weights"`
	Extras      []string       `json:"extras"`
	RequiresGPU bool           `json:"requires_gpu"`
	Platforms   []string       `json:"platforms"`
	Hidden      bool           `json:"hidden"`
	SizeHint    string         `json:"size_hint"`
	Tags        []string       `json:"tags"`
	Featured    bool           `json:"featured"`
	Probe       bool           `json:"probe"`
	Options     map[string]any `json:"options"`
}

// WeightsInfo 是权重落盘情况。
type WeightsInfo struct {
	Ready bool   `json:"ready"`
	Path  string `json:"path"`
	Bytes uint64 `json:"bytes"`
	Files int    `json:"files"`
	At    int64  `json:"at"`
}

// RepoFile 是 Hub 上的一个文件。
type RepoFile struct {
	Name  string `json:"name"`
	Bytes uint64 `json:"bytes"`
}

// RepoInfo 是 HuggingFace 仓库探测结果。
type RepoInfo struct {
	Repo         string     `json:"repo"`
	Exists       bool       `json:"exists"`
	Reachable    bool       `json:"reachable"`
	Endpoint     string     `json:"endpoint"`
	PipelineTag  *string    `json:"pipeline_tag"`
	LibraryName  *string    `json:"library_name"`
	Tags         []string   `json:"tags"`
	Files        []RepoFile `json:"files"`
	TotalBytes   uint64     `json:"total_bytes"`
	Downloads    uint64     `json:"downloads"`
	Likes        uint64     `json:"likes"`
	LastModified *string    `json:"last_modified"`
	Gated        bool       `json:"gated"`
	Private      bool       `json:"private"`
	Error        *string    `json:"error"`
}

// ModelStatus 是一条模型的运行时状态。
type ModelStatus struct {
	ID      string      `json:"id"`
	State   string      `json:"state"`
	Message string      `json:"message"`
	Error   string      `json:"error"`
	Loaded  bool        `json:"loaded"`
	Ready   bool        `json:"weights_ready"`
	NeedsDl bool        `json:"needs_download"`
	Extras  []string    `json:"extras"`
	ExtOK   bool        `json:"extras_ready"`
	GPUNeed bool        `json:"requires_gpu"`
	GPUOK   bool        `json:"gpu_ok"`
	Workers int         `json:"workers"`
	Weights WeightsInfo `json:"weights"`
	Repo    *RepoInfo   `json:"repo"`
	Updated int64       `json:"updated_at"`
}

// ModelInfo 是 /api/models 的一条记录。
type ModelInfo struct {
	ModelSpec
	Status ModelStatus `json:"status"`
}

// EnvStatus 是 python venv 的状态。
type EnvStatus struct {
	State       string          `json:"state"`
	Message     string          `json:"message"`
	Base        *string         `json:"base"`
	VenvPython  string          `json:"venv_python"`
	VenvReady   bool            `json:"venv_ready"`
	Extras      map[string]bool `json:"extras"`
	ProbeErrors []string        `json:"probe_errors"`
	Updated     int64           `json:"updated_at"`
}

// GpuDevice 是一块显卡。
type GpuDevice struct {
	Index    int    `json:"index"`
	Name     string `json:"name"`
	MemoryMB uint64 `json:"memory_mb"`
	Driver   string `json:"driver"`
}

// GpuInfo 是本机 GPU 探测结果。
type GpuInfo struct {
	Available bool        `json:"available"`
	Devices   []GpuDevice `json:"devices"`
	Error     string      `json:"error"`
	CheckedAt int64       `json:"checked_at"`
}

// Settings 是识别默认参数。
type Settings struct {
	DefaultModel string                    `json:"default_model"`
	ActiveModel  string                    `json:"active_model"`
	HFEndpoint   string                    `json:"hf_endpoint"`
	AutoLoad     bool                      `json:"auto_load"`
	MaxPages     int                       `json:"max_pages"`
	PDFDPI       int                       `json:"pdf_dpi"`
	MaxSide      int                       `json:"max_side"`
	Options      map[string]map[string]any `json:"options"`
}

// AppInfo 是 /api/info。
type AppInfo struct {
	Version   string            `json:"version"`
	Root      string            `json:"root"`
	Dirs      map[string]string `json:"dirs"`
	Active    string            `json:"active"`
	Settings  Settings          `json:"settings"`
	Env       EnvStatus         `json:"env"`
	Op        *Op               `json:"op"`
	GPU       GpuInfo           `json:"gpu"`
	Models    int               `json:"models"`
	Visible   int               `json:"visible"`
	Sources   []string          `json:"sources"`
	Tesseract string            `json:"tesseract"`
	Docs      map[string]string `json:"docs"`
}

// Op 是后端正在跑的后台任务（装依赖 / 下权重 / 重建 venv）。
type Op struct {
	Kind    string `json:"kind"`
	Model   string `json:"model"`
	Label   string `json:"label"`
	Started int64  `json:"started"`
}

// ElapsedMS 返回后台任务已经跑了多久。
func (o *Op) ElapsedMS() int64 {
	if o == nil || o.Started == 0 {
		return 0
	}
	return nowMilli() - o.Started
}

// Block 是识别结果里的一块内容。
type Block struct {
	Text       string      `json:"text"`
	Kind       string      `json:"kind"`
	Box        [][]float64 `json:"box"`
	Confidence *float64    `json:"confidence"`
}

// ResultPage 是识别结果的一页。
type ResultPage struct {
	Index int `json:"index"`
	// Source 是这一页来自哪个输入文件（产物目录里的副本名，如 02-report.pdf）。
	Source   string  `json:"source,omitempty"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Text     string  `json:"text"`
	Markdown string  `json:"markdown"`
	Blocks   []Block `json:"blocks"`
}

// OcrResult 是 result.json 的形状。
type OcrResult struct {
	Engine     string          `json:"engine"`
	Pages      []ResultPage    `json:"pages"`
	Meta       json.RawMessage `json:"meta"`
	DurationMS int64           `json:"duration_ms"`
}

// HistoryEntry 是历史记录的一行（meta.json）。
type HistoryEntry struct {
	ID         string         `json:"id"`
	Model      string         `json:"model"`
	ModelName  string         `json:"model_name"`
	Engine     string         `json:"engine"`
	Status     string         `json:"status"`
	Error      string         `json:"error"`
	SourceName string         `json:"source_name"`
	SourceKind string         `json:"source_kind"`
	InputFile  string         `json:"input_file"`
	Bytes      int64          `json:"bytes"`
	CreatedAt  int64          `json:"created_at"`
	DurationMS int64          `json:"duration_ms"`
	Pages      int            `json:"pages"`
	Blocks     int            `json:"blocks"`
	Chars      int            `json:"chars"`
	Width      int            `json:"width"`
	Height     int            `json:"height"`
	Preview    string         `json:"preview"`
	Options    map[string]any `json:"options"`
}

// Job 是一次识别的进度快照。
type Job struct {
	ID         string        `json:"id"`
	Model      string        `json:"model"`
	ModelName  string        `json:"model_name"`
	Status     string        `json:"status"`
	Stage      string        `json:"stage"`
	Message    string        `json:"message"`
	Progress   float64       `json:"progress"`
	SourceName string        `json:"source_name"`
	SourceKind string        `json:"source_kind"`
	Pages      int           `json:"pages"`
	Blocks     int           `json:"blocks"`
	Chars      int           `json:"chars"`
	DurationMS int64         `json:"duration_ms"`
	Error      string        `json:"error"`
	Entry      *HistoryEntry `json:"entry"`
	CreatedAt  int64         `json:"created_at"`
	FinishedAt int64         `json:"finished_at"`
}

// Event 是 SSE / /api/logs 里的一条事件。
type Event struct {
	Type    string        `json:"type"`
	Ts      int64         `json:"ts"`
	Source  string        `json:"source"`
	Level   string        `json:"level"`
	Message string        `json:"message"`
	Stage   string        `json:"stage"`
	Percent *float64      `json:"percent"`
	Job     string        `json:"job"`
	Model   string        `json:"model"`
	State   string        `json:"state"`
	Data    *Job          `json:"data"`
	Bytes   *ProgressByte `json:"bytes"`
}

// ProgressByte 是下载进度里的字节数。
type ProgressByte struct {
	Done       uint64 `json:"done"`
	Total      uint64 `json:"total"`
	FilesDone  uint32 `json:"files_done"`
	FilesTotal uint32 `json:"files_total"`
	File       string `json:"file"`
	FileDone   uint64 `json:"file_done"`
	FileTotal  uint64 `json:"file_total"`
}

// LicenseOrTags 从 tags 里挑出许可与管线信息，拼成一行说明。
func (i *RepoInfo) LicenseOrTags() string {
	out := []string{}
	if i.PipelineTag != nil && *i.PipelineTag != "" {
		out = append(out, "pipeline:"+*i.PipelineTag)
	}
	for _, tag := range i.Tags {
		if strings.HasPrefix(tag, "license:") {
			out = append(out, strings.TrimPrefix(tag, "license:"))
		}
	}
	return strings.Join(out, " · ")
}

// ------------------------------------------------------------------ 客户端

type client struct {
	base string
	http *http.Client
}

func newClient(base string) *client {
	return &client{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 5 * time.Minute}}
}

func (c *client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode >= 400 {
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &failure) == nil && failure.Error != "" {
			return fmt.Errorf("%s", failure.Error)
		}
		return fmt.Errorf("%s %s: %s", method, path, res.Status)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// ping 是健康检查：连上就说明后端可以用了。
func (c *client) ping(ctx context.Context) bool {
	_, ok := c.health(ctx)
	return ok
}

// healthInfo 是 /api/health 的形状：桌面壳拿它判断端口上是谁、是不是老进程。
type healthInfo struct {
	OK      bool   `json:"ok"`
	Service string `json:"service"`
	Version string `json:"version"`
	Uptime  int64  `json:"uptime"` // 进程已经跑了多少秒
	PID     int    `json:"pid"`
}

// health 探一下端口上的服务是不是 ocr-server（顺带拿版本与 pid）。
func (c *client) health(ctx context.Context) (healthInfo, bool) {
	ctx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer cancel()
	var body healthInfo
	if err := c.do(ctx, http.MethodGet, "/api/health", nil, &body); err != nil {
		return healthInfo{}, false
	}
	// 端口上可能是别的服务（比如 tts-server），认准 service 名
	if !body.OK || body.Service != "ocr-server" {
		return healthInfo{}, false
	}
	return body, true
}

func (c *client) info(ctx context.Context) (AppInfo, error) {
	var body AppInfo
	err := c.do(ctx, http.MethodGet, "/api/info", nil, &body)
	return body, err
}

func (c *client) models(ctx context.Context, refresh bool) ([]ModelInfo, error) {
	var body struct {
		Models []ModelInfo `json:"models"`
	}
	path := "/api/models"
	if refresh {
		path += "?refresh=1"
	}
	err := c.do(ctx, http.MethodGet, path, nil, &body)
	return body.Models, err
}

func (c *client) prepare(ctx context.Context, id string, force bool) error {
	path := "/api/models/" + id + "/prepare"
	if force {
		path += "?force=1"
	}
	return c.do(ctx, http.MethodPost, path, nil, nil)
}

func (c *client) activate(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/api/models/"+id+"/activate", nil, nil)
}

func (c *client) deactivate(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/api/models/"+id+"/deactivate", nil, nil)
}

func (c *client) env(ctx context.Context, refresh bool) (EnvStatus, error) {
	var body struct {
		Env EnvStatus `json:"env"`
	}
	path := "/api/env"
	if refresh {
		path += "?refresh=1"
	}
	err := c.do(ctx, http.MethodGet, path, nil, &body)
	return body.Env, err
}

func (c *client) installEnv(ctx context.Context, extras []string) error {
	return c.do(ctx, http.MethodPost, "/api/env/install", map[string]any{"extras": extras}, nil)
}

func (c *client) recreateEnv(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/env/recreate", nil, nil)
}

func (c *client) selectBasePython(ctx context.Context, base string) error {
	return c.do(ctx, http.MethodPost, "/api/env/base", map[string]any{"base": base}, nil)
}

func (c *client) settings(ctx context.Context) (Settings, error) {
	var body struct {
		Settings Settings `json:"settings"`
	}
	err := c.do(ctx, http.MethodGet, "/api/settings", nil, &body)
	return body.Settings, err
}

func (c *client) saveSettings(ctx context.Context, patch Settings) (Settings, error) {
	var body struct {
		Settings Settings `json:"settings"`
	}
	err := c.do(ctx, http.MethodPost, "/api/settings", patch, &body)
	return body.Settings, err
}

func (c *client) saveLayout(ctx context.Context, modelsDir, outDir string) error {
	return c.do(ctx, http.MethodPost, "/api/layout", map[string]any{
		"models_dir": modelsDir,
		"out_dir":    outDir,
	}, nil)
}

// recognize 提交一次识别：桌面壳直接传本机路径，后端负责拷副本到产物目录。
func (c *client) recognize(ctx context.Context, model string, paths []string, options map[string]any) (Job, error) {
	var body struct {
		Job Job `json:"job"`
	}
	err := c.do(ctx, http.MethodPost, "/api/recognize", map[string]any{
		"model":   model,
		"paths":   paths,
		"options": options,
	}, &body)
	return body.Job, err
}

// exportXLSX 拉一次识别导出的 Excel 字节。
func (c *client) exportXLSX(ctx context.Context, id string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/history/"+url.PathEscape(id)+"/export.xlsx", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("导出 Excel 失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &failure) == nil && failure.Error != "" {
			return nil, fmt.Errorf("导出 Excel 失败：%s", failure.Error)
		}
		return nil, fmt.Errorf("导出 Excel 失败：%s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func (c *client) job(ctx context.Context, id string) (Job, error) {
	var body struct {
		Job Job `json:"job"`
	}
	err := c.do(ctx, http.MethodGet, "/api/jobs/"+id, nil, &body)
	return body.Job, err
}

func (c *client) cancelJob(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/api/jobs/"+id+"/cancel", nil, nil)
}

func (c *client) history(ctx context.Context, limit int) ([]HistoryEntry, error) {
	var body struct {
		Items []HistoryEntry `json:"items"`
	}
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/history?limit=%d", limit), nil, &body)
	return body.Items, err
}

func (c *client) historyItem(ctx context.Context, id string) (HistoryEntry, OcrResult, error) {
	var body struct {
		Entry  HistoryEntry `json:"entry"`
		Result OcrResult    `json:"result"`
	}
	err := c.do(ctx, http.MethodGet, "/api/history/"+id, nil, &body)
	return body.Entry, body.Result, err
}

func (c *client) deleteHistory(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/history/"+id, nil, nil)
}

func (c *client) logs(ctx context.Context, n int) ([]Event, error) {
	var body struct {
		Events []Event `json:"events"`
	}
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/logs?n=%d", n), nil, &body)
	return body.Events, err
}

// fileURL 是产物文件的下载 / 预览地址（点「打开」时交给系统浏览器）。
func (c *client) fileURL(id, name string) string {
	return fmt.Sprintf("%s/api/history/%s/files/%s?download=1", c.base, id, name)
}
