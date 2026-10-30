//go:build !windows

package main

import (
	"os/exec"
	"runtime"
	"strings"
)

// 非 Windows 上借系统自带工具弹对话框。Linux 用 zenity（GNOME）或 kdialog
// （KDE），macOS 用 osascript。都没有就报错，让用户手动填路径 —— 反正这
// 台机器上也弹不出别的对话框。

// pickFiles 打开系统的多选文件对话框，返回选中的绝对路径（取消返回空）。
func pickFiles(title string, filters []fileFilter) ([]string, error) {
	if runtime.GOOS == "darwin" {
		return pickFilesDarwin(title)
	}
	if _, err := exec.LookPath("zenity"); err == nil {
		return pickFilesZenity(title, filters)
	}
	if _, err := exec.LookPath("kdialog"); err == nil {
		return pickFilesKDialog(title, filters)
	}
	return nil, errNoDialog
}

// pickSaveFile 打开保存对话框，返回目标路径（取消返回空）。
func pickSaveFile(title, defaultName string, filters []fileFilter) (string, error) {
	if runtime.GOOS == "darwin" {
		return pickSaveFileDarwin(title, defaultName)
	}
	if _, err := exec.LookPath("zenity"); err == nil {
		cmd := exec.Command("zenity", "--file-selection", "--save", "--confirm-overwrite",
			"--title="+title, "--filename="+defaultName, "--file-filter="+filterText(filters, "xlsx"))
		out, err := cmd.Output()
		return saveResult(out, err)
	}
	if _, err := exec.LookPath("kdialog"); err == nil {
		cmd := exec.Command("kdialog", "--title", title, "--getsavefilename", defaultName+"|*.xlsx")
		out, err := cmd.Output()
		return saveResult(out, err)
	}
	return "", errNoDialog
}

func saveResult(out []byte, err error) (string, error) {
	if err != nil {
		// zenity 点取消返回退出码 1，kdialog 也是
		return "", nil
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", nil
	}
	return path, nil
}

// filterText 把过滤条件拼成 zenity 的 "名字 | *.png *.jpg" 形式。
func filterText(filters []fileFilter, ext string) string {
	for _, filter := range filters {
		if strings.Contains(filter.Pattern, "*."+ext) {
			return filter.Label + " | " + strings.ReplaceAll(filter.Pattern, ";", " ")
		}
	}
	return "所有文件 | *"
}

func pickFilesZenity(title string, filters []fileFilter) ([]string, error) {
	args := []string{"--file-selection", "--multiple", "--separator=\n", "--title=" + title}
	for _, filter := range filters {
		args = append(args, "--file-filter="+filter.Label+" | "+strings.ReplaceAll(filter.Pattern, ";", " "))
	}
	out, err := exec.Command("zenity", args...).Output()
	if err != nil {
		return nil, nil // 取消
	}
	return splitLines(string(out)), nil
}

func pickFilesKDialog(title string, filters []fileFilter) ([]string, error) {
	args := []string{"--title", title, "--getopenfilename", ".", filterText(filters, "pdf") + "\n.*|所有文件"}
	out, err := exec.Command("kdialog", args...).Output()
	if err != nil {
		return nil, nil
	}
	// kdialog 多选返回 "file1 file2"（带引号的空格路径），处理引号即可
	return splitKDialog(strings.TrimSpace(string(out))), nil
}

func splitKDialog(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	current := strings.Builder{}
	inQuote := false
	for _, r := range text {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				out = append(out, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

func splitLines(text string) []string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// osascript 里选多个文件：choose file with multiple selections allowed →
// 每个 alias 转成 POSIX path，一行一个。
const pickFilesScript = `on run argv
	set theTitle to item 1 of argv
	set chosen to choose file with prompt theTitle with multiple selections allowed
	set out to ""
	repeat with f in chosen
		set out to out & POSIX path of f & linefeed
	end repeat
	return out
end run`

func pickFilesDarwin(title string) ([]string, error) {
	out, err := exec.Command("osascript", "-e", pickFilesScript, title).Output()
	if err != nil {
		return nil, nil
	}
	return splitLines(string(out)), nil
}

// pickSaveFileDarwin 用 osascript 的 choose file name 拿保存路径。
const pickSaveScript = `on run argv
	set theTitle to item 1 of argv
	set theName to item 2 of argv
	set target to choose file name with prompt theTitle default name theName
	return POSIX path of target
end run`

func pickSaveFileDarwin(title, defaultName string) (string, error) {
	out, err := exec.Command("osascript", "-e", pickSaveScript, title, defaultName).Output()
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}
