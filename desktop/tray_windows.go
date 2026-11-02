//go:build windows

package main

import (
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// 系统托盘（Windows）。
//
// NSGo 没有托盘 API，这里直接用 Win32：建一个隐藏窗口收托盘回调，右键弹菜单。
// 不引入额外依赖，全部走 syscall 里的 DLL 调用。
//
// 菜单：打开主窗口 / 打开数据目录 / 退出。
//
// 关窗口（X、Alt+F4、任务栏关闭）不退出：主窗口只是被藏起来，后端和 python
// worker 继续跑，识别队列不断。真要退出走托盘菜单「退出」，那里会把窗口真正
// 关掉，main 里的 defer 才有机会收后端。

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
	swShow    = 5
	swHide    = 0

	nimModify = 1
	nifInfo   = 0x10 // 想让托盘图标弹气泡，得带上这个 flag 再 NIM_MODIFY
	niifInfo  = 0x1

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
	procIsIconic            = user32.NewProc("IsIconic")
	procIsWindow            = user32.NewProc("IsWindow")
	procCallWindowProcW     = user32.NewProc("CallWindowProcW")
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
	// 主窗口句柄：收到托盘里以后窗口不可见，EnumWindows 那个按「可见」
	// 挑窗口的办法就找不回来了，所以换窗口过程时就把它记住。
	mainHwnd atomic.Uintptr

	// quitting 为真时 WM_CLOSE 照常往下传（真关），否则只把窗口藏起来。
	quitting atomic.Bool

	// oldWndProc 是换窗口过程前的原过程（SetWindowLongPtr 那条退路要转回去）。
	oldWndProc atomic.Uintptr

	// firstHide 只弹一次「收到托盘里了」的气泡，别每次关都骚扰。
	firstHide   sync.Once
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
		installCloseToTray()
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
	copy(nid.Tip[:], syscall.StringToUTF16("OCR Manager（单击打开窗口 / 右键退出）"))
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

// closeToTrayProc 是换到 NSGo 主窗口上的窗口过程。
//
// 只拦 WM_CLOSE：把窗口藏起来、返回 0，NSGo 就收不到这条消息，也就不会
// DestroyWindow → PostQuitMessage → 进程退出。其余消息原样转给 NSGo 的过程，
// 绘制 / 输入 / 选区分毫不变。
var closeToTrayProc = syscall.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
	if msg == wmClose && !quitting.Load() {
		hideToTray(hwnd)
		return 0
	}
	r, _, _ := procCallWindowProcW.Call(oldWndProc.Load(), hwnd, msg, wparam, lparam)
	return r
})

// installCloseToTray 等 NSGo 把窗口建出来，再把它的窗口过程换成 closeToTrayProc。
//
// 为什么不在 startTray 里直接换：nsgo.Run 是阻塞的，调用它的时侯窗口还不存在。
// 这里每秒瞅 10 次，找到就换，换完退出协程。
//
// 用 SetWindowLongPtr 而不是 comctl32 的 SetWindowSubclass：后者要 exe 带
// comctl32 v6 清单才生效，我们没带（实测 5.82 直接返回失败），换过程这条路
// 一条就够，32 位走 SetWindowLongW（见 wndproc_windows_32.go）。
func installCloseToTray() {
	go func() {
		for i := 0; i < 600; i++ { // 最多等 60 秒，等不到就算了（点 X 照旧退出）
			if hwnd := findMainWindow(); hwnd != 0 {
				// 记下句柄：藏起来以后窗口不可见，EnumWindows 就找不回来了。
				mainHwnd.Store(hwnd)
				if old := setWndProcLong(hwnd, closeToTrayProc); old != 0 {
					oldWndProc.Store(old)
				}
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
}

// hideToTray 把主窗口藏起来，并告诉用户它在托盘里。
func hideToTray(hwnd uintptr) {
	procShowWindow.Call(hwnd, swHide)
	firstHide.Do(func() {
		trayBalloon("还在跑", "窗口收到托盘里了，识别任务不受影响。\n单击托盘图标打开窗口，右键→退出。")
	})
}

// trayBalloon 弹一个气泡/通知（Win10+ 会显示成普通通知）。
func trayBalloon(title, text string) {
	trayMu.Lock()
	nid := trayNID
	trayMu.Unlock()
	if nid.Wnd == 0 {
		return
	}
	nid.Flags |= nifInfo
	nid.InfoFlags = niifInfo
	nid.Info = [256]uint16{}
	nid.InfoTitle = [64]uint16{}
	copy(nid.Info[:], syscall.StringToUTF16(text))
	copy(nid.InfoTitle[:], syscall.StringToUTF16(title))
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

// showMainWindow 把主窗口从托盘里拿回来（藏起来过就用记住的句柄）。
func showMainWindow() {
	hwnd := rememberedMainWindow()
	if hwnd == 0 {
		return
	}
	// 藏起来之前是最小化状态就用 SW_RESTORE，否则 SW_SHOW
	// （SW_RESTORE 会把最大化窗口退回普通尺寸，不能无脑用）。
	if v, _, _ := procIsIconic.Call(hwnd); v != 0 {
		procShowWindow.Call(hwnd, swRestore)
	} else {
		procShowWindow.Call(hwnd, swShow)
	}
	procSetForegroundWindow.Call(hwnd)
}

// rememberedMainWindow 返回主窗口句柄：先用记住的，句柄失效了再枚举。
func rememberedMainWindow() uintptr {
	if hwnd := mainHwnd.Load(); hwnd != 0 {
		if v, _, _ := procIsWindow.Call(hwnd); v != 0 {
			return hwnd
		}
		mainHwnd.Store(0)
	}
	if hwnd := findMainWindow(); hwnd != 0 {
		mainHwnd.Store(hwnd)
		return hwnd
	}
	return 0
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

// quitFromTray 真退出：先举起 quitting 旗（否则 WM_CLOSE 又会被换成「藏起来」），
// 再请主窗口关闭——走 NSGo 的正常退出流程，main 里的 defer 会把后端和
// python worker 一起收掉。窗口不在就直接收后端。
func quitFromTray() {
	quitting.Store(true)
	hwnd := rememberedMainWindow()
	if hwnd == 0 {
		stopServer()
		stopTray()
		os.Exit(0)
	}
	procPostMessageW.Call(hwnd, wmClose, 0, 0)
	// 兜底：两秒后窗口还在（消息没走到 NSGo、或转发链断了），直接收摊。
	// 不这么做的话菜单里的「退出」可能变成「又藏起来」，用户会以为没反应。
	time.Sleep(2 * time.Second)
	if v, _, _ := procIsWindow.Call(hwnd); v != 0 {
		stopServer()
		stopTray()
		os.Exit(0)
	}
}
