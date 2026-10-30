//go:build windows

package main

import "syscall"

// createNoWindow 是 CREATE_NO_WINDOW：子进程照常跑，但不给它可见的控制台。
const createNoWindow = 0x08000000

// hiddenProcAttr 让子进程不弹黑框。
//
// 桌面壳本身是 `go tool nsgo build` 带 -H=windowsgui 编出来的，没有控制台；
// 但它拉起的 ocr-server、taskkill 都是控制台程序，直接 Start 会各自新开一个
// 命令行窗口——用户看到的就是「一启动先闪几个黑框」。
func hiddenProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
