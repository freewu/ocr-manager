package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// 后端进程：能连上就以「开发模式」复用现成的 ocr-server，连不上就自己拉起一个。

type serverProc struct {
	cmd *exec.Cmd
	out *tailWriter
}

var (
	procMu  sync.Mutex
	running *serverProc
)

const defaultPort = "8899"

// apiBase 返回后端地址：OCR_API 优先，否则按 OCR_PORT 拼本地地址。
func apiBase() string {
	if v := strings.TrimSpace(os.Getenv("OCR_API")); v != "" {
		return strings.TrimRight(v, "/")
	}
	port := strings.TrimSpace(os.Getenv("OCR_PORT"))
	if port == "" {
		port = defaultPort
	}
	return "http://127.0.0.1:" + port
}

func apiPort() string {
	if i := strings.LastIndex(apiBase(), ":"); i >= 0 && i+1 < len(apiBase()) {
		return apiBase()[i+1:]
	}
	return defaultPort
}

func exeName() string {
	if runtime.GOOS == "windows" {
		return "ocr-server.exe"
	}
	return "ocr-server"
}

// repoRoot 从可执行文件与当前目录向上找仓库根（config/models.toml 所在处）。
func repoRoot() string {
	var starts []string
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	for _, start := range starts {
		dir := start
		for i := 0; i < 6; i++ {
			if _, err := os.Stat(filepath.Join(dir, "config", "models.toml")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return ""
}

// findServer 找出 ocr-server 可执行文件。
func findServer() (string, bool) {
	if v := strings.TrimSpace(os.Getenv("OCR_SERVER")); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v, true
		}
	}
	name := exeName()
	var candidates []string
	if root := repoRoot(); root != "" {
		// bin/ 是打包脚本的输出目录
		candidates = append(candidates,
			filepath.Join(root, "bin", name),
			filepath.Join(root, "target", "release", name),
		)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), name))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, name))
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c, true
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	return "", false
}

// tailWriter 只保留最后一段输出，启动失败时好把原因给用户看。
type tailWriter struct {
	mu   sync.Mutex
	data []byte
	keep int
}

func newTailWriter(keep int) *tailWriter { return &tailWriter{keep: keep} }

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.data = append(w.data, p...)
	if len(w.data) > w.keep {
		w.data = w.data[len(w.data)-w.keep:]
	}
	return len(p), nil
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.TrimSpace(string(w.data))
}

// staleServer 判断端口上跑的是不是「比 bin/ocr-server 还老」的进程。
//
// 进程启动时间 = 现在 - uptime；二进制 mtime 比它还新，说明这个进程是
// 用旧二进制跑起来的（重新编译之后没重启），此时接口可能已经对不上了。
func staleServer(h healthInfo, bin string) bool {
	if h.Uptime <= 0 || bin == "" {
		return false
	}
	info, err := os.Stat(bin)
	if err != nil || info.IsDir() {
		return false
	}
	started := time.Now().Add(-time.Duration(h.Uptime) * time.Second)
	// 留 3s 余量：二进制刚落盘、进程刚起来时别误判成旧进程
	return info.ModTime().After(started.Add(3 * time.Second))
}

// stopPID 结束一个不归自己管的 ocr-server（端口上残留的旧进程）。
func stopPID(pid int) error {
	if pid <= 0 {
		return nil
	}
	if runtime.GOOS == "windows" {
		// 进程树一起收，否则它拉起的 python worker 会留在后台。
		kill := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprint(pid))
		kill.SysProcAttr = hiddenProcAttr()
		out, err := kill.CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return errors.New(msg)
		}
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// waitGone 等端口上的后端彻底退出（最多 10s，够它收掉 worker）。
func waitGone(ctx context.Context, cl *client) bool {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, alive := cl.health(ctx); !alive {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(200 * time.Millisecond):
		}
	}
	return false
}

// startServer 启动 ocr-server 并等它健康检查通过。
//
// 端口上已经有 ocr-server 就复用它（开发模式下后端由 just dev 托管）；
// 如果那个进程比 bin/ocr-server 还老，说明是上一次开发留下的旧二进制，
// 直接结束掉重拉，免得界面跟后端对不上接口。
func startServer(ctx context.Context, cl *client, onStatus func(string)) error {
	// 测试或「后端由别的终端托管」时不要自己拉进程
	if os.Getenv("OCR_NO_SPAWN") != "" {
		return errors.New("已设置 OCR_NO_SPAWN，不自动启动后端服务")
	}
	bin, ok := findServer()
	if h, alive := cl.health(ctx); alive {
		// 端口上是一台现成的、跟二进制同期的 ocr-server：复用（开发模式）
		if !staleServer(h, bin) {
			return nil
		}
		if h.PID <= 0 {
			return fmt.Errorf("端口 %s 上跑着一个旧的后端（版本 %s，已运行 %ds），但它不支持自动重启：请先结束它，再点「重连」",
				apiPort(), h.Version, h.Uptime)
		}
		if onStatus != nil {
			onStatus(fmt.Sprintf("发现旧的后端进程（pid %d），正在重启…", h.PID))
		}
		if err := stopPID(h.PID); err != nil {
			return fmt.Errorf("结束旧的后端进程（pid %d）失败：%w", h.PID, err)
		}
		if !waitGone(ctx, cl) {
			return fmt.Errorf("旧的后端进程（pid %d）没有退出，请到「任务管理器」结束它后重试", h.PID)
		}
	}
	if !ok {
		return errors.New("没找到后端程序 ocr-server：请重新安装本应用，或用 OCR_SERVER 指定它的路径")
	}
	cmd := exec.Command(bin, "-addr", "127.0.0.1:"+apiPort())
	cmd.SysProcAttr = hiddenProcAttr() // 后端是控制台程序，别让它闪黑框
	if root := repoRoot(); root != "" {
		cmd.Dir = root
	}
	out := newTailWriter(8 << 10)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 %s 失败：%w", bin, err)
	}
	procMu.Lock()
	running = &serverProc{cmd: cmd, out: out}
	procMu.Unlock()

	deadline := time.Now().Add(3 * time.Minute)
	started := time.Now()
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			stopServer()
			return ctx.Err()
		}
		if cl.ping(ctx) {
			return nil
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return fmt.Errorf("ocr-server 退出了：%s", out.String())
		}
		if onStatus != nil {
			onStatus(fmt.Sprintf("正在启动后端… %ds", int(time.Since(started).Seconds())))
		}
		select {
		case <-ctx.Done():
			stopServer()
			return ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
	stopServer()
	tail := out.String()
	if tail == "" {
		tail = "（没有输出）"
	}
	return fmt.Errorf("后端启动超时：%s", tail)
}

// stopServer 结束由本进程拉起的后端（连同它的 python worker）。
func stopServer() {
	procMu.Lock()
	proc := running
	running = nil
	procMu.Unlock()
	if proc == nil || proc.cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		// 进程树要一起收掉，否则 python worker 会留在后台。
		kill := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprint(proc.cmd.Process.Pid))
		kill.SysProcAttr = hiddenProcAttr()
		_ = kill.Run()
	} else {
		_ = proc.cmd.Process.Kill()
	}
	_, _ = proc.cmd.Process.Wait()
}
