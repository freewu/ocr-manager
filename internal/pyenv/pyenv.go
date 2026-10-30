// Package pyenv 管理 python 虚拟环境：基础解释器探测、venv 创建、按模型装 extras、
// 以及拼出 worker 的启动命令。
//
// 每个模型的依赖都装在**应用自己的 venv** 里（`$OCR_HOME/venv`），不污染系统 Python。
package pyenv

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bluefrog/ocr-manager/internal/events"
	"github.com/bluefrog/ocr-manager/internal/models"
)

// moduleProbe 是判断某个 extras 装没装的一组模块名（任一可用即可）。
type moduleProbe struct {
	Extra   string
	Modules []string
}

// 与 pyproject.toml 里的 optional-dependencies 一一对应。
var probes = []moduleProbe{
	{"rapidocr", []string{"rapidocr", "rapidocr_onnxruntime"}},
	{"tesseract", []string{"pytesseract"}},
	{"paddle", []string{"paddleocr"}},
	{"vlm", []string{"torch", "transformers"}},
}

// EnvStatus 是「python 环境」的整体状态。
type EnvStatus struct {
	State    string          `json:"state"` // missing | creating | installing | ready | error
	Message  string          `json:"message"`
	Base     *string         `json:"base"`
	VenvPath string          `json:"venv_python"`
	VenvUsed bool            `json:"venv_ready"`
	Extras   map[string]bool `json:"extras"`
	Probes   []string        `json:"probe_errors"`
	Updated  int64           `json:"updated_at"`
}

// Env 是一个 venv（基础解释器可能被用户在设置里换掉）。
type Env struct {
	pythonDir string
	venvDir   string

	base *string
}

// New 建一个 Env（不创建目录，首次 EnsureVenv 才建）。
func New(pythonDir, venvDir string) *Env {
	return &Env{pythonDir: pythonDir, venvDir: venvDir, base: loadBase(venvDir)}
}

// PythonDir 是 worker 源码目录。
func (e *Env) PythonDir() string { return e.pythonDir }

// VenvDir 是虚拟环境目录。
func (e *Env) VenvDir() string { return e.venvDir }

// VenvPython 是 venv 里的解释器绝对路径。
func (e *Env) VenvPython() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(e.venvDir, "Scripts", "python.exe")
	}
	return filepath.Join(e.venvDir, "bin", "python")
}

// VenvConfig 是 venv 的配置档（python -m venv 写的，记着 base 解释器与路径）。
func (e *Env) VenvConfig() string { return filepath.Join(e.venvDir, "pyvenv.cfg") }

// VenvReady 表示 venv 已经建好。
//
// 光有 python.exe 不算数：创建过程被打断时会留下一个「半截 venv」——
// 解释器拷过去了、pyvenv.cfg 还没写，pip 进去只会回一句
// "No pyvenv.cfg file"，看着像依赖装不上，其实是环境本身坏了。
func (e *Env) VenvReady() bool {
	return FileExists(e.VenvPython()) && FileExists(e.VenvConfig())
}

// Base 是当前选用的基础解释器（nil = 自动探测）。
func (e *Env) Base() *string { return e.base }

// SetBase 换基础解释器（nil 表示回到自动探测）。
func (e *Env) SetBase(base *string) {
	e.base = base
	if base == nil || strings.TrimSpace(*base) == "" {
		_ = os.Remove(filepath.Join(e.venvDir, ".ocr-base.txt"))
		return
	}
	_ = os.MkdirAll(e.venvDir, 0o755)
	_ = os.WriteFile(filepath.Join(e.venvDir, ".ocr-base.txt"), []byte(*base), 0o644)
}

func loadBase(venvDir string) *string {
	raw, err := os.ReadFile(filepath.Join(venvDir, ".ocr-base.txt"))
	if err != nil {
		return nil
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return nil
	}
	return &value
}

// DetectSystemPython 找系统里的解释器。
//
// 顺序：`OCR_PYTHON` > venv 记录的基础解释器 > PATH 里的 python3 / python。
func (e *Env) DetectSystemPython() *string {
	if value := strings.TrimSpace(os.Getenv("OCR_PYTHON")); value != "" {
		return &value
	}
	if e.base != nil && FileExists(*e.base) {
		return e.base
	}
	candidates := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		candidates = []string{"python", "py"}
	}
	for _, name := range candidates {
		if path, err := exec.LookPath(name); err == nil {
			return &path
		}
	}
	return nil
}

// EnsureVenv 建好 venv（已存在就直接返回）。
func (e *Env) EnsureVenv(bus *events.EventBus) error {
	if e.VenvReady() {
		// 基础解释器换过就重建：不然 venv 里还是老 python
		if recorded := e.VenvBase(); recorded != nil {
			if base := e.DetectSystemPython(); base != nil && !samePath(*recorded, *base) {
				bus.Warn("python", "基础解释器变了，正在重建 venv："+*base)
				return e.RecreateVenv(bus)
			}
		}
		return nil
	}
	if FileExists(e.VenvPython()) {
		// 半截 venv（创建过程被打断，缺 pyvenv.cfg）：先清掉再建一遍，
		// 不然 pip 会一直报 "No pyvenv.cfg file"。
		bus.Warn("python", "venv 不完整（缺 pyvenv.cfg），正在重建："+e.venvDir)
		if err := os.RemoveAll(e.venvDir); err != nil {
			bus.Warn("python", "旧 venv 删不干净（可能有进程占着），直接在上面重建："+err.Error())
		}
	}
	base := e.DetectSystemPython()
	if base == nil {
		return fmt.Errorf("找不到 python 解释器：请安装 Python 3.9+，或用 `OCR_PYTHON=` 指定路径")
	}

	bus.Env("installing", "正在创建 venv："+e.venvDir)
	if err := os.MkdirAll(filepath.Dir(e.venvDir), 0o755); err != nil {
		return err
	}
	cmd := exec.Command(*base, "-m", "venv", e.venvDir)
	ok, err := RunStreaming(cmd, bus, "python")
	if err != nil || !ok {
		return fmt.Errorf("创建 venv 失败（%s）：%w", *base, orErr(err, "退出码非 0"))
	}
	if !e.VenvReady() {
		return fmt.Errorf("创建 venv 后找不到解释器：%s", e.VenvPython())
	}
	_ = os.WriteFile(filepath.Join(e.venvDir, ".ocr-base.txt"), []byte(*base), 0o644)
	bus.Env("ready", "venv 就绪")
	return nil
}

// VenvBase 读 venv 记录的基础解释器。
func (e *Env) VenvBase() *string {
	raw, err := os.ReadFile(filepath.Join(e.venvDir, ".ocr-base.txt"))
	if err != nil {
		return nil
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return nil
	}
	return &value
}

// RecreateVenv 删掉旧 venv 重建。
func (e *Env) RecreateVenv(bus *events.EventBus) error {
	if err := os.RemoveAll(e.venvDir); err != nil {
		return fmt.Errorf("删除旧 venv 失败: %w", err)
	}
	return e.EnsureVenv(bus)
}

// Install 装 worker 包（含 extras）。extras 为空表示只装 worker 本身。
func (e *Env) Install(bus *events.EventBus, extras []string) error {
	if err := e.EnsureVenv(bus); err != nil {
		return err
	}
	if !e.VenvReady() {
		return fmt.Errorf("python 环境不完整（venv 缺 pyvenv.cfg）：去「设置 → Python 环境」点「重建 venv」")
	}
	target := e.pythonDir
	if len(extras) > 0 {
		target = fmt.Sprintf("%s[%s]", e.pythonDir, strings.Join(extras, ","))
	}
	args := []string{"-m", "pip", "install", "--disable-pip-version-check", "-e", target}
	bus.Env("installing", "安装 python 依赖："+target)
	cmd := e.Command(args...)
	ok, err := RunStreaming(cmd, bus, "pip")
	if err != nil || !ok {
		return fmt.Errorf("安装依赖失败：%w", orErr(err, "pip 退出码非 0（看日志里的 pip 输出）"))
	}
	bus.Env("ready", "python 依赖安装完成")
	return nil
}

// Command 用 venv 解释器跑一条命令。
func (e *Env) Command(args ...string) *exec.Cmd {
	cmd := exec.Command(e.VenvPython(), args...)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUNBUFFERED=1", "PYTHONUTF8=1")
	return cmd
}

// WorkerCommand 拼出 worker 启动命令（stdin/stdout 走协议，所以不接终端）。
func (e *Env) WorkerCommand(spec models.ModelSpec, modelsDir, hfEndpoint string) *exec.Cmd {
	options, _ := json.Marshal(spec.OptionsJSON())
	args := []string{
		"-m", "ocr_worker",
		"--model", spec.ID,
		"--engine", spec.Engine,
		"--repo", spec.Repo,
		"--options", string(options),
		"--models-dir", modelsDir,
	}
	if hfEndpoint != "" {
		args = append(args, "--hf-endpoint", hfEndpoint)
	}
	if truthy(os.Getenv("OCR_HF_OFFLINE")) {
		args = append(args, "--offline")
	}
	cmd := e.Command(args...)
	cmd.Dir = e.pythonDir
	env := append(cmd.Env,
		"PYTHONPATH="+e.pythonDir,
		"HF_HOME="+modelsDir,
		"HF_HUB_CACHE="+filepath.Join(modelsDir, "hub"),
		"HF_HUB_DISABLE_TELEMETRY=1",
		"PADDLE_PDX_CACHE_HOME="+filepath.Join(modelsDir, "paddlex"),
		"TOKENIZERS_PARALLELISM=false",
	)
	if hfEndpoint != "" {
		env = append(env, "HF_ENDPOINT="+hfEndpoint)
	}
	cmd.Env = env
	return cmd
}

// Status 汇总环境状态（探测模块时会起一个 python 子进程，调用方可以缓存）。
func (e *Env) Status() EnvStatus {
	status := EnvStatus{
		State:    "missing",
		Message:  "python 环境还没建好",
		VenvPath: e.VenvPython(),
		Extras:   map[string]bool{},
		Updated:  time.Now().UnixMilli(),
	}
	if base := e.DetectSystemPython(); base != nil {
		status.Base = base
	}
	if !e.VenvReady() {
		if FileExists(e.VenvPython()) {
			// 半截 venv：直接报能照做的提示，别让用户去读 pip 的英文错误
			status.State = "error"
			status.Message = "venv 不完整（缺 pyvenv.cfg）：去「设置 → Python 环境」点「重建 venv」"
		}
		return status
	}
	status.VenvUsed = true
	modules, err := e.probeModules()
	if err != nil {
		status.Probes = append(status.Probes, err.Error())
	}
	for _, probe := range probes {
		found := false
		for _, module := range probe.Modules {
			if contains(modules, module) {
				found = true
				break
			}
		}
		status.Extras[probe.Extra] = found
	}
	if _, err := exec.LookPath("tesseract"); err == nil {
		status.Extras["tesseract_binary"] = true
	}
	core := status.Extras["rapidocr"] || status.Extras["paddle"] || status.Extras["tesseract"] || status.Extras["vlm"]
	if core {
		status.State, status.Message = "ready", "python 环境就绪"
	} else {
		status.State, status.Message = "waiting", "venv 已建好，还没装引擎依赖（在「设置 → Python 环境」点「安装依赖」，或在模型卡上点「下载」）"
	}
	return status
}

func (e *Env) probeModules() ([]string, error) {
	script := "import importlib.util,json,sys;" +
		"mods=" + pyList(probeModules()) + ";" +
		"print(json.dumps([m for m in mods if importlib.util.find_spec(m) is not None]))"
	cmd := e.Command("-c", script)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("探测 python 模块失败: %w", err)
	}
	var modules []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &modules); err != nil {
		return nil, fmt.Errorf("解析模块探测结果失败: %w", err)
	}
	return modules, nil
}

// ForModel 返回这个模型需要的 extras（清单里的声明，缺省给一个空表）。
func ForModel(spec models.ModelSpec) []string {
	out := make([]string, 0, len(spec.Extras))
	for _, extra := range spec.Extras {
		if strings.TrimSpace(extra) != "" {
			out = append(out, strings.TrimSpace(extra))
		}
	}
	// external 只是个登记项，不装任何东西
	if spec.Engine == "external" {
		return nil
	}
	return out
}

// RunStreaming 跑一条命令，把输出逐行变成事件（长任务靠它看进度）。
// 返回命令是否成功退出。
func RunStreaming(cmd *exec.Cmd, bus *events.EventBus, source string) (bool, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return false, err
	}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	done := make(chan struct{}, 2)
	go func() { scanLines(stdout, func(line string) { bus.Log(source, "stdout", line) }); done <- struct{}{} }()
	go func() { scanLines(stderr, func(line string) { bus.Log(source, "stderr", line) }); done <- struct{}{} }()
	<-done
	<-done
	err = cmd.Wait()
	var exit *exec.ExitError
	if asExitError(err, &exit) {
		return false, nil
	}
	return err == nil, err
}

func scanLines(reader io.Reader, each func(string)) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line != "" {
			each(line)
		}
	}
}

func asExitError(err error, target **exec.ExitError) bool {
	if err == nil {
		return false
	}
	exit, ok := err.(*exec.ExitError)
	if ok {
		*target = exit
	}
	return ok
}

// FileExists 判断文件存在且不是目录。
func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	left, err1 := filepath.Abs(a)
	right, err2 := filepath.Abs(b)
	if err1 == nil && err2 == nil {
		return strings.EqualFold(left, right)
	}
	return a == b
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func orErr(err error, fallback string) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("%s", fallback)
}

func probeModules() []string {
	set := map[string]struct{}{}
	for _, probe := range probes {
		for _, module := range probe.Modules {
			set[module] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for module := range set {
		out = append(out, module)
	}
	return out
}

func pyList(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, fmt.Sprintf("%q", item))
	}
	return "[" + strings.Join(quoted, ",") + "]"
}
