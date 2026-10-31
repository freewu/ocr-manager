//go:build darwin

package hardware

import (
	"os/exec"
	"strconv"
	"strings"
)

// probeMemory 走 sysctl：`hw.memsize` 是总内存，可用内存要靠 vm_stat，代价高，先只给总数。
func probeMemory() Memory {
	raw, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return wrap(Memory{Error: "sysctl hw.memsize 失败：" + err.Error()})
	}
	total, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil || total <= 0 {
		return wrap(Memory{Error: "sysctl hw.memsize 输出看不懂：" + strings.TrimSpace(string(raw))})
	}
	return wrap(Memory{TotalMB: int(total / (1 << 20))})
}
