// Package paths 管理运行时目录布局（数据目录 / 权重缓存 / venv / 识别产物）。
//
// 根目录优先级：`OCR_HOME` > 系统数据目录 > 当前目录下的 `.runtime`。
//
//	<root>/
//	  config/models.toml     用户模型清单覆盖
//	  config/layout.json     权重目录 / 产物目录的覆盖（「设置」页写的）
//	  config/python.toml     基础解释器选择
//	  runtime/python/        内置的 python worker 源码（启动时解包）
//	  venv/                  python 虚拟环境
//	  models/                权重缓存（HF_HOME 指向这里）—— 可在设置里改
//	  out/<job-id>/          每次识别的输入副本 + 结果产物 —— 可在设置里改
//	  logs/
package paths

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Layout 是用户可以改的目录布局（「设置」页）。
//
// 两项目录都是可选的：`nil` 表示用默认值。改 `ModelsDir` 等于换一个权重仓库，
// 旧目录里的权重和 `ready.json` 不会跟过来 —— 换回来才又看得到。
type Layout struct {
	// ModelsDir 是权重缓存目录（同时作为 `HF_HOME`）。
	ModelsDir *string `json:"models_dir"`
	// OutDir 是识别产物目录。
	OutDir *string `json:"out_dir"`
}

// Normalized 把空字符串当成「没设置」。
func (l Layout) Normalized() Layout {
	trim := func(p *string) *string {
		if p == nil {
			return nil
		}
		if strings.TrimSpace(*p) == "" {
			return nil
		}
		value := *p
		return &value
	}
	return Layout{ModelsDir: trim(l.ModelsDir), OutDir: trim(l.OutDir)}
}

// Empty 表示没有覆盖任何默认目录。
func (l Layout) Empty() bool { return l.ModelsDir == nil && l.OutDir == nil }

// Paths 是一次进程内的目录布局（`Layout` 可被设置页热改）。
type Paths struct {
	Root string

	mu     sync.RWMutex
	layout Layout
}

// Resolve 按环境变量 / 系统目录推导根目录。
func Resolve() (*Paths, error) {
	root := strings.TrimSpace(os.Getenv("OCR_HOME"))
	if root == "" {
		if dir := dataDir(); dir != "" {
			root = filepath.Join(dir, "ocr-manager")
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return nil, fmt.Errorf("无法确定当前目录: %w", err)
			}
			root = filepath.Join(cwd, ".runtime")
		}
	}
	return WithRoot(root)
}

// WithRoot 用指定根目录建布局（目录不存在会被创建）。
func WithRoot(root string) (*Paths, error) {
	p := &Paths{Root: root, layout: loadLayout(root)}
	if err := p.Ensure(); err != nil {
		return nil, err
	}
	return p, nil
}

// dataDir 返回系统数据目录：Windows `%APPDATA%`，macOS `~/Library/Application Support`，
// Linux `$XDG_DATA_HOME` 或 `~/.local/share`。
func dataDir() string {
	switch runtime.GOOS {
	case "windows", "darwin":
		if dir, err := os.UserConfigDir(); err == nil && dir != "" {
			return dir
		}
		return ""
	default:
		if dir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dir != "" {
			return dir
		}
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		return filepath.Join(home, ".local", "share")
	}
}

func loadLayout(root string) Layout {
	raw, err := os.ReadFile(filepath.Join(root, "config", "layout.json"))
	if err != nil {
		return Layout{}
	}
	var layout Layout
	if err := json.Unmarshal(raw, &layout); err != nil {
		return Layout{}
	}
	return layout.Normalized()
}

// Layout 返回当前布局。
func (p *Paths) Layout() Layout {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.layout
}

// SetLayout 写入新的目录布局并落盘。目录不存在会被创建，创建不了就整笔失败。
func (p *Paths) SetLayout(layout Layout) (Layout, error) {
	layout = layout.Normalized()
	checks := []struct {
		label string
		dir   *string
	}{
		{"权重目录", layout.ModelsDir},
		{"产物目录", layout.OutDir},
	}
	for _, check := range checks {
		if check.dir == nil {
			continue
		}
		if !filepath.IsAbs(*check.dir) {
			return Layout{}, fmt.Errorf("%s必须写绝对路径：%s", check.label, *check.dir)
		}
	}

	file := p.LayoutFile()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return Layout{}, fmt.Errorf("创建目录失败: %s: %w", filepath.Dir(file), err)
	}
	raw, err := json.MarshalIndent(layout, "", "  ")
	if err != nil {
		return Layout{}, err
	}
	if err := os.WriteFile(file, raw, 0o644); err != nil {
		return Layout{}, fmt.Errorf("写入失败: %s: %w", file, err)
	}

	p.mu.Lock()
	p.layout = layout
	p.mu.Unlock()

	// 换了目录之后要立刻能用：建目录顺便验证路径可写
	if err := p.Ensure(); err != nil {
		return Layout{}, err
	}
	return layout, nil
}

// LayoutFile 是覆盖配置的落盘位置。
func (p *Paths) LayoutFile() string { return filepath.Join(p.Root, "config", "layout.json") }

// DefaultModelsDir 是权重缓存的默认位置。
func (p *Paths) DefaultModelsDir() string { return filepath.Join(p.Root, "models") }

// DefaultOutDir 是识别产物目录的默认位置。
func (p *Paths) DefaultOutDir() string { return filepath.Join(p.Root, "out") }

// Ensure 建好所有运行期目录。
func (p *Paths) Ensure() error {
	dirs := []string{
		p.Root,
		p.RuntimeDir(),
		p.PythonDir(),
		p.VenvDir(),
		p.ModelsDir(),
		p.OutDir(),
		p.LogsDir(),
		filepath.Join(p.Root, "config"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建目录失败: %s: %w", dir, err)
		}
	}
	return nil
}

// RuntimeDir 是运行时解包目录。
func (p *Paths) RuntimeDir() string { return filepath.Join(p.Root, "runtime") }

// PythonDir 是内置 python worker 源码解包位置。
func (p *Paths) PythonDir() string { return filepath.Join(p.RuntimeDir(), "python") }

// VenvDir 是 python 虚拟环境。
func (p *Paths) VenvDir() string { return filepath.Join(p.Root, "venv") }

// PythonSettingsFile 记录基础解释器选择（`base = "..."`）。
func (p *Paths) PythonSettingsFile() string { return filepath.Join(p.Root, "config", "python.toml") }

// ModelsDir 是权重缓存，同时作为 `HF_HOME`（设置页可改）。
func (p *Paths) ModelsDir() string {
	if dir := p.Layout().ModelsDir; dir != nil {
		return *dir
	}
	return p.DefaultModelsDir()
}

// HFHome 是 HuggingFace Hub 缓存根。
func (p *Paths) HFHome() string { return filepath.Join(p.ModelsDir(), "hub") }

// ModelStateDir 是每个模型的元数据目录（下载完成标记等），与权重缓存分开。
func (p *Paths) ModelStateDir(modelID string) string {
	return filepath.Join(p.ModelsDir(), "state", modelID)
}

// ModelReadyMarker 是权重下载完成的标记文件：只有它的存在才能激活模型。
func (p *Paths) ModelReadyMarker(modelID string) string {
	return filepath.Join(p.ModelStateDir(modelID), "ready.json")
}

// OutDir 是识别产物目录（设置页可改）。
func (p *Paths) OutDir() string {
	if dir := p.Layout().OutDir; dir != nil {
		return *dir
	}
	return p.DefaultOutDir()
}

// LogsDir 是日志目录。
func (p *Paths) LogsDir() string { return filepath.Join(p.Root, "logs") }

// ConfigFile 是用户清单覆盖文件。
func (p *Paths) ConfigFile() string { return filepath.Join(p.Root, "config", "models.toml") }

// DevConfigFile 是仓库内随源码分发的模型清单（开发态）。
func (p *Paths) DevConfigFile() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	file := filepath.Join(cwd, "config", "models.toml")
	if _, err := os.Stat(file); err != nil {
		return ""
	}
	return file
}

// JobDir 是一次识别的产物目录。
func (p *Paths) JobDir(jobID string) string { return filepath.Join(p.OutDir(), jobID) }

// NewJobID 生成一个按时间排序、带随机后缀的任务 id（也能直接当目录名用）。
func NewJobID() string {
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(buf)
}

// Describe 是给 UI 的目录清单（顺序稳定）。
func (p *Paths) Describe() [][2]string {
	return [][2]string{
		{"root", p.Root},
		{"python", p.PythonDir()},
		{"venv", p.VenvDir()},
		{"models", p.ModelsDir()},
		{"output", p.OutDir()},
		{"logs", p.LogsDir()},
	}
}

// IsInsideDir 判断路径是否在目录里（防目录穿越）。
func IsInsideDir(dir, path string) bool {
	absDir, err := filepath.Abs(dir)
	if err == nil {
		dir = absDir
	}
	if resolved, err := filepath.Abs(path); err == nil {
		path = resolved
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}
