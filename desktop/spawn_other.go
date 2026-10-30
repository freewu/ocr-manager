//go:build !windows

package main

import "syscall"

// hiddenProcAttr：Linux / macOS 起子进程本来就不弹窗口，用默认属性即可。
func hiddenProcAttr() *syscall.SysProcAttr { return nil }
