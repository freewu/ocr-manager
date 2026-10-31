package core

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/bluefrog/ocr-manager/internal/gpu"
	"github.com/bluefrog/ocr-manager/internal/hardware"
	"github.com/bluefrog/ocr-manager/internal/models"
)

// 本机不可用的原因分类（界面按 kind 挑文案，CLI 也能按它过滤）。
const (
	ReasonGPU      = "gpu"      // 没有 NVIDIA 显卡 / CUDA
	ReasonVRAM     = "vram"     // 显存不够
	ReasonRAM      = "ram"      // 系统内存不够
	ReasonExternal = "external" // 外部工具，本应用不代管
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
		out = append(out, Reason{Kind: ReasonExternal, Message: externalReason(spec)})
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

// externalReason 说清楚外部工具「缺什么」：没装命令 / 装了但不归本应用管。
func externalReason(spec models.ModelSpec) string {
	command := spec.ExternalCommand()
	if command == "" {
		return "外部工具：本应用只登记入口，不代管它的运行环境"
	}
	if path, err := exec.LookPath(command); err == nil {
		return fmt.Sprintf(
			"外部工具：本机已经装了 %s（%s），但它有独立的环境和权重，本应用不接管 —— 直接用它自己的命令行",
			command, path,
		)
	}
	return fmt.Sprintf(
		"外部工具：本机没装 %s 命令（按它官网的说明装好后刷新一下即可），本应用只登记入口、不代管依赖与权重",
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
