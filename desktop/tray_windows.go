//go:build windows

package main

import (
	"os"
	"sync"
	"syscall"
	"unsafe"
)

// 系统托盘（Windows）。
//
// NSGo 没有托盘 API，这里直接用 Win32：建一个隐藏窗口收托盘回调，右键弹菜单。
// 不引入额外依赖，全部走 syscall 里的 DLL 调用。
//
// 菜单：打开主窗口 / 打开数据目录 / 退出。

const (
	trayCallbackMsg = 0x8001 // WM_APP+1

	wmDestroy       = 0x0010
	wmClose         = 0x0010
	wmNull          = 0x0000
	wmRButtonUp     = 0x0205
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203

	nimAdd     = 0
	nimDelete  = 2
	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4

	mfString       = 0x0000
	mfSeparator    = 0x0800
	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	swRestore = 9

	idShowWindow = 1
	idOpenHome   = 2
	idQuit       = 3

	idiApplication = 32512
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procEnumWindows         = user32.NewProc("EnumWindows")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procGetWindowThreadPID  = user32.NewProc("GetWindowThreadProcessId")
	procShowWindow          = user32.NewProc("ShowWindow")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procCreateIconFromResEx = user32.NewProc("CreateIconFromResourceEx")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
)

// wndClassEx 是 WNDCLASSEXW。
type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type trayPoint struct{ X, Y int32 }

type trayMsg struct {
	Hwnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       trayPoint
	LPrivate uint32
}

// notifyIconData 是 NOTIFYICONDATAW（64 位布局）。
type notifyIconData struct {
	Size            uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUID            [16]byte
	BalloonIcon     uintptr
}

var (
	trayOnce    sync.Once
	trayHandle  uintptr
	trayMu      sync.Mutex
	trayNID     notifyIconData
	trayWndProc = syscall.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
		switch msg {
		case trayCallbackMsg:
			switch uint32(lparam & 0xffff) {
			case wmRButtonUp:
				showTrayMenu(hwnd)
			case wmLButtonUp, wmLButtonDblClk:
				showMainWindow()
			}
			return 0
		case wmDestroy:
			procPostQuitMessage.Call(0)
			return 0
		}
		r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
		return r
	})
)

// startTray 建隐藏窗口 + 托盘图标，然后在后台跑消息循环。
func startTray() {
	if os.Getenv("OCR_NO_TRAY") != "" {
		return
	}
	trayOnce.Do(func() {
		hwnd, err := createTrayWindow()
		if err != nil {
			return
		}
		trayHandle = hwnd
		addTrayIcon(hwnd)
		go messageLoop()
	})
}

// stopTray 摘掉托盘图标（退出前调用，否则会留一个「幽灵图标」）。
func stopTray() {
	trayMu.Lock()
	defer trayMu.Unlock()
	if trayNID.Wnd != 0 {
		procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&trayNID)))
		trayNID = notifyIconData{}
	}
	if trayHandle != 0 {
		procDestroyWindow.Call(trayHandle)
		trayHandle = 0
	}
}

func createTrayWindow() (uintptr, error) {
	instance, _, _ := procGetModuleHandleW.Call(0)
	class, err := syscall.UTF16PtrFromString("OCRManagerTrayWnd")
	if err != nil {
		return 0, err
	}
	wc := wndClassEx{
		Size:      uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:   trayWndProc,
		Instance:  instance,
		ClassName: class,
	}
	if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return 0, err
	}
	title, _ := syscall.UTF16PtrFromString("OCR Manager Tray")
	hwnd, _, err := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0, 0, 0, instance, 0,
	)
	if hwnd == 0 {
		return 0, err
	}
	return hwnd, nil
}

func addTrayIcon(hwnd uintptr) {
	nid := notifyIconData{
		Wnd:             hwnd,
		ID:              1,
		Flags:           nifMessage | nifIcon | nifTip,
		CallbackMessage: trayCallbackMsg,
		Icon:            trayIcon(),
	}
	nid.Size = uint32(unsafe.Sizeof(nid))
	copy(nid.Tip[:], syscall.StringToUTF16("OCR Manager"))
	procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))

	trayMu.Lock()
	trayNID = nid
	trayMu.Unlock()
}

// trayIcon 取托盘图标：先找 exe 里链接的资源（打包后的 exe 有），
// 没有就用内嵌的 PNG（`go run` 时 exe 没有资源），最后退回系统默认图标。
func trayIcon() uintptr {
	instance, _, _ := procGetModuleHandleW.Call(0)
	if h, _, _ := procLoadIconW.Call(instance, 1); h != 0 {
		return h
	}
	if len(iconPNG) > 0 {
		if h, _, _ := procCreateIconFromResEx.Call(
			uintptr(unsafe.Pointer(&iconPNG[0])), uintptr(len(iconPNG)),
			1, 0x00030000, 0, 0, 0,
		); h != 0 {
			return h
		}
	}
	h, _, _ := procLoadIconW.Call(0, idiApplication)
	return h
}

func messageLoop() {
	var m trayMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func menuText(s string) uintptr {
	p, _ := syscall.UTF16PtrFromString(s)
	return uintptr(unsafe.Pointer(p))
}

func showTrayMenu(hwnd uintptr) {
	var p trayPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))

	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	procAppendMenuW.Call(menu, mfString, idShowWindow, menuText("打开主窗口"))
	if dir := trayHomeDir(); dir != "" {
		procAppendMenuW.Call(menu, mfString, idOpenHome, menuText("打开数据目录"))
	}
	procAppendMenuW.Call(menu, mfSeparator, 0, 0)
	procAppendMenuW.Call(menu, mfString, idQuit, menuText("退出"))

	// 托盘菜单的规矩：先抢前台，否则点菜单外面收不掉。
	procSetForegroundWindow.Call(hwnd)
	id, _, _ := procTrackPopupMenu.Call(
		menu, tpmRightButton|tpmReturnCmd,
		uintptr(p.X), uintptr(p.Y), 0, hwnd, 0,
	)
	procPostMessageW.Call(hwnd, wmNull, 0, 0)
	procDestroyMenu.Call(menu)

	switch id {
	case idShowWindow:
		showMainWindow()
	case idOpenHome:
		if dir := trayHomeDir(); dir != "" {
			_ = openPath(dir)
		}
	case idQuit:
		quitFromTray()
	}
}

// showMainWindow 把主窗口从最小化恢复并拿到前台。
func showMainWindow() {
	hwnd := findMainWindow()
	if hwnd == 0 {
		return
	}
	procShowWindow.Call(hwnd, swRestore)
	procSetForegroundWindow.Call(hwnd)
}

// enumFound 是 EnumWindows 回调找到的主窗口。
var enumFound uintptr

// enumWindowsProc 挑「本进程的、可见的」顶层窗口。
// 不按标题找：裸 `go run` 时 NSGo 的窗口标题是默认的 "NSGo"，不是 nsgo.yaml 的名字。
var enumWindowsProc = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
	var pid uint32
	procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != uint32(os.Getpid()) {
		return 1
	}
	if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
		return 1 // 托盘自己的隐藏窗口
	}
	enumFound = hwnd
	return 0
})

func findMainWindow() uintptr {
	enumFound = 0
	procEnumWindows.Call(enumWindowsProc, 0)
	return enumFound
}

// quitFromTray 先请主窗口关闭（走正常退出流程，后端和 worker 会被收掉），
// 窗口不在就直接收后端。
func quitFromTray() {
	if hwnd := findMainWindow(); hwnd != 0 {
		procPostMessageW.Call(hwnd, wmClose, 0, 0)
		return
	}
	stopServer()
	stopTray()
	os.Exit(0)
}
