package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oneo-me/nsgo/nsgotest"
)

// TestGroupBySource 多文件一次识别时按来源文件分 tab。
func TestGroupBySource(t *testing.T) {
	groups := groupBySource([]ResultPage{
		{Index: 0, Source: "01-invoice.png"},
		{Index: 1, Source: "02-report.pdf"},
		{Index: 2, Source: "02-report.pdf"},
	})
	if len(groups) != 2 {
		t.Fatalf("应该分成 2 组，得到 %d", len(groups))
	}
	if groups[0].label != "invoice.png" || groups[1].label != "report.pdf" {
		t.Errorf("标签没去掉序号前缀：%q / %q", groups[0].label, groups[1].label)
	}
	if len(groups[1].pages) != 2 {
		t.Errorf("PDF 的两页应该在同一组，得到 %d", len(groups[1].pages))
	}
}

// TestGroupBySourceLegacy 老结果没有 source 字段时归成一组「全部页」。
func TestGroupBySourceLegacy(t *testing.T) {
	groups := groupBySource([]ResultPage{{Index: 0}, {Index: 1}})
	if len(groups) != 1 || groups[0].label != "全部页" || len(groups[0].pages) != 2 {
		t.Fatalf("老结果分组不对：%+v", groups)
	}
}

// TestDisplaySource 副本名还原成人看得懂的名字。
func TestDisplaySource(t *testing.T) {
	cases := map[string]string{
		"02-report.pdf":         "report.pdf",
		"input.png":             "input.png",
		"":                      "",
		"/tmp/out/07-scan.jpeg": "scan.jpeg",
		"07-":                   "07-",
	}
	for in, want := range cases {
		if got := displaySource(in); got != want {
			t.Errorf("displaySource(%q) = %q，想要 %q", in, got, want)
		}
	}
}

// TestFileKind 只把图片与 PDF 当输入。
func TestFileKind(t *testing.T) {
	cases := map[string]string{
		"/a/b.PNG": "image", "/a/b.jpeg": "image", "/a/b.pdf": "pdf",
		"/a/b.docx": "", "/a/b.zip": "",
	}
	for in, want := range cases {
		if got := fileKind(in); got != want {
			t.Errorf("fileKind(%q) = %q，想要 %q", in, got, want)
		}
	}
}

// TestNewPickedFileRejectsMissing 选到不存在的文件要给出能照着做的错误。
func TestNewPickedFileRejectsMissing(t *testing.T) {
	if _, err := newPickedFile("/definitely/not/here.png"); err == nil {
		t.Fatal("不存在的文件应该报错")
	}
}

// writeTinyPNG 写一张 1x1 的合法 PNG，给文件列表用例当输入。
func writeTinyPNG(t *testing.T, path string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("建测试图片失败：%v", err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatalf("写测试图片失败：%v", err)
	}
	return path
}

// TestRemoveInputFromList 识别页选中的文件要能一个个删掉。
//
// 以前名字按钮写死 Width(100%)，把行尾的 ✕ 挤到可视区外——用户看到的就是
// 「选进来的文件删不掉」，所以这里连位置一起验。
func TestRemoveInputFromList(t *testing.T) {
	dir := t.TempDir()
	first := writeTinyPNG(t, filepath.Join(dir, "a.png"))
	second := writeTinyPNG(t, filepath.Join(dir, "b.png"))
	t.Setenv("OCR_TEST_INPUTS", strings.Join([]string{first, second}, string(os.PathListSeparator)))

	server := fakeServer(t)
	defer server.Close()
	t.Setenv("OCR_API", server.URL)
	t.Setenv("OCR_NO_SPAWN", "1")

	app := nsgotest.Start(t, Root, nsgotest.Size(1200, 840))
	app.Find("id:recognize-file-a.png")
	app.Find("id:recognize-file-b.png")

	remove := app.Find("id:recognize-remove-a.png")
	if remove.Frame.Size.Width <= 0 {
		t.Fatalf("✕ 宽度是 %v，点不到", remove.Frame.Size.Width)
	}
	if right := remove.Offset.X + remove.Frame.Size.Width; right > 1200 {
		t.Fatalf("✕ 被挤到窗口外了：right=%v", right)
	}

	app.Press("id:recognize-remove-a.png")
	app.Settle()
	if app.Exists("id:recognize-file-a.png") {
		t.Error("点了 ✕ 之后 a.png 应该从列表里消失")
	}
	app.Find("id:recognize-file-b.png")
}
