package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// fileFilter 是文件对话框里的一条类型过滤。
type fileFilter struct {
	// Label 是给用户看的名字，例如「图片」。
	Label string
	// Pattern 是分号分隔的匹配串，例如 "*.png;*.jpg"。
	Pattern string
}

// 输入文件对话框认的格式：图片 + PDF 混选。
var inputFilters = []fileFilter{
	{Label: "图片与 PDF", Pattern: "*.png;*.jpg;*.jpeg;*.bmp;*.webp;*.tif;*.tiff;*.pdf"},
	{Label: "图片", Pattern: "*.png;*.jpg;*.jpeg;*.bmp;*.webp;*.tif;*.tiff"},
	{Label: "PDF", Pattern: "*.pdf"},
	{Label: "所有文件", Pattern: "*.*"},
}

// excelFilter 是导出 Excel 的保存对话框过滤。
var excelFilter = []fileFilter{
	{Label: "Excel 工作簿", Pattern: "*.xlsx"},
	{Label: "所有文件", Pattern: "*.*"},
}

// pickedFile 是用户选进来的一个输入文件。
type pickedFile struct {
	Path string
	Name string
	Size int64
	// Kind 是 image / pdf。
	Kind string
}

// fileKind 按扩展名判断输入类型。
func fileKind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf":
		return "pdf"
	case ".png", ".jpg", ".jpeg", ".bmp", ".webp", ".tif", ".tiff", ".gif":
		return "image"
	default:
		return ""
	}
}

// newPickedFile 把路径变成一条输入记录（文件不在就直接报错）。
func newPickedFile(path string) (pickedFile, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	info, err := os.Stat(abs)
	if err != nil {
		return pickedFile{}, fmt.Errorf("找不到文件 %s", path)
	}
	if info.IsDir() {
		return pickedFile{}, fmt.Errorf("%s 是目录，只能选图片或 PDF 文件", path)
	}
	kind := fileKind(abs)
	if kind == "" {
		return pickedFile{}, fmt.Errorf("%s 不是支持的格式（只支持图片与 PDF）", filepath.Base(abs))
	}
	return pickedFile{Path: abs, Name: filepath.Base(abs), Size: info.Size(), Kind: kind}, nil
}

// errNoDialog 说明这台机器上找不到可用的文件对话框。
var errNoDialog = fmt.Errorf("这台机器上没有可用的文件选择对话框，请在「手动添加路径」里粘贴完整路径")
