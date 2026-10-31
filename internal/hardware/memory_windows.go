//go:build windows

package hardware

import (
	"syscall"
	"unsafe"
)

// memoryStatusEx 对应 Win32 的 MEMORYSTATUSEX。
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	globalMemoryStatus = kernel32.NewProc("GlobalMemoryStatusEx")
)

// probeMemory 用 GlobalMemoryStatusEx 读物理内存：不引第三方库，也不起子进程。
func probeMemory() Memory {
	var status memoryStatusEx
	status.Length = uint32(unsafe.Sizeof(status))
	ret, _, err := globalMemoryStatus.Call(uintptr(unsafe.Pointer(&status)))
	if ret == 0 {
		return wrap(Memory{Error: "读内存信息失败：" + err.Error()})
	}
	return wrap(Memory{
		TotalMB: int(status.TotalPhys / (1 << 20)),
		FreeMB:  int(status.AvailPhys / (1 << 20)),
	})
}
