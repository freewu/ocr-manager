// Package xlsx 生成最小可用的 Excel 工作簿（.xlsx），只用标准库。
//
// 为什么不引第三方库：整个项目的第三方依赖只有 BurntSushi/toml，导个表格不值得
// 再拖一个 OOXML 实现进来。xlsx 是个 zip 包，里面几份 XML：内容类型、关系、
// workbook、worksheet、styles。这里只生成这些东西，够 Excel / WPS / Numbers 打开。
//
// 单元格全部用 inlineStr（共享字符串表能省体积，但生成逻辑会复杂一倍）；数字
// 单独走数值单元格，这样置信度还能在 Excel 里排序。
package xlsx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
)

// Cell 是一个单元格：文本或数字（数字为空时按文本写）。
type Cell struct {
	Text   string
	Number *float64
}

// Text 建一个文本单元格。
func Text(s string) Cell { return Cell{Text: s} }

// Number 建一个数值单元格。
func Number(f float64) Cell { return Cell{Number: &f} }

// Column 是一列：表头 + 列宽（0 用默认宽度）。
type Column struct {
	Title string
	Width float64
}

// Sheet 是一张表。
type Sheet struct {
	Name string
	// Columns 给出表头与列宽。
	Columns []Column
	// Rows 是数据行，短的自动补空。
	Rows [][]Cell
	// FreezeHeader 冻结首行（长表格滚动时表头不跑）。
	FreezeHeader bool
}

// Build 把若干张表打成一个工作簿。
func Build(sheets []Sheet) ([]byte, error) {
	if len(sheets) == 0 {
		return nil, fmt.Errorf("没有要导出的表格")
	}
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)

	files := []struct {
		name string
		body string
	}{
		{"[Content_Types].xml", contentTypes(len(sheets))},
		{"_rels/.rels", rootRels()},
		{"xl/workbook.xml", workbook(sheets)},
		{"xl/_rels/workbook.xml.rels", workbookRels(len(sheets))},
		{"xl/styles.xml", stylesXML},
	}
	for i, sheet := range sheets {
		files = append(files, struct {
			name string
			body string
		}{fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), worksheet(sheet, i == 0)})
	}
	for _, file := range files {
		w, err := zw.Create(file.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(file.body)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const header = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

func contentTypes(sheets int) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	b.WriteString(`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`)
	for i := 1; i <= sheets; i++ {
		b.WriteString(fmt.Sprintf(`<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i))
	}
	b.WriteString(`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	b.WriteString(`</Types>`)
	return b.String()
}

func rootRels() string {
	return header + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
		`</Relationships>`
}

func workbook(sheets []Sheet) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	for i, sheet := range sheets {
		name := sheet.Name
		if strings.TrimSpace(name) == "" {
			name = fmt.Sprintf("Sheet%d", i+1)
		}
		b.WriteString(fmt.Sprintf(`<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, escape(sheetName(name, i)), i+1, i+1))
	}
	b.WriteString(`</sheets></workbook>`)
	return b.String()
}

func workbookRels(sheets int) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := 1; i <= sheets; i++ {
		b.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i, i))
	}
	b.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`, sheets+1))
	b.WriteString(`</Relationships>`)
	return b.String()
}

// stylesXML 只有两种格式：0 普通、1 表头加粗。
const stylesXML = header + `<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
	`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>` +
	`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
	`<borders count="1"><border/></borders>` +
	`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
	`<cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
	`<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/></cellXfs>` +
	`</styleSheet>`

func worksheet(sheet Sheet, first bool) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)

	if widths := columnWidths(sheet.Columns); widths != "" {
		b.WriteString("<cols>" + widths + "</cols>")
	}
	if sheet.FreezeHeader && len(sheet.Columns) > 0 {
		b.WriteString(`<sheetViews><sheetView workbookViewId="0">` +
			`<pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/>` +
			`</sheetView></sheetViews>`)
	}
	b.WriteString("<sheetData>")

	if len(sheet.Columns) > 0 {
		b.WriteString(rowXML(1, headerCells(sheet.Columns)))
	}
	for i, cells := range sheet.Rows {
		b.WriteString(rowXML(i+2, cells))
	}
	b.WriteString("</sheetData></worksheet>")
	_ = first
	return b.String()
}

func columnWidths(columns []Column) string {
	var b strings.Builder
	for i, column := range columns {
		width := column.Width
		if width <= 0 {
			width = 16
		}
		b.WriteString(fmt.Sprintf(`<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, i+1, i+1, width))
	}
	return b.String()
}

func headerCells(columns []Column) []Cell {
	out := make([]Cell, len(columns))
	for i, column := range columns {
		out[i] = Text(column.Title)
	}
	return out
}

func rowXML(n int, cells []Cell) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<row r="%d">`, n))
	style := ""
	if n == 1 {
		style = ` s="1"`
	}
	for i, cell := range cells {
		ref := fmt.Sprintf("%s%d", columnName(i), n)
		if cell.Number != nil {
			b.WriteString(fmt.Sprintf(`<c r="%s"%s><v>%s</v></c>`, ref, style, formatNumber(*cell.Number)))
			continue
		}
		if cell.Text == "" {
			continue // 空格子不写，文件小一半
		}
		b.WriteString(fmt.Sprintf(`<c r="%s"%s t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, escape(cell.Text)))
	}
	b.WriteString(`</row>`)
	return b.String()
}

// columnName 把 0 起的列号转成 A / Z / AA。
func columnName(i int) string {
	name := ""
	for i >= 0 {
		name = string(rune('A'+i%26)) + name
		i = i/26 - 1
	}
	return name
}

func formatNumber(f float64) string {
	text := fmt.Sprintf("%g", f)
	if strings.Contains(text, "e") || strings.Contains(text, "E") {
		text = fmt.Sprintf("%.6f", f)
	}
	return text
}

// escape 做 XML 转义，并扔掉 XML 1.0 不接受的字符（识别结果里偶尔混进控制符，
// Excel 会因为一个 0x01 拒绝打开整份文件）。
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		case '\r':
			// Excel 里换行靠 \n，\r\n 会变成两个空行
		default:
			if r == '\t' || r == '\n' || r >= 0x20 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// sheetName 处理 Excel 对表名的限制：不超过 31 字符，不能有 []:*?/\。
func sheetName(name string, index int) string {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '[', ']', ':', '*', '?', '/', '\\':
			return '-'
		}
		return r
	}, strings.TrimSpace(name))
	cleaned = strings.Trim(cleaned, "'")
	if cleaned == "" {
		return fmt.Sprintf("Sheet%d", index+1)
	}
	runes := []rune(cleaned)
	if len(runes) > 31 {
		cleaned = string(runes[:31])
	}
	return cleaned
}
