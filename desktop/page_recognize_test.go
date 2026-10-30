package main

import "testing"

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
