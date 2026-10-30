package core

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Op 是一条正在跑的后台长任务：装依赖、下权重、重建 venv。
//
// 这些活儿动辄几分钟，HTTP 请求一律立刻返回，界面靠 /api/info 里的 op
// 知道「现在在忙什么」、进度条画到哪；也靠它挡掉重复点击。
type Op struct {
	// Kind 是 prepare / install / recreate。
	Kind string `json:"kind"`
	// Model 只有 prepare 才有。
	Model string `json:"model,omitempty"`
	// Label 是给界面看的中文说明。
	Label string `json:"label"`
	// Started 是开始时间（毫秒时间戳）。
	Started int64 `json:"started"`
}

// ElapsedMS 返回这条任务已经跑了多久。
func (o *Op) ElapsedMS() int64 {
	if o == nil || o.Started == 0 {
		return 0
	}
	return time.Now().UnixMilli() - o.Started
}

// opSlot 保证同一时间只有一条动 venv / 权重目录的任务：
// 两条 pip 并发只会互相锁住 site-packages，两条 HF 下载也只会抢带宽。
type opSlot struct {
	mu  sync.Mutex
	cur *Op
}

// begin 占坑；已经有人占着就把在跑的活儿报回去（界面能直接告诉用户等谁）。
func (s *opSlot) begin(kind, model, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur != nil {
		return fmt.Errorf("「%s」还没做完，等它结束再点", s.cur.Label)
	}
	s.cur = &Op{Kind: kind, Model: model, Label: label, Started: time.Now().UnixMilli()}
	return nil
}

// end 放坑。
func (s *opSlot) end() {
	s.mu.Lock()
	s.cur = nil
	s.mu.Unlock()
}

// current 返回正在跑的活儿（没有就是 nil）。
func (s *opSlot) current() *Op {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil {
		return nil
	}
	copied := *s.cur
	return &copied
}

// busy 报告有没有活儿在跑。
func (s *opSlot) busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur != nil
}

// CurrentOp 返回正在跑的后台任务，没有就返回 nil。
func (c *Core) CurrentOp() *Op { return c.op.current() }

// OpBusy 报告是否有后台任务在跑（装依赖 / 下权重 / 重建 venv）。
func (c *Core) OpBusy() bool { return c.op.busy() }

// runOp 是「占坑 + 开 goroutine + 放坑 + 出错记日志」的公共外壳。
func (c *Core) runOp(kind, model, label string, fn func() error) error {
	if err := c.op.begin(kind, model, label); err != nil {
		return err
	}
	go func() {
		defer c.op.end()
		if err := fn(); err != nil {
			source := model
			if source == "" {
				source = kind
			}
			c.Bus.Error(source, label+"失败："+err.Error())
			return
		}
		c.Bus.Info(sourceOrKind(model, kind), label+"完成")
	}()
	return nil
}

func sourceOrKind(model, kind string) string {
	if model != "" {
		return model
	}
	return kind
}

// StartPrepare 异步准备模型：装 pip extras + 下权重，立刻返回。
//
// 状态先切成「准备中」，界面不用等 HTTP 回来就能看到反应；
// 真正的进度走事件总线（progress 事件）+ /api/models 里的状态。
func (c *Core) StartPrepare(modelID string, force bool) error {
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

	label := "准备 " + spec.Name
	if err := c.op.begin("prepare", modelID, label); err != nil {
		return err
	}
	// 先落状态再开 goroutine：界面下一帧就能显示「准备中」。
	c.setState(modelID, StatePreparing, "正在安装依赖…")
	go func() {
		defer c.op.end()
		if err := c.Prepare(modelID, force); err != nil {
			c.Bus.Error(modelID, "准备失败："+err.Error())
			return
		}
		c.Bus.Info(modelID, "准备完成")
	}()
	return nil
}

// StartEnvInstall 异步装 pip extras（venv 的依赖安装，动辄几分钟）。
func (c *Core) StartEnvInstall(extras []string) error {
	label := "安装依赖"
	if len(extras) > 0 {
		label += " " + strings.Join(extras, ",")
	}
	return c.runOp("install", "", label, func() error {
		if err := c.Env.Install(c.Bus, extras); err != nil {
			return err
		}
		c.ReloadEnv()
		return nil
	})
}

// StartEnvRecreate 异步重建 venv。
func (c *Core) StartEnvRecreate() error {
	return c.runOp("recreate", "", "重建 venv", func() error {
		if err := c.Env.RecreateVenv(c.Bus); err != nil {
			return err
		}
		c.ReloadEnv()
		return nil
	})
}
