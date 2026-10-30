// Command ocr-manager-desktop 是 OCR Manager 的原生桌面壳。
//
// UI 用 NSGo（github.com/oneo-me/nsgo，Go 版 Native UI）写，业务全部走 Go 后端
// ocr-server：启动时先探一下本地 8899，连不上就自己拉起 bin/ocr-server，
// 退出时把它（和它的 python worker）一起收掉。
package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/oneo-me/nsgo"
)

func main() {
	// 后端是本进程拉起来的，就由本进程负责关掉（连同 python worker）。
	defer stopServer()
	// 托盘图标要在退出前摘掉，否则系统托盘里会留一个点不动的残影。
	defer stopTray()
	// 窗口起不来（多半是 Linux 缺 GTK 4 运行库）时给一句能照做的话，别只甩 panic。
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		msg := fmt.Sprint(r)
		fmt.Fprintf(os.Stderr, "启动界面失败：%v\n", r)
		// 光一句 nil pointer 谁也修不了，调用栈一起打出来（启动期只有一次，不影响性能）。
		fmt.Fprintf(os.Stderr, "%s\n", debug.Stack())
		// 只有确实是 GTK 缺失才搬出安装命令，别让别的 panic 也套上这句话。
		if runtime.GOOS == "linux" && strings.Contains(msg, "GTK") {
			fmt.Fprintln(os.Stderr, "Linux 需要 GTK 4 / Pango / Cairo，例如：sudo apt install libgtk-4-1 libpango-1.0-0 libcairo2")
		}
		os.Exit(1)
	}()

	startTray()
	nsgo.Run(Root)
}
