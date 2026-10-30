//go:build windows

package main

import (
	"strings"
	"syscall"
	"unsafe"
)

// 系统文件对话框。Windows 上直接用 comdlg32 的 GetOpenFileNameW /
// GetSaveFileNameW：调起来比引第三方依赖省事，也不给 exe 添体积。

var (
	comdlg32               = syscall.NewLazyDLL("comdlg32.dll")
	procGetOpenFileNameW   = comdlg32.NewProc("GetOpenFileNameW")
	procGetSaveFileNameW   = comdlg32.NewProc("GetSaveFileNameW")
	procCommDlgExtendedErr = comdlg32.NewProc("CommDlgExtendedError")
)

const (
	ofnOverwritePrompt  = 0x00000002
	ofnNoChangeDir      = 0x00000008
	ofnPathMustExist    = 0x00000800
	ofnFileMustExist    = 0x00001000
	ofnAllowMultiSelect = 0x00000200
	ofnExplorer         = 0x00080000
	ofnHideReadOnly     = 0x00000004
	ofnNoTestFileCreate = 0x00010000
	ofnLongNames        = 0x00200000
)

// openFileName 是 OPENFILENAMEW 结构体。字段顺序必须和 winuser.h 一致。
type openFileName struct {
	Size            uint32
	Owner           uintptr
	Instance        uintptr
	Filter          *uint16
	CustomFilter    *uint16
	MaxCustomFilter uint32
	FilterIndex     uint32
	File            *uint16
	MaxFile         uint32
	FileTitle       *uint16
	MaxFileTitle    uint32
	InitialDir      *uint16
	Title           *uint16
	Flags           uint32
	FileOffset      uint16
	FileExtension   uint16
	DefaultExt      *uint16
	CustData        uintptr
	Hook            uintptr
	TemplateName    *uint16
	Reserved        uintptr
	Reserved2       uint32
	FlagsEx         uint32
}

// pickFiles 打开系统的多选文件对话框，返回选中的绝对路径（取消返回空）。
func pickFiles(title string, filters []fileFilter) ([]string, error) {
	buffer := make([]uint16, 64*1024)
	filter := filterUTF16(filters)
	titleUTF16, _ := syscall.UTF16PtrFromString(title)
	filePtr := &buffer[0]

	ofn := openFileName{
		Size:        uint32(unsafe.Sizeof(openFileName{})),
		Filter:      filter,
		FilterIndex: 1,
		File:        filePtr,
		MaxFile:     uint32(len(buffer)),
		Title:       titleUTF16,
		Flags:       ofnExplorer | ofnAllowMultiSelect | ofnFileMustExist | ofnPathMustExist | ofnNoChangeDir | ofnHideReadOnly | ofnLongNames,
	}
	ok, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ok == 0 {
		// 取消是常态：CommDlgExtendedError 返回 0 表示用户点了取消
		if code := extendedError(); code != 0 {
			return nil, dialogError(code)
		}
		return nil, nil
	}
	return splitMultiSelect(buffer), nil
}

// pickSaveFile 打开保存对话框，返回目标路径（取消返回空）。
func pickSaveFile(title, defaultName string, filters []fileFilter) (string, error) {
	buffer := make([]uint16, 4096)
	seed := defaultName
	if seed != "" {
		copy(buffer, syscall.StringToUTF16(seed))
	}
	filter := filterUTF16(filters)
	titleUTF16, _ := syscall.UTF16PtrFromString(title)
	defExt, _ := syscall.UTF16PtrFromString("xlsx")

	ofn := openFileName{
		Size:        uint32(unsafe.Sizeof(openFileName{})),
		Filter:      filter,
		FilterIndex: 1,
		File:        &buffer[0],
		MaxFile:     uint32(len(buffer)),
		Title:       titleUTF16,
		Flags:       ofnExplorer | ofnOverwritePrompt | ofnPathMustExist | ofnNoChangeDir | ofnLongNames | ofnNoTestFileCreate,
		DefaultExt:  defExt,
	}
	ok, _, _ := procGetSaveFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ok == 0 {
		if code := extendedError(); code != 0 {
			return "", dialogError(code)
		}
		return "", nil
	}
	return syscall.UTF16ToString(buffer), nil
}

// filterUTF16 把过滤条件拼成对话框要的 "名字\x00匹配串\x00...\x00\x00"。
func filterUTF16(filters []fileFilter) *uint16 {
	parts := make([]uint16, 0, 64)
	for _, filter := range filters {
		label, _ := syscall.UTF16FromString(filter.Label + " (" + strings.ReplaceAll(filter.Pattern, ";", ", ") + ")")
		// UTF16FromString 自带结尾 0，直接接上匹配串
		parts = append(parts, label...)
		pattern, _ := syscall.UTF16FromString(filter.Pattern)
		parts = append(parts, pattern...)
	}
	if len(parts) == 0 {
		return nil
	}
	parts = append(parts, 0)
	return &parts[0]
}

// splitMultiSelect 还原多选结果的路径列表。
//
// 选了一个文件时缓冲区就是一整条路径；选了多个时是「目录\0文件名\0文件名\0\0」。
func splitMultiSelect(buffer []uint16) []string {
	parts := make([]string, 0, 8)
	start := 0
	for i, value := range buffer {
		if value != 0 {
			continue
		}
		if i > start {
			parts = append(parts, string(syscall.UTF16ToString(buffer[start:i])))
		}
		start = i + 1
		if start >= len(buffer) {
			break
		}
		if buffer[start] == 0 {
			break
		}
	}
	if len(parts) == 0 {
		return nil
	}
	if len(parts) == 1 {
		return []string{parts[0]}
	}
	dir := parts[0]
	out := make([]string, 0, len(parts)-1)
	for _, name := range parts[1:] {
		if strings.ContainsAny(name, `:\/`) {
			// 已经是完整路径，别再拼目录
			out = append(out, name)
			continue
		}
		out = append(out, dir+`\`+name)
	}
	return out
}

func extendedError() uint32 {
	code, _, _ := procCommDlgExtendedErr.Call()
	return uint32(code)
}

func dialogError(code uint32) error {
	switch code {
	case 0xFFFF:
		return nil // 内存不足之类的边缘情况，当取消处理
	case 0x3002:
		return errNoDialog
	default:
		return errNoDialog
	}
}
