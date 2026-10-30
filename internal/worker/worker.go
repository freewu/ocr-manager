// Package worker 管理 python worker 子进程与 JSONL 协议。
//
// 协议（每行一个 JSON）：
//
//	请求  {"id":"1","cmd":"load"}
//	     {"id":"2","cmd":"prepare"}
//	     {"id":"3","cmd":"recognize","file":"/abs/a.png","options":{...}}
//	回复  {"id":"1","type":"result","ok":true,"loaded":true}
//	     {"id":"2","type":"error","message":"..."}
//	异步  {"type":"log","level":"info","message":"..."}
//	     {"id":"3","type":"progress","stage":"ocr","percent":42.0,"message":"..."}
package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bluefrog/ocr-manager/internal/events"
	"github.com/bluefrog/ocr-manager/internal/models"
	"github.com/bluefrog/ocr-manager/internal/pyenv"
)

// Progress 是 worker 上报的一条进度。
type Progress struct {
	Stage   string
	Percent *float64
	Message string
	Bytes   *events.ProgressBytes
}

// Callbacks 是一次请求的异步回调（在读取 goroutine 上同步调用，别做重活）。
type Callbacks struct {
	Progress func(Progress)
	Log      func(level, message string)
}

// Error 是 worker 返回的业务错误（引擎缺依赖、图片打不开这类）。
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

type pending struct {
	id  string
	ch  chan map[string]any
	cb  *Callbacks
	cmd string
}

// Worker 是一个运行中的 worker 进程。
type Worker struct {
	ModelID string
	Engine  string
	PID     int

	stdin   io.WriteCloser
	stdinMu sync.Mutex

	// callMu 让同一个 worker 上的请求串行：引擎实例不是线程安全的。
	callMu sync.Mutex

	proc   *exec.Cmd
	procMu sync.Mutex

	pending   map[string]*pending
	pendingMu sync.Mutex

	ev  *events.EventBus
	seq atomic.Uint64

	exited chan struct{}
	once   sync.Once
	ready  atomic.Bool
}

// Spawn 启动 worker 子进程。
func Spawn(spec models.ModelSpec, env *pyenv.Env, modelsDir, hfEndpoint string, bus *events.EventBus) (*Worker, error) {
	if !pyenv.FileExists(env.VenvPython()) {
		return nil, fmt.Errorf("python 环境未就绪：%s 不存在（先在「设置 → Python 环境」点「安装依赖」）", env.VenvPython())
	}
	cmd := env.WorkerCommand(spec, modelsDir, hfEndpoint)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("无法获取 worker stdin: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("无法获取 worker stdout: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("无法获取 worker stderr: %w", err)
	}

	bus.Info(spec.ID, "启动 worker："+spec.ID)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 worker 失败（%s）: %w", env.VenvPython(), err)
	}

	w := &Worker{
		ModelID: spec.ID,
		Engine:  spec.Engine,
		PID:     cmd.Process.Pid,
		stdin:   stdinPipe,
		proc:    cmd,
		pending: map[string]*pending{},
		ev:      bus,
		exited:  make(chan struct{}),
	}
	w.seq.Store(1)

	go w.readProtocol(stdoutPipe)
	go w.readStderr(stderrPipe)
	go func() {
		_ = cmd.Wait()
		w.markExited()
	}()

	return w, nil
}

// Alive 表示进程还活着。
func (w *Worker) Alive() bool {
	select {
	case <-w.exited:
		return false
	default:
		return true
	}
}

// Ready 表示 `load` 成功过。
func (w *Worker) Ready() bool { return w.ready.Load() }

func (w *Worker) readProtocol(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		w.handleLine(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		w.ev.Warn(w.ModelID, "读取 worker stdout 出错: "+err.Error())
	}
}

func (w *Worker) readStderr(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		w.ev.Log(w.ModelID, "stderr", line)
	}
}

func (w *Worker) markExited() {
	w.once.Do(func() {
		w.ready.Store(false)
		message := "worker 已退出"
		w.ev.Status(w.ModelID, "idle", &message)

		w.pendingMu.Lock()
		waiting := w.pending
		w.pending = map[string]*pending{}
		w.pendingMu.Unlock()
		for _, item := range waiting {
			select {
			case item.ch <- map[string]any{"type": "error", "message": "worker 已退出"}:
			default:
			}
		}
		close(w.exited)
	})
}

func (w *Worker) handleLine(line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		// 不是协议行就是第三方库漏到 stdout 的日志，当 stderr 处理
		w.ev.Log(w.ModelID, "stdout", trimmed)
		return
	}

	kind, _ := value["type"].(string)
	id, _ := value["id"].(string)

	switch kind {
	case "result", "error":
		w.pendingMu.Lock()
		item := w.pending[id]
		delete(w.pending, id)
		w.pendingMu.Unlock()
		if item == nil {
			return
		}
		select {
		case item.ch <- value:
		default:
		}
	case "progress":
		progress := Progress{Stage: stringOf(value["stage"]), Message: stringOf(value["message"])}
		if percent, ok := number(value["percent"]); ok {
			progress.Percent = &percent
		}
		if bytes, ok := value["bytes"].(map[string]any); ok {
			progress.Bytes = &events.ProgressBytes{
				Done:       uint64Of(bytes["done"]),
				Total:      uint64Of(bytes["total"]),
				FilesDone:  uint32Of(bytes["files_done"]),
				FilesTotal: uint32Of(bytes["files_total"]),
			}
		}
		if cb := w.callbacks(id); cb != nil && cb.Progress != nil {
			cb.Progress(progress)
			return
		}
		w.ev.ProgressBytes(w.ModelID, progress.Stage, progress.Percent, progress.Message, progress.Bytes)
	case "log":
		level := stringOf(value["level"])
		if level == "" {
			level = "info"
		}
		message := stringOf(value["message"])
		if cb := w.callbacks(id); cb != nil && cb.Log != nil {
			cb.Log(level, message)
		}
		w.ev.Log(w.ModelID, level, message)
	default:
		w.ev.Log(w.ModelID, "stdout", trimmed)
	}
}

func (w *Worker) callbacks(id string) *Callbacks {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	if item, ok := w.pending[id]; ok {
		return item.cb
	}
	return nil
}

// Call 发一条请求并等回复。同一个 worker 上的调用是串行的。
func (w *Worker) Call(ctx context.Context, cmd string, args map[string]any, cb *Callbacks) (map[string]any, error) {
	w.callMu.Lock()
	defer w.callMu.Unlock()

	if !w.Alive() {
		return nil, fmt.Errorf("worker 已退出")
	}

	id := strconv.FormatUint(w.seq.Add(1), 10)
	body := map[string]any{"id": id, "cmd": cmd}
	for key, value := range args {
		body[key] = value
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	item := &pending{id: id, ch: make(chan map[string]any, 1), cb: cb, cmd: cmd}
	w.pendingMu.Lock()
	w.pending[id] = item
	w.pendingMu.Unlock()
	defer func() {
		w.pendingMu.Lock()
		delete(w.pending, id)
		w.pendingMu.Unlock()
	}()

	w.stdinMu.Lock()
	_, writeErr := w.stdin.Write(append(raw, '\n'))
	w.stdinMu.Unlock()
	if writeErr != nil {
		return nil, fmt.Errorf("写入 worker 失败: %w", writeErr)
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("%s 超时（%s）", cmd, ctx.Err())
	case value := <-item.ch:
		if kind, _ := value["type"].(string); kind == "error" {
			return nil, &Error{Message: stringOf(value["message"])}
		}
		if ok, exists := value["ok"].(bool); exists && !ok {
			return nil, &Error{Message: stringOf(value["message"])}
		}
		return value, nil
	}
}

// Load 让 worker 把模型读进内存。
func (w *Worker) Load(ctx context.Context, cb *Callbacks) error {
	if _, err := w.Call(ctx, "load", nil, cb); err != nil {
		return err
	}
	w.ready.Store(true)
	return nil
}

// Shutdown 优雅退出（超时就 kill）。
func (w *Worker) Shutdown(timeout time.Duration) {
	if !w.Alive() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, _ = w.Call(ctx, "shutdown", nil, nil)

	select {
	case <-w.exited:
		return
	case <-time.After(timeout):
	}
	w.Kill()
}

// Kill 强杀进程。
func (w *Worker) Kill() {
	w.procMu.Lock()
	defer w.procMu.Unlock()
	if w.proc != nil && w.proc.Process != nil {
		_ = w.proc.Process.Kill()
	}
}

func stringOf(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		raw, _ := json.Marshal(typed)
		return string(raw)
	}
}

func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		out, err := typed.Float64()
		return out, err == nil
	}
	return 0, false
}

func uint64Of(value any) uint64 {
	if out, ok := number(value); ok && out > 0 {
		return uint64(out)
	}
	return 0
}

func uint32Of(value any) uint32 {
	if out, ok := number(value); ok && out > 0 {
		return uint32(out)
	}
	return 0
}
