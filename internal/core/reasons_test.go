package core

import (
	"strings"
	"testing"

	"github.com/bluefrog/ocr-manager/internal/gpu"
	"github.com/bluefrog/ocr-manager/internal/hardware"
	"github.com/bluefrog/ocr-manager/internal/models"
)

// TestReasonsExternalToolWithoutCommand 外部工具没装 CLI 时要说清「没装什么命令」，
// 而不是一句「本应用不代管运行环境」（用户看的就是这句，等于没说）。
func TestReasonsExternalToolWithoutCommand(t *testing.T) {
	c := &Core{Memory: hardware.Memory{TotalMB: 32 * 1024}}
	spec := models.ModelSpec{
		ID:     "surya",
		Name:   "Surya（多语言 OCR）",
		Engine: "external",
		Options: map[string]any{
			// 故意用一个肯定装不上的名字
			"command": "ocr-manager-不存在的命令 <image>",
		},
	}

	reasons := c.Reasons(spec)
	if len(reasons) != 1 {
		t.Fatalf("外部工具应该给出一条原因，拿到 %d 条：%+v", len(reasons), reasons)
	}
	if reasons[0].Kind != ReasonExternal {
		t.Errorf("原因分类应该是 %s，拿到 %s", ReasonExternal, reasons[0].Kind)
	}
	for _, want := range []string{"没装", "ocr-manager-不存在的命令", "不代管"} {
		if !strings.Contains(reasons[0].Message, want) {
			t.Errorf("原因里应该有「%s」：%s", want, reasons[0].Message)
		}
	}
}

// TestReasonsExternalToolInstalled 装了 CLI 的外部工具不该再说「没装」，
// 而是「装了但不归本应用管」。
func TestReasonsExternalToolInstalled(t *testing.T) {
	c := &Core{Memory: hardware.Memory{TotalMB: 32 * 1024}}
	spec := models.ModelSpec{
		ID:      "surya",
		Engine:  "external",
		Options: map[string]any{"command": "sh -c true"},
	}

	reasons := c.Reasons(spec)
	if len(reasons) != 1 {
		t.Fatalf("应该给出一条原因，拿到 %d 条", len(reasons))
	}
	if strings.Contains(reasons[0].Message, "没装") {
		t.Errorf("命令在 PATH 里，不该说没装：%s", reasons[0].Message)
	}
	if !strings.Contains(reasons[0].Message, "已经装了") {
		t.Errorf("应该说明命令已经装了：%s", reasons[0].Message)
	}
}

// TestReasonsGPUModels 没显卡的机器上，VLM 模型的原因里要出现显卡 / 显存这些字眼，
// 并且带上 GPU 探测的具体错误。
func TestReasonsGPUModels(t *testing.T) {
	c := &Core{
		Memory: hardware.Memory{TotalMB: 64 * 1024},
		GPU:    gpu.Info{Available: false, Error: "没找到 nvidia-smi：本机大概没有 NVIDIA 显卡"},
	}
	spec := models.ModelSpec{ID: "dots-ocr", Engine: "vlm", RequiresGPU: true, MinVRAMGB: 8, MinRAMGB: 16}

	reasons := c.Reasons(spec)
	if len(reasons) != 1 || reasons[0].Kind != ReasonGPU {
		t.Fatalf("应该只有一条 GPU 原因，拿到 %+v", reasons)
	}
	for _, want := range []string{"NVIDIA", "CUDA", "nvidia-smi"} {
		if !strings.Contains(reasons[0].Message, want) {
			t.Errorf("原因里应该有「%s」：%s", want, reasons[0].Message)
		}
	}
}

// TestReasonsVRAMTooSmall 有卡但显存不够时，要把「需要多少 / 本机多少」都写出来。
func TestReasonsVRAMTooSmall(t *testing.T) {
	c := &Core{
		Memory: hardware.Memory{TotalMB: 64 * 1024},
		GPU: gpu.Info{
			Available: true,
			Devices:   []gpu.Device{{Index: 0, Name: "NVIDIA GeForce GTX 1060", MemoryMB: 6144}},
		},
	}
	spec := models.ModelSpec{ID: "deepseek-ocr", Engine: "vlm", RequiresGPU: true, MinVRAMGB: 16}

	reasons := c.Reasons(spec)
	if len(reasons) != 1 || reasons[0].Kind != ReasonVRAM {
		t.Fatalf("应该报显存不够，拿到 %+v", reasons)
	}
	for _, want := range []string{"16 GB", "6144 MB", "GTX 1060"} {
		if !strings.Contains(reasons[0].Message, want) {
			t.Errorf("原因里应该有「%s」：%s", want, reasons[0].Message)
		}
	}

	// 显存够的时候一条原因都不该有
	c.GPU.Devices = []gpu.Device{{Index: 0, Name: "NVIDIA RTX 4090", MemoryMB: 24564}}
	spec.MinRAMGB = 0
	if reasons := c.Reasons(spec); len(reasons) != 0 {
		t.Errorf("显存够就不该有原因，拿到 %+v", reasons)
	}
}

// TestReasonsRAMTooSmall 内存不够也要单独说出来（CPU 跑不动的模型同样常见）。
func TestReasonsRAMTooSmall(t *testing.T) {
	c := &Core{Memory: hardware.Memory{TotalMB: 8 * 1024, FreeMB: 2 * 1024}}
	spec := models.ModelSpec{ID: "dots-ocr", Engine: "vlm", MinRAMGB: 16}

	reasons := c.Reasons(spec)
	if len(reasons) != 1 || reasons[0].Kind != ReasonRAM {
		t.Fatalf("应该报内存不够，拿到 %+v", reasons)
	}
	for _, want := range []string{"16 GB", "8 GB", "可用"} {
		if !strings.Contains(reasons[0].Message, want) {
			t.Errorf("原因里应该有「%s」：%s", want, reasons[0].Message)
		}
	}
}

// TestUnavailableGateText 下载 / 启用 / 识别被拦住时，报错要带上具体原因。
func TestUnavailableGateText(t *testing.T) {
	c := &Core{
		Memory: hardware.Memory{TotalMB: 64 * 1024},
		GPU:    gpu.Info{Available: false, Error: "没找到 nvidia-smi"},
	}
	spec := models.ModelSpec{ID: "dots-ocr", Name: "dots.ocr", Engine: "vlm", RequiresGPU: true}

	err := c.unavailableGate(spec, "启用")
	if err == nil {
		t.Fatal("没有显卡时启用应该被拦住")
	}
	for _, want := range []string{"启用", "dots.ocr", "NVIDIA"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报错里应该有「%s」：%s", want, err.Error())
		}
	}

	ok := models.ModelSpec{ID: "rapidocr", Name: "RapidOCR", Engine: "rapidocr"}
	if err := c.unavailableGate(ok, "启用"); err != nil {
		t.Errorf("CPU 模型不该被拦：%v", err)
	}
}
