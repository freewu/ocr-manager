package core

import (
	"fmt"
	"strings"

	"github.com/bluefrog/ocr-manager/internal/gpu"
	"github.com/bluefrog/ocr-manager/internal/hardware"
	"github.com/bluefrog/ocr-manager/internal/models"
	"github.com/bluefrog/ocr-manager/internal/pyenv"
)

// 本机不可用的原因分类（界面按 kind 挑文案，CLI 也能按它过滤）。
const (
	ReasonGPU      = "gpu"      // 没有 NVIDIA 显卡 / CUDA
	ReasonVRAM     = "vram"     // 显存不够
	ReasonRAM      = "ram"      // 系统内存不够
	ReasonExternal = "external" // 外部工具：命令装了但跑不起来
	ReasonPlatform = "platform" // 系统不支持
)

// Reason 是一条「本机为什么跑不了这个模型」的具体原因。
//
// 以前状态里只有一句「本机没有 NVIDIA 显卡」，遇到 surya / MinerU 这种外部
// 工具就只剩「不支持」三个字，用户不知道是没装命令、显存不够还是内存不够。
type Reason struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// Reasons 汇总这条模型在本机跑不起来的全部原因（空表示能跑）。
func (c *Core) Reasons(spec models.ModelSpec) []Reason { return c.reasonsOf(spec) }

// ReasonText 把原因拼成一句话（没有原因就是空串）。
func (c *Core) ReasonText(spec models.ModelSpec) string {
	return reasonSummary(c.reasonsOf(spec))
}

// unavailableGate 拦住本机跑不起来的模型（下载 / 启用 / 识别共用）。
//
// action 是「准备」这类动名词，报错直接念出来：「无法准备「Surya」：外部工具…」
func (c *Core) unavailableGate(spec models.ModelSpec, action string) error {
	summary := c.ReasonText(spec)
	if summary == "" {
		return nil
	}
	if action == "" {
		return fmt.Errorf("「%s」在本机跑不起来：%s", spec.Name, summary)
	}
	return fmt.Errorf("无法%s「%s」：%s", action, spec.Name, summary)
}

// reasonSummary 把多条原因拼成一句话（卡片上显示的那行）。
func reasonSummary(reasons []Reason) string {
	out := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		out = append(out, reason.Message)
	}
	return strings.Join(out, "；")
}

// reasonsOf 逐条比对本机条件和清单要求。
func (c *Core) reasonsOf(spec models.ModelSpec) []Reason {
	out := []Reason{}

	if !models.PlatformAllowed(spec) {
		out = append(out, Reason{
			Kind:    ReasonPlatform,
			Message: fmt.Sprintf("这个模型只支持 %s，本机是 %s", strings.Join(spec.Platforms, " / "), models.Platform),
		})
	}

	if spec.Engine == "external" {
		// 外部工具（MinerU / Surya）现在也走「安装命令」：依赖没装是待安装，不算不可用。
		if message := c.externalReason(spec); message != "" {
			out = append(out, Reason{Kind: ReasonExternal, Message: message})
		}
	}

	if spec.RequiresGPU {
		out = append(out, c.gpuReasons(spec)...)
	}

	if spec.MinRAMGB > 0 {
		mem := c.Memory
		switch {
		case mem.TotalMB <= 0 && mem.Error != "":
			out = append(out, Reason{
				Kind:    ReasonRAM,
				Message: fmt.Sprintf("需要 ≥ %d GB 内存，但读不到本机内存大小（%s）", spec.MinRAMGB, mem.Error),
			})
		case mem.TotalMB > 0 && mem.TotalGB() < spec.MinRAMGB:
			out = append(out, Reason{
				Kind:    ReasonRAM,
				Message: fmt.Sprintf("内存不够：需要 ≥ %d GB，本机只有 %s", spec.MinRAMGB, memoryText(mem)),
			})
		}
	}
	return out
}

// gpuReasons 是有卡 / 没卡 / 显存不够三种情况的具体说明。
func (c *Core) gpuReasons(spec models.ModelSpec) []Reason {
	if !c.GPU.Available {
		message := "需要 NVIDIA 显卡（CUDA），本机没检测到可用的 GPU"
		if strings.TrimSpace(c.GPU.Error) != "" {
			message += "（" + c.GPU.Error + "）"
		}
		message += "。先装 NVIDIA 驱动（含 CUDA），装好后刷新一下重新检测；只有 CPU 的话可以改用 RapidOCR / PaddleOCR"
		return []Reason{{Kind: ReasonGPU, Message: message}}
	}
	if spec.MinVRAMGB <= 0 {
		return nil
	}
	best, ok := bestGPU(c.GPU.Devices)
	if !ok || best.MemoryMB <= 0 || best.MemoryMB/1024 >= spec.MinVRAMGB {
		return nil
	}
	return []Reason{{
		Kind: ReasonVRAM,
		Message: fmt.Sprintf("显存不够：需要 ≥ %d GB，本机最好的卡是 %s（%d MB 显存）",
			spec.MinVRAMGB, strings.TrimSpace(best.Name), best.MemoryMB),
	}}
}

// bestGPU 挑显存最大的一张卡（多卡机器上按最大的算，够用就行）。
func bestGPU(devices []gpu.Device) (gpu.Device, bool) {
	var best gpu.Device
	found := false
	for _, item := range devices {
		if !found || item.MemoryMB > best.MemoryMB {
			best, found = item, true
		}
	}
	return best, found
}

// externalReason 只在「依赖装好了、命令却没出来」时说话。
//
// 依赖没装属于正常待安装状态（卡片上有「安装命令」按钮），不应该是「本机不可用」。
func (c *Core) externalReason(spec models.ModelSpec) string {
	extrasReady := len(pyenv.ForModel(spec)) == 0
	if !extrasReady && c.Env != nil {
		extrasReady = c.extrasReady(spec)
	}
	lookup := func(name string) string {
		if c.Env == nil {
			return ""
		}
		return c.Env.CommandPath(name)
	}
	return externalMissingReason(spec, extrasReady, lookup)
}

// externalMissingReason 是纯函数版本（方便单测）：依赖齐了但命令找不到才报原因。
func externalMissingReason(spec models.ModelSpec, extrasReady bool, lookup func(string) string) string {
	command := spec.ExternalCommand()
	if command == "" {
		return "清单里没写 options.command，不知道要调哪个命令行：在模型清单里补上再刷新"
	}
	if !extrasReady {
		return ""
	}
	if strings.TrimSpace(lookup(command)) != "" {
		return ""
	}
	return fmt.Sprintf(
		"依赖装好了，但 venv 里没有 %s 命令：点模型卡上的「重新安装命令」，或去「设置 → Python 环境」点「重新检测」",
		command,
	)
}

// memoryText 把内存现状说成人话。
func memoryText(mem hardware.Memory) string {
	if mem.TotalMB <= 0 {
		return "未知"
	}
	if mem.FreeMB > 0 {
		return fmt.Sprintf("%d GB（当前可用 %d GB）", mem.TotalGB(), mem.FreeGB())
	}
	return fmt.Sprintf("%d GB", mem.TotalGB())
}
