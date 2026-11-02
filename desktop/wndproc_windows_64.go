//go:build windows && (amd64 || arm64)

package main

// gwlpWndProc 是 GWLP_WNDPROC。
const gwlpWndProc = ^uintptr(3)

var procSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")

// setWndProcLong 换窗口过程，返回原过程（0 表示失败）。
//
// 64 位必须用 SetWindowLongPtrW：SetWindowLongW 收不住 64 位指针。
func setWndProcLong(hwnd, proc uintptr) uintptr {
	r, _, _ := procSetWindowLongPtrW.Call(hwnd, gwlpWndProc, proc)
	return r
}
