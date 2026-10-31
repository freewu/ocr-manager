//go:build !windows && !linux && !darwin

package hardware

// probeMemory 在这类平台上还没有实现：返回空值，判断内存要求时会跳过这一关，
// 而不是误报「内存不够」。
func probeMemory() Memory {
	return wrap(Memory{Error: "这个系统还不支持读内存信息"})
}
