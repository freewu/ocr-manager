// Package core 是服务端门面：把目录、模型清单、python 环境、worker、任务、历史串起来。
//
// api / desktop / cli 都只依赖这一层，不认识 python 协议细节。
package core

import (
	"fmt"
	"sync"
	"time"

	ocr "github.com/bluefrog/ocr-manager"
	"github.com/bluefrog/ocr-manager/internal/events"
	"github.com/bluefrog/ocr-manager/internal/gpu"
	"github.com/bluefrog/ocr-manager/internal/hub"
	"github.com/bluefrog/ocr-manager/internal/models"
	"github.com/bluefrog/ocr-manager/internal/paths"
	"github.com/bluefrog/ocr-manager/internal/pyenv"
	"github.com/bluefrog/ocr-manager/internal/worker"
)

// Options 是启动参数。
type Options struct {
	// Root 是数据根目录（空 = 按 paths.Resolve 推导）。
	Root string
	// BuiltinCatalog 是编进二进制的默认模型清单。
	BuiltinCatalog string
	// Version 是应用版本。
	Version string
	// NoPrewarm 跳过启动时的 Hub 元数据探测（CI / 离线）。
	NoPrewarm bool
}

// Core 是进程内的服务门面。
type Core struct {
	Paths   *paths.Paths
	Catalog *models.Catalog
	Env     *pyenv.Env
	Bus     *events.EventBus
	Hub     *hub.Client
	GPU     gpu.Info
	Version string
	Started time.Time

	sink *events.FileSink

	mu       sync.Mutex
	settings Settings
	status   map[string]*ModelStatus
	workers  map[string]*worker.Worker
	active   string
	jobs     map[string]*Job

	// prepareMu 让「下载 / 装依赖」串行：pip 和 HF 下载并发只会互相拖慢。
	prepareMu sync.Mutex

	// envStatus 缓存 python 模块探测结果（起子进程，别每次刷新都跑）。
	envStatus *pyenv.EnvStatus
	envAt     time.Time
}

// New 组装一切（不启动任何模型）。
func New(opts Options) (*Core, error) {
	var p *paths.Paths
	var err error
	if opts.Root != "" {
		p, err = paths.WithRoot(opts.Root)
	} else {
		p, err = paths.Resolve()
	}
	if err != nil {
		return nil, err
	}

	bus := events.New(600)
	sink, sinkErr := events.NewFileSink(p.LogsDir())
	if sinkErr == nil {
		bus.SetSink(sink)
	} else {
		bus.Warn("logs", "日志落盘不可用: "+sinkErr.Error())
	}

	if opts.BuiltinCatalog == "" {
		opts.BuiltinCatalog = ocr.DefaultCatalog
	}

	// 内置 python 源码解包到运行时目录（内容变了才写）。
	// 开发态 ResolvePythonDir 直接指向仓库里的 `python/`，这里跳过解包，免得覆盖源码。
	pythonDir := ocr.ResolvePythonDir(p.RuntimeDir())
	if pythonDir == p.PythonDir() {
		if written, err := ocr.EnsurePythonAssets(p.PythonDir()); err != nil {
			bus.Warn("python", "解包内置 worker 源码失败: "+err.Error())
		} else if written > 0 {
			bus.Info("python", fmt.Sprintf("已解包 %d 个 worker 源文件到 %s", written, p.PythonDir()))
		}
	}

	_ = models.SyncUserCopy(p.ConfigFile(), opts.BuiltinCatalog)
	catalog, err := models.Load(p, opts.BuiltinCatalog)
	if err != nil {
		return nil, err
	}

	core := &Core{
		Paths:    p,
		Catalog:  catalog,
		Bus:      bus,
		Hub:      hub.New(),
		GPU:      gpu.Detect(),
		Version:  opts.Version,
		Started:  time.Now(),
		sink:     sink,
		settings: loadSettings(p),
		status:   map[string]*ModelStatus{},
		workers:  map[string]*worker.Worker{},
		jobs:     map[string]*Job{},
	}
	core.Env = pyenv.New(pythonDir, p.VenvDir())

	status := core.Env.Status()
	bus.Env(status.State, status.Message)
	core.refreshAll(opts.NoPrewarm)
	return core, nil
}

// VersionString 返回版本号。
func (c *Core) VersionString() string { return c.Version }

// Root 返回数据根目录。
func (c *Core) Root() string { return c.Paths.Root }

// Close 关掉所有 worker 与日志文件。
func (c *Core) Close() {
	c.mu.Lock()
	workers := c.workers
	c.workers = map[string]*worker.Worker{}
	c.active = ""
	c.mu.Unlock()
	for _, w := range workers {
		w.Shutdown(3 * time.Second)
	}
	if c.sink != nil {
		_ = c.sink.Close()
	}
	c.Bus.Close()
}

// ReloadCatalog 重新读模型清单（改完用户 config/models.toml 后用）。
func (c *Core) ReloadCatalog() error {
	catalog, err := models.Load(c.Paths, ocr.DefaultCatalog)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.Catalog = catalog
	c.mu.Unlock()
	c.refreshAll(true)
	return nil
}

// SetLayout 改权重 / 产物目录（会重置模型状态缓存）。
func (c *Core) SetLayout(layout paths.Layout) (paths.Layout, error) {
	next, err := c.Paths.SetLayout(layout)
	if err != nil {
		return paths.Layout{}, err
	}
	c.refreshAll(true)
	c.Bus.Info("settings", "目录布局已更新")
	return next, nil
}

// ReloadEnv 重读基础解释器设置。
func (c *Core) ReloadEnv() {
	status := c.Env.Status()
	c.Bus.Env(status.State, status.Message)
}
