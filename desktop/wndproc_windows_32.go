//go:build windows && 386

package main

// gwlpWndProc 是 GWL_WNDPROC。
const gwlpWndProc = ^uintptr(3)

var procSetWindowLongW = user32.NewProc("SetWindowLongW")

// setWndProcLong 换窗口过程，返回原过程（0 表示失败）。
//
// 32 位的 user32 里没有 SetWindowLongPtrW 这个导出，只能走 SetWindowLongW。
func setWndProcLong(hwnd, proc uintptr) uintptr {
	r, _, _ := procSetWindowLongW.Call(hwnd, gwlpWndProc, proc)
	return r
}
