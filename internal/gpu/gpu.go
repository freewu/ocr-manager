// Package gpu 探测本机 NVIDIA 显卡（决定 VLM 类模型能不能下载/启用）。
//
// 只查 `nvidia-smi`，不引入 CUDA 依赖：够用来回答「这台机器能不能跑 VLM」。
package gpu

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Device 是一张卡。
type Device struct {
	Index    int    `json:"index"`
	Name     string `json:"name"`
	MemoryMB int    `json:"memory_mb"`
	Driver   string `json:"driver"`
}

// Info 是探测结果。
type Info struct {
	Available bool     `json:"available"`
	Devices   []Device `json:"devices"`
	Error     string   `json:"error,omitempty"`
	CheckedAt int64    `json:"checked_at"`
}

var (
	once   sync.Once
	cached Info
)

// Detect 探测一次并缓存（显卡不会热插拔）。
func Detect() Info {
	once.Do(func() { cached = probe() })
	return cached
}

// Refresh 忽略缓存重新探测（设置页的「重新检测」）。
func Refresh() Info {
	cached = probe()
	return cached
}

func probe() Info {
	info := Info{Devices: []Device{}, CheckedAt: time.Now().UnixMilli()}
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		info.Error = "没找到 nvidia-smi：本机大概没有 NVIDIA 显卡（或驱动没装）"
		return info
	}

	cmd := exec.Command(path, "--query-gpu=index,name,memory.total,driver_version", "--format=csv,noheader,nounits")
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		info.Error = "nvidia-smi 执行失败：" + err.Error()
		return info
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		for idx := range fields {
			fields[idx] = strings.TrimSpace(fields[idx])
		}
		device := Device{}
		if len(fields) > 0 {
			if parsed, err := strconv.Atoi(fields[0]); err == nil {
				device.Index = parsed
			}
		}
		if len(fields) > 1 {
			device.Name = fields[1]
		}
		if len(fields) > 2 {
			if parsed, err := strconv.Atoi(fields[2]); err == nil {
				device.MemoryMB = parsed
			}
		}
		if len(fields) > 3 {
			device.Driver = fields[3]
		}
		info.Devices = append(info.Devices, device)
	}
	info.Available = len(info.Devices) > 0
	if !info.Available {
		info.Error = "nvidia-smi 没列出任何卡"
	}
	return info
}
