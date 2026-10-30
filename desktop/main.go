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

	"github.com/oneo-me/nsgo"
)

func main() {
	// 后端是本进程拉起来的，就由本进程负责关掉（连同 python worker）。
	defer stopServer()
	// 托盘图标要在退出前摘掉，否则系统托盘里会留一个点不动的残影。
	defer stopTray()
	// 窗口起不来（多半是 Linux 缺 GTK 4 运行库）时给一句能照做的话，别只甩 panic。
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "启动界面失败：%v\n", r)
			if runtime.GOOS == "linux" {
				fmt.Fprintln(os.Stderr, "Linux 需要 GTK 4 / Pango / Cairo，例如：sudo apt install libgtk-4-1 libpango-1.0-0 libcairo2")
			}
			os.Exit(1)
		}
	}()

	startTray()
	nsgo.Run(Root)
}
