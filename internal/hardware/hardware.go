// Package hardware 探测本机硬件上限（内存），用来回答「这个模型为什么跑不动」。
//
// 显卡那部分在 `internal/gpu`（要调 nvidia-smi），这里只管内存：
// 模型清单里写了 `min_ram_gb` 就必须先过这一关，界面上的「本机不可用」
// 要能说出具体是内存不够还是显存不够，而不是一句笼统的「不支持」。
package hardware

import (
	"sync"
	"time"
)

// Memory 是本机内存现状（单位 MB）。
type Memory struct {
	TotalMB int `json:"total_mb"`
	// FreeMB 是当前可用内存；有些系统拿不到就是 0。
	FreeMB    int    `json:"free_mb"`
	Error     string `json:"error,omitempty"`
	CheckedAt int64  `json:"checked_at"`
}

// TotalGB 把总内存换算成 GB（向下取整，1 GB = 1024 MB）。
func (m Memory) TotalGB() int { return m.TotalMB / 1024 }

// FreeGB 把可用内存换算成 GB（向下取整）。
func (m Memory) FreeGB() int { return m.FreeMB / 1024 }

var (
	once   sync.Once
	cached Memory
)

// DetectMemory 探测一次并缓存（总内存不会变，可用内存当参考值）。
func DetectMemory() Memory {
	once.Do(func() { cached = probeMemory() })
	return cached
}

// RefreshMemory 忽略缓存重新探测（设置页的「重新检测」）。
func RefreshMemory() Memory {
	cached = probeMemory()
	return cached
}

// wrap 补上时间戳，让各平台的实现只管填数字。
func wrap(info Memory) Memory {
	info.CheckedAt = time.Now().UnixMilli()
	return info
}
