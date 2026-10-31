//go:build linux

package hardware

import (
	"os"
	"strconv"
	"strings"
)

// probeMemory 读 /proc/meminfo：MemTotal 一定有，MemAvailable 老内核可能没有。
func probeMemory() Memory {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return wrap(Memory{Error: "读 /proc/meminfo 失败：" + err.Error()})
	}
	info := Memory{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		kb := parseKB(value)
		switch strings.TrimSpace(key) {
		case "MemTotal":
			info.TotalMB = kb / 1024
		case "MemAvailable", "MemFree":
			// MemAvailable 才是「还能用多少」；只有 MemFree 时先凑个数，
			// 后面遇到 MemAvailable 会覆盖它（/proc/meminfo 里它在 MemFree 之后）。
			if info.FreeMB == 0 || strings.TrimSpace(key) == "MemAvailable" {
				info.FreeMB = kb / 1024
			}
		}
	}
	if info.TotalMB == 0 {
		info.Error = "/proc/meminfo 里没找到 MemTotal"
	}
	return wrap(info)
}

// parseKB 从 "16384000 kB" 里抠出数字。
func parseKB(value string) int {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return 0
	}
	parsed, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0
	}
	return parsed
}
