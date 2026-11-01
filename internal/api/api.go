// Package api 是 HTTP 门面：REST + SSE + 前端静态托管。
//
// 默认只监听 127.0.0.1：这是个本地工具，识别任务要读写本地文件，不该暴露到局域网。
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bluefrog/ocr-manager/internal/core"
	"github.com/bluefrog/ocr-manager/internal/events"
	"github.com/bluefrog/ocr-manager/internal/gpu"
	"github.com/bluefrog/ocr-manager/internal/hardware"
	"github.com/bluefrog/ocr-manager/internal/paths"
)

// Options 是服务器参数。
type Options struct {
	// Addr 是监听地址（默认 127.0.0.1:8899）。
	Addr string
	// WebDir 是前端构建产物目录（空 = 只提供 API）。
	WebDir string
	// Token 是可选的访问口令（非空时要求 `Authorization: Bearer <token>`）。
	Token string
}

// Server 是 HTTP 服务。
type Server struct {
	Core *core.Core
	opts Options
	mux  *http.ServeMux
}

// New 建路由。
func New(c *core.Core, opts Options) *Server {
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:8899"
	}
	s := &Server{Core: c, opts: opts, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler 返回带中间件的 handler。
func (s *Server) Handler() http.Handler { return s.logRequests(s.auth(s.mux)) }

// ListenAndServe 起服务。
func (s *Server) ListenAndServe() error {
	server := &http.Server{
		Addr:              s.opts.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// 上传大 PDF / 大图不能卡超时，但也不能无限挂着
		IdleTimeout: 120 * time.Second,
	}
	s.Core.Bus.Info("api", "监听 http://"+s.opts.Addr)
	return server.ListenAndServe()
}

func (s *Server) routes() {
	m := s.mux

	m.HandleFunc("GET /api/health", s.handleHealth)
	m.HandleFunc("GET /api/info", s.handleInfo)
	m.HandleFunc("GET /api/gpu", s.handleGPU)

	m.HandleFunc("GET /api/models", s.handleModels)
	m.HandleFunc("GET /api/models/{id}", s.handleModel)
	m.HandleFunc("POST /api/models/{id}/prepare", s.handlePrepare)
	m.HandleFunc("POST /api/models/{id}/activate", s.handleActivate)
	m.HandleFunc("POST /api/models/{id}/deactivate", s.handleDeactivate)

	m.HandleFunc("GET /api/env", s.handleEnv)
	m.HandleFunc("POST /api/env/install", s.handleEnvInstall)
	m.HandleFunc("POST /api/env/recreate", s.handleEnvRecreate)
	m.HandleFunc("POST /api/env/base", s.handleEnvBase)

	m.HandleFunc("GET /api/settings", s.handleSettings)
	m.HandleFunc("POST /api/settings", s.handleSaveSettings)
	m.HandleFunc("GET /api/layout", s.handleLayout)
	m.HandleFunc("POST /api/layout", s.handleSaveLayout)

	m.HandleFunc("POST /api/recognize", s.handleRecognize)
	m.HandleFunc("GET /api/jobs", s.handleJobs)
	m.HandleFunc("GET /api/jobs/{id}", s.handleJob)
	m.HandleFunc("POST /api/jobs/{id}/cancel", s.handleCancelJob)

	m.HandleFunc("GET /api/history", s.handleHistory)
	m.HandleFunc("GET /api/history/{id}", s.handleHistoryGet)
	m.HandleFunc("DELETE /api/history/{id}", s.handleHistoryDelete)
	m.HandleFunc("GET /api/history/{id}/files/{name}", s.handleHistoryFile)
	m.HandleFunc("GET /api/history/{id}/export.xlsx", s.handleHistoryXLSX)

	m.HandleFunc("GET /api/events", s.handleEvents)
	m.HandleFunc("GET /api/logs", s.handleLogs)

	if s.opts.WebDir != "" {
		m.Handle("/", s.spaHandler())
	}
}

// ---------------------------------------------------------------- handler 小工具

func (s *Server) auth(next http.Handler) http.Handler {
	token := strings.TrimSpace(s.opts.Token)
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/health" {
			header := r.Header.Get("Authorization")
			provided := strings.TrimSpace(strings.TrimPrefix(header, "Bearer"))
			if provided == "" {
				provided = r.URL.Query().Get("token")
			}
			if provided != token {
				writeError(w, http.StatusUnauthorized, "需要访问口令")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SSE 是长连接，别给它记「慢请求」
		if r.URL.Path == "/api/events" {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		if recorder.status >= 500 {
			s.Core.Bus.Error("api", fmt.Sprintf("%s %s → %d (%s)", r.Method, r.URL.Path, recorder.status, time.Since(started).Round(time.Millisecond)))
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": message})
}

func writeOK(w http.ResponseWriter, payload any) {
	body := map[string]any{"ok": true}
	marshaled, err := json.Marshal(payload)
	if err == nil {
		_ = json.Unmarshal(marshaled, &body)
	}
	writeJSON(w, http.StatusOK, body)
}

func decodeJSON(r *http.Request, target any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	return json.Unmarshal(body, target)
}

// ---------------------------------------------------------------- 基础信息

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// pid + uptime 是给桌面壳看的：进程比 bin/ocr-server 还老，就说明跑的是旧二进制。
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "ocr-server",
		"version": s.Core.VersionString(),
		"uptime":  int64(time.Since(s.Core.Started).Seconds()),
		"pid":     os.Getpid(),
	})
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	dirs := map[string]string{}
	for _, pair := range s.Core.Paths.Describe() {
		dirs[pair[0]] = pair[1]
	}
	settings := s.Core.Settings()
	env := s.Core.EnvStatus()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"version":   s.Core.VersionString(),
		"root":      s.Core.Root(),
		"dirs":      dirs,
		"active":    s.Core.ActiveModel(),
		"settings":  settings,
		"env":       env,
		"op":        s.Core.CurrentOp(),
		"gpu":       s.Core.GPU,
		"memory":    s.Core.Memory,
		"models":    len(s.Core.Catalog.All()),
		"visible":   len(s.Core.Catalog.Visible()),
		"sources":   s.Core.Catalog.Sources(),
		"tesseract": tesseractPath(),
		"docs":      docsLinks(),
	})
}

func (s *Server) handleGPU(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("refresh") == "1" {
		// 重新检测顺带把内存也刷一遍：模型卡上的「内存不够」跟着更新
		s.Core.Memory = hardware.RefreshMemory()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "gpu": gpu.Refresh(), "memory": s.Core.Memory})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "gpu": gpu.Detect(), "memory": hardware.DetectMemory()})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	refresh := r.URL.Query().Get("refresh") == "1"
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"models": s.Core.Models(refresh),
	})
}

func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	model, ok := s.Core.Model(r.PathValue("id"), r.URL.Query().Get("refresh") == "1")
	if !ok {
		writeError(w, http.StatusNotFound, "未知模型")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "model": model})
}

// ---------------------------------------------------------------- 模型操作

func (s *Server) handlePrepare(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	spec, ok := s.Core.Spec(id)
	if !ok {
		writeError(w, http.StatusNotFound, "未知模型")
		return
	}
	force := r.URL.Query().Get("force") == "1"
	if summary := s.Core.ReasonText(spec); summary != "" {
		writeError(w, http.StatusConflict, "无法准备「"+spec.Name+"」："+summary)
		return
	}

	// 真正干活在后台：HTTP 立刻返回 202，已经在跑就回 409（别让人叠一堆下载）。
	if err := s.Core.StartPrepare(id, force); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "started": true, "model": id})
}

func (s *Server) handleActivate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	go func() {
		if err := s.Core.Activate(id); err != nil {
			s.Core.Bus.Error(id, "启用失败："+err.Error())
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "started": true, "model": id})
}

func (s *Server) handleDeactivate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Core.Deactivate(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]any{"model": id, "active": s.Core.ActiveModel()})
}

// ---------------------------------------------------------------- python 环境

func (s *Server) handleEnv(w http.ResponseWriter, r *http.Request) {
	// 起 python 子进程有成本：默认给缓存，用户点「重新检测」才用 ?refresh=1 真探一遍。
	env := s.Core.EnvStatus()
	if r.URL.Query().Get("refresh") == "1" {
		env = s.Core.EnvStatusFresh()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"env":      env,
		"python":   s.Core.Env.VenvPython(),
		"venv":     s.Core.Env.VenvDir(),
		"base":     s.Core.Env.Base(),
		"detected": s.Core.Env.DetectSystemPython(),
	})
}

func (s *Server) handleEnvInstall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Extras []string `json:"extras"`
	}
	_ = decodeJSON(r, &body)
	if err := s.Core.StartEnvInstall(body.Extras); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "started": true})
}

func (s *Server) handleEnvRecreate(w http.ResponseWriter, r *http.Request) {
	if err := s.Core.StartEnvRecreate(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "started": true})
}

func (s *Server) handleEnvBase(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Base *string `json:"base"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	s.Core.Env.SetBase(body.Base)
	writeOK(w, map[string]any{"base": s.Core.Env.Base(), "detected": s.Core.Env.DetectSystemPython()})
}

// ---------------------------------------------------------------- 设置 / 目录

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": s.Core.Settings()})
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var patch core.Settings
	if err := decodeJSON(r, &patch); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	next, err := s.Core.UpdateSettings(patch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]any{"settings": next})
}

func (s *Server) handleLayout(w http.ResponseWriter, r *http.Request) {
	layout := s.Core.Paths.Layout()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"layout":  layout,
		"current": dirsMap(s.Core.Paths),
	})
}

func (s *Server) handleSaveLayout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ModelsDir *string `json:"models_dir"`
		OutDir    *string `json:"out_dir"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	layout, err := s.Core.SetLayout(paths.Layout{ModelsDir: body.ModelsDir, OutDir: body.OutDir})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOK(w, map[string]any{"layout": layout, "current": dirsMap(s.Core.Paths)})
}

func dirsMap(p *paths.Paths) map[string]string {
	out := map[string]string{}
	for _, pair := range p.Describe() {
		out[pair[0]] = pair[1]
	}
	return out
}

// ---------------------------------------------------------------- 识别

func (s *Server) handleRecognize(w http.ResponseWriter, r *http.Request) {
	model := r.FormValue("model")
	options := map[string]any{}
	if raw := r.FormValue("options"); strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &options); err != nil {
			writeError(w, http.StatusBadRequest, "options 不是合法 JSON")
			return
		}
	}

	var inputs []core.Input
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "解析上传失败："+err.Error())
			return
		}
		files := r.MultipartForm.File["files"]
		if len(files) == 0 {
			files = r.MultipartForm.File["file"]
		}
		if len(files) == 0 {
			writeError(w, http.StatusBadRequest, "没有上传文件")
			return
		}
		tempDir, err := os.MkdirTemp("", "ocr-upload-")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer os.RemoveAll(tempDir)

		for _, header := range files {
			source, err := header.Open()
			if err != nil {
				writeError(w, http.StatusBadRequest, "读取上传文件失败："+err.Error())
				return
			}
			name := filepath.Base(header.Filename)
			target := filepath.Join(tempDir, name)
			dest, err := os.Create(target)
			if err != nil {
				source.Close()
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			written, copyErr := io.Copy(dest, source)
			dest.Close()
			source.Close()
			if copyErr != nil {
				writeError(w, http.StatusInternalServerError, copyErr.Error())
				return
			}
			inputs = append(inputs, core.Input{Path: target, Name: name, Size: written})
		}
	} else {
		var body struct {
			Model   string         `json:"model"`
			Paths   []string       `json:"paths"`
			Options map[string]any `json:"options"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
			return
		}
		model = firstNonEmpty(model, body.Model)
		if body.Options != nil {
			for key, value := range body.Options {
				options[key] = value
			}
		}
		for _, path := range body.Paths {
			input, err := core.SaveUpload(path, filepath.Base(path))
			if err != nil {
				writeError(w, http.StatusBadRequest, "读不到文件 "+path+"："+err.Error())
				return
			}
			inputs = append(inputs, input)
		}
	}

	if model == "" {
		model = s.Core.Settings().DefaultModel
	}
	if model == "" {
		writeError(w, http.StatusBadRequest, "没指定模型")
		return
	}

	job, err := s.Core.StartRecognize(core.RecognizeRequest{Model: model, Inputs: inputs, Options: options})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "job": job})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "jobs": s.Core.Jobs(limit)})
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	job, ok := s.Core.Job(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "未知任务")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": job})
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	if err := s.Core.CancelJob(r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOK(w, map[string]any{"withdraw": true})
}

// ---------------------------------------------------------------- 历史

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": s.Core.HistoryList(limit)})
}

func (s *Server) handleHistoryGet(w http.ResponseWriter, r *http.Request) {
	entry, result, err := s.Core.HistoryGet(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "entry": entry, "result": result})
}

func (s *Server) handleHistoryDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Core.HistoryDelete(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeOK(w, map[string]any{"deleted": true})
}

func (s *Server) handleHistoryFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.PathValue("name")
	file, err := s.Core.HistoryFile(id, name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	download := r.URL.Query().Get("download") == "1"
	serveFile(w, r, file, download)
}

// handleHistoryXLSX 把一次识别导出成 Excel（分块表 + 全文表）。
func (s *Server) handleHistoryXLSX(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	data, err := s.Core.ExportXLSX(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	// 一律按附件下：浏览器点和桌面壳拉的都是它
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "ocr-"+id+".xlsx"))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if _, err := w.Write(data); err != nil {
		return
	}
}

func serveFile(w http.ResponseWriter, r *http.Request, file string, download bool) {
	info, err := os.Stat(file)
	if err != nil {
		writeError(w, http.StatusNotFound, "文件不存在")
		return
	}
	if kind := mime.TypeByExtension(filepath.Ext(file)); kind != "" {
		w.Header().Set("Content-Type", kind)
	}
	if download {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(file)))
	}
	http.ServeContent(w, r, filepath.Base(file), info.ModTime(), mustOpen(file))
}

func mustOpen(file string) *os.File {
	fh, err := os.Open(file)
	if err != nil {
		return nil
	}
	return fh
}

// ---------------------------------------------------------------- 事件流

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "这个客户端不支持 SSE")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// 先补一段历史，前端刷新后不至于空白
	if r.URL.Query().Get("replay") != "0" {
		for _, ev := range s.Core.Bus.Recent(120) {
			writeSSE(w, ev)
		}
		flusher.Flush()
	}

	ch, cancel := s.Core.Bus.Subscribe()
	defer cancel()

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = io.WriteString(w, ": ping\n\n")
			flusher.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			writeSSE(w, ev)
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, ev events.Event) {
	raw, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_, _ = io.WriteString(w, "data: ")
	_, _ = w.Write(raw)
	_, _ = io.WriteString(w, "\n\n")
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	count, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if count <= 0 {
		count = 200
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "events": s.Core.Bus.Recent(count)})
}

// ---------------------------------------------------------------- 静态资源

// spaHandler 托管 web/dist，并把非静态路径回落到 index.html（前端路由）。
func (s *Server) spaHandler() http.Handler {
	root := s.opts.WebDir
	files := http.FileServer(http.Dir(root))
	index := filepath.Join(root, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if clean == "." {
			clean = "index.html"
		}
		target := filepath.Join(root, filepath.FromSlash(clean))
		if paths.IsInsideDir(root, target) {
			if info, err := os.Stat(target); err == nil && !info.IsDir() {
				// 打包产物带 hash，可以长缓存；index.html 必须每次回源
				if clean == "index.html" {
					w.Header().Set("Cache-Control", "no-cache")
				} else {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, index)
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func tesseractPath() string {
	if path, err := exec.LookPath("tesseract"); err == nil {
		return path
	}
	return ""
}

func docsLinks() map[string]string {
	return map[string]string{
		"rapidocr":      "https://github.com/RapidAI/RapidOCR",
		"paddleocr":     "https://github.com/PaddlePaddle/PaddleOCR",
		"tesseract":     "https://github.com/tesseract-ocr/tesseract",
		"mineru":        "https://github.com/opendatalab/MinerU",
		"surya":         "https://github.com/datalab-to/surya",
		"huggingface":   "https://huggingface.co",
		"models_toml":   "config/models.toml",
		"python_worker": "python/ocr_worker",
	}
}
