package xlsx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

// TestBuildReadableWorkbook 打开自己生成的工作簿：必须是合法 zip，每个 XML 部件都能解析。
func TestBuildReadableWorkbook(t *testing.T) {
	confidence := 0.987
	data, err := Build([]Sheet{
		{
			Name:         "分块",
			FreezeHeader: true,
			Columns:      []Column{{Title: "文件", Width: 24}, {Title: "置信度"}},
			Rows: [][]Cell{
				{Text("01-invoice.png"), Number(confidence)},
				{Text("a<b&c"), Text("")},
			},
		},
		{Name: "全文", Columns: []Column{{Title: "文本"}}, Rows: [][]Cell{{Text("with\nnewline")}}},
	})
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("产物不是合法 zip：%v", err)
	}
	want := []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"xl/workbook.xml",
		"xl/_rels/workbook.xml.rels",
		"xl/styles.xml",
		"xl/worksheets/sheet1.xml",
		"xl/worksheets/sheet2.xml",
	}
	got := map[string]bool{}
	for _, file := range reader.File {
		got[file.Name] = true
		body, err := readAll(file)
		if err != nil {
			t.Fatalf("读 %s 失败：%v", file.Name, err)
		}
		decoder := xml.NewDecoder(strings.NewReader(body))
		for {
			if _, err := decoder.Token(); err != nil {
				if err == io.EOF {
					break
				}
				t.Fatalf("%s 不是合法 XML：%v", file.Name, err)
			}
		}
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("少了部件 %s", name)
		}
	}
}

// TestEscapeDropsControlChars 识别结果里混进控制符时不能让 Excel 打不开整份文件。
func TestEscapeDropsControlChars(t *testing.T) {
	if out := escape("a\x01b<c>&"); out != "ab&lt;c&gt;&amp;" {
		t.Errorf("转义结果不对：%q", out)
	}
}

// TestSheetNameLimits Excel 表名不能超过 31 字符、不能带 []:*?/\。
func TestSheetNameLimits(t *testing.T) {
	long := sheetName(strings.Repeat("长", 40), 0)
	if len([]rune(long)) != 31 {
		t.Errorf("表名没截到 31 字符：%d", len([]rune(long)))
	}
	if got := sheetName("a/b:c*d?e[f]g\\h", 0); strings.ContainsAny(got, `[]:*?/\`) {
		t.Errorf("表名还留着非法字符：%q", got)
	}
	if got := sheetName("   ", 2); got != "Sheet3" {
		t.Errorf("空表名应回退成 SheetN，得到 %q", got)
	}
}

// TestColumnName 列号转字母要能跨过 Z。
func TestColumnName(t *testing.T) {
	cases := map[int]string{0: "A", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA"}
	for in, want := range cases {
		if got := columnName(in); got != want {
			t.Errorf("columnName(%d) = %q，想要 %q", in, got, want)
		}
	}
}

func readAll(file *zip.File) (string, error) {
	reader, err := file.Open()
	if err != nil {
		return "", err
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
