package main

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// openPath 在系统文件管理器里打开一个目录（历史记录 / 设置页用）。
func openPath(dir string) error {
	if dir == "" {
		return errors.New("还没有目录可打开")
	}
	if _, err := os.Stat(dir); err != nil {
		return errors.New("目录不存在：" + dir)
	}
	switch runtime.GOOS {
	case "windows":
		// explorer 对目录参数不认引号，直接传原始路径
		return exec.Command("explorer", dir).Start()
	case "darwin":
		return exec.Command("open", dir).Start()
	default:
		return exec.Command("xdg-open", dir).Start()
	}
}

// openURL 用系统默认浏览器打开一个网址（模型卡上的「官网」）。
//
// 只认 http/https：模型清单是可以被用户随手改的文本文件，别让它变成
// 「点一下就执行任意命令」的口子（`file://` / `javascript:` 一律拒掉）。
func openURL(raw string) error {
	target := strings.TrimSpace(raw)
	if target == "" {
		return errors.New("这个模型没有官网地址")
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return errors.New("网址不合法：" + target)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("只支持 http/https 链接")
	}
	switch runtime.GOOS {
	case "windows":
		// rundll32 比 `cmd /c start` 少一层转义，也不弹黑框
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	case "darwin":
		return exec.Command("open", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}
