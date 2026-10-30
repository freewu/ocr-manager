// Package history 把每次识别的输入副本、结果 JSON、Markdown / 纯文本落到产物目录。
//
//	<out>/<job-id>/
//	  meta.json      索引信息（列表页只读它）
//	  input.png      输入副本（PDF 也是原样一份）
//	  result.json    完整结果（页 / 块 / 坐标 / 置信度）
//	  result.md      给用户直接复制的 Markdown
//	  result.txt     纯文本
package history

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bluefrog/ocr-manager/internal/paths"
	"github.com/bluefrog/ocr-manager/internal/xlsx"
)

// 任务状态。
const (
	StatusDone     = "done"
	StatusError    = "error"
	StatusCanceled = "canceled"
)

// Entry 是 meta.json 的内容，也是列表页的一行。
type Entry struct {
	ID         string         `json:"id"`
	Model      string         `json:"model"`
	ModelName  string         `json:"model_name"`
	Engine     string         `json:"engine"`
	Status     string         `json:"status"`
	Error      string         `json:"error,omitempty"`
	SourceName string         `json:"source_name"`
	SourceKind string         `json:"source_kind"` // image | pdf
	InputFile  string         `json:"input_file"`
	Bytes      int64          `json:"bytes"`
	CreatedAt  int64          `json:"created_at"`
	DurationMS int64          `json:"duration_ms"`
	Pages      int            `json:"pages"`
	Blocks     int            `json:"blocks"`
	Chars      int            `json:"chars"`
	Width      int            `json:"width"`
	Height     int            `json:"height"`
	Preview    string         `json:"preview"`
	Options    map[string]any `json:"options,omitempty"`
}

// Result 是 result.json 的最小形状（Go 侧导出 / 统计用）。
type Result struct {
	Engine     string          `json:"engine"`
	Pages      []Page          `json:"pages"`
	Meta       json.RawMessage `json:"meta,omitempty"`
	DurationMS int64           `json:"duration_ms"`
}

// Page 是识别结果的一页。
type Page struct {
	Index int `json:"index"`
	// Source 是这一页来自哪个输入文件（产物目录里的副本名，如 01-page.png）。
	// 多文件一次识别时前端靠它分 tab；老结果没有这个字段，就当全部来自一个文件。
	Source   string  `json:"source,omitempty"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Text     string  `json:"text"`
	Markdown string  `json:"markdown"`
	Blocks   []Block `json:"blocks"`
}

// Block 是页面里的一块内容。
type Block struct {
	Text       string      `json:"text"`
	Kind       string      `json:"kind"`
	Box        [][]float64 `json:"box"`
	Confidence *float64    `json:"confidence"`
}

// Dir 返回某个任务的产物目录。
func Dir(p *paths.Paths, jobID string) string { return p.JobDir(cleanID(jobID)) }

// SaveInput 把上传/本地文件复制进产物目录，返回相对文件名、字节数与扩展名。
//
// 副本名形如 `02-invoice.pdf`：序号保证多文件一次识别时不会互相覆盖（以前所有
// 文件都存成 input.png / input.pdf，第二个就把第一个盖掉了），文件名留着方便
// 用户在产物目录里认人。
func SaveInput(p *paths.Paths, jobID string, index int, sourcePath, sourceName string) (string, int64, error) {
	dir := Dir(p, jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	name := fmt.Sprintf("%02d-%s", index+1, inputName(sourcePath, sourceName))

	src, err := os.Open(sourcePath)
	if err != nil {
		return "", 0, err
	}
	defer src.Close()

	dst, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return "", 0, err
	}
	written, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return "", 0, copyErr
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	return name, written, nil
}

// Write 落盘 meta / result / md / txt（先写临时文件再 rename，避免读到半份）。
func Write(p *paths.Paths, entry Entry, result Result) error {
	dir := Dir(p, entry.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	if err := writeJSON(filepath.Join(dir, "result.json"), result); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "result.md"), []byte(Markdown(result)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "result.txt"), []byte(Plain(result)), 0o644); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, "meta.json"), entry)
}

// WriteMeta 只更新 meta.json（任务跑完但结果没变时用）。
func WriteMeta(p *paths.Paths, entry Entry) error {
	return writeJSON(filepath.Join(Dir(p, entry.ID), "meta.json"), entry)
}

// Markdown 把结果拼成一份连续的 Markdown。
func Markdown(result Result) string {
	parts := make([]string, 0, len(result.Pages)+2)
	for index, page := range result.Pages {
		body := strings.TrimSpace(page.Markdown)
		if body == "" {
			body = strings.TrimSpace(page.Text)
		}
		if body == "" {
			continue
		}
		if len(result.Pages) > 1 {
			parts = append(parts, fmt.Sprintf("<!-- page %d -->", index+1))
		}
		parts = append(parts, body)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n") + "\n"
}

// Plain 把结果拼成纯文本（多页之间用分隔线）。
func Plain(result Result) string {
	parts := make([]string, 0, len(result.Pages))
	for index, page := range result.Pages {
		body := strings.TrimSpace(page.Text)
		if body == "" {
			body = strings.TrimSpace(page.Markdown)
		}
		if body == "" {
			continue
		}
		if len(result.Pages) > 1 {
			parts = append(parts, fmt.Sprintf("===== 第 %d 页 =====", index+1))
		}
		parts = append(parts, body)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n") + "\n"
}

// List 返回最近的 n 条记录（按创建时间倒序）。
func List(p *paths.Paths, limit int) []Entry {
	root := p.OutDir()
	dirs, err := os.ReadDir(root)
	if err != nil {
		return []Entry{}
	}
	out := make([]Entry, 0, len(dirs))
	for _, dir := range dirs {
		if !dir.IsDir() || strings.HasPrefix(dir.Name(), ".") {
			continue
		}
		entry, err := readEntry(filepath.Join(root, dir.Name(), "meta.json"))
		if err != nil {
			continue
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt > out[b].CreatedAt })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Get 读一条记录 + 完整结果。
func Get(p *paths.Paths, jobID string) (Entry, Result, error) {
	dir := Dir(p, jobID)
	entry, err := readEntry(filepath.Join(dir, "meta.json"))
	if err != nil {
		return Entry{}, Result{}, fmt.Errorf("找不到记录 %s", jobID)
	}
	var result Result
	raw, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err == nil {
		if err := json.Unmarshal(raw, &result); err != nil {
			return entry, Result{}, fmt.Errorf("结果文件损坏: %w", err)
		}
	}
	return entry, result, nil
}

// Delete 删掉一条记录（连同输入副本）。
func Delete(p *paths.Paths, jobID string) error {
	dir := Dir(p, jobID)
	if !paths.IsInsideDir(p.OutDir(), dir) || dir == p.OutDir() {
		return fmt.Errorf("非法的记录 id：%s", jobID)
	}
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
		return fmt.Errorf("找不到记录 %s", jobID)
	}
	return os.RemoveAll(dir)
}

// File 返回记录目录里的一个文件（限制在白名单里，防目录穿越）。
func File(p *paths.Paths, jobID, name string) (string, error) {
	switch name {
	case "input", "result.json", "result.md", "result.txt", "meta.json":
	default:
		return "", fmt.Errorf("不支持的文件：%s", name)
	}
	dir := Dir(p, jobID)
	target := filepath.Join(dir, name)
	if name == "input" {
		matches, _ := filepath.Glob(filepath.Join(dir, "input.*"))
		if len(matches) == 0 {
			return "", fmt.Errorf("没有输入文件")
		}
		target = matches[0]
	}
	if !paths.IsInsideDir(dir, target) {
		return "", fmt.Errorf("非法路径")
	}
	if _, err := os.Stat(target); err != nil {
		return "", fmt.Errorf("文件不存在")
	}
	return target, nil
}

// Summary 统计页数与块数（识别完成后回填 meta）。
func Summary(result Result) (pages, blocks, chars int, preview string) {
	pages = len(result.Pages)
	var first string
	for _, page := range result.Pages {
		blocks += len(page.Blocks)
		chars += len([]rune(page.Text))
		if first == "" {
			first = strings.TrimSpace(page.Text)
		}
	}
	runes := []rune(first)
	if len(runes) > 140 {
		first = string(runes[:140]) + "…"
	}
	first = strings.ReplaceAll(first, "\n", " ")
	return pages, blocks, chars, first
}

// NowMilli 是当前毫秒时间戳（Go 1.17+ 也行，统一走这里）。
func NowMilli() int64 { return time.Now().UnixMilli() }

func readEntry(file string) (Entry, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return Entry{}, err
	}
	var entry Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func writeJSON(file string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

func cleanID(jobID string) string {
	cleaned := strings.TrimSpace(jobID)
	cleaned = strings.ReplaceAll(cleaned, "\\", "")
	cleaned = strings.ReplaceAll(cleaned, "/", "")
	cleaned = strings.ReplaceAll(cleaned, "..", "")
	if cleaned == "" {
		return "unknown"
	}
	return cleaned
}

// ExportXLSX 把一次识别的结果导成 Excel：一张「分块」表（一行一块，带坐标与
// 置信度）+ 一张「全文」表（一页一行，纯文本与 Markdown）。
func ExportXLSX(p *paths.Paths, jobID string) ([]byte, error) {
	entry, result, err := Get(p, cleanID(jobID))
	if err != nil {
		return nil, err
	}
	blocks := xlsx.Sheet{
		Name:         "分块",
		FreezeHeader: true,
		Columns: []xlsx.Column{
			{Title: "文件", Width: 26},
			{Title: "页", Width: 6},
			{Title: "序号", Width: 6},
			{Title: "类型", Width: 10},
			{Title: "置信度", Width: 9},
			{Title: "坐标", Width: 30},
			{Title: "文本", Width: 60},
		},
	}
	pages := xlsx.Sheet{
		Name:         "全文",
		FreezeHeader: true,
		Columns: []xlsx.Column{
			{Title: "文件", Width: 26},
			{Title: "页", Width: 6},
			{Title: "字数", Width: 8},
			{Title: "纯文本", Width: 60},
			{Title: "Markdown", Width: 60},
		},
	}

	for i, page := range result.Pages {
		source := DisplaySource(page.Source)
		if source == "" {
			source = DisplaySource(entry.InputFile)
		}
		pageNo := page.Index
		if pageNo <= 0 {
			pageNo = i + 1
		}
		pages.Rows = append(pages.Rows, []xlsx.Cell{
			xlsx.Text(source),
			xlsx.Number(float64(pageNo)),
			xlsx.Number(float64(len([]rune(page.Text)))),
			xlsx.Text(page.Text),
			xlsx.Text(page.Markdown),
		})
		for j, block := range page.Blocks {
			kind := block.Kind
			if kind == "" {
				kind = "text"
			}
			cells := []xlsx.Cell{
				xlsx.Text(source),
				xlsx.Number(float64(pageNo)),
				xlsx.Number(float64(j + 1)),
				xlsx.Text(kind),
			}
			if block.Confidence != nil {
				cells = append(cells, xlsx.Number(*block.Confidence))
			} else {
				cells = append(cells, xlsx.Text(""))
			}
			cells = append(cells, xlsx.Text(boxText(block.Box)), xlsx.Text(block.Text))
			blocks.Rows = append(blocks.Rows, cells)
		}
	}
	if len(blocks.Rows) == 0 && len(pages.Rows) == 0 {
		return nil, fmt.Errorf("这次识别没有结果可导出")
	}
	return xlsx.Build([]xlsx.Sheet{blocks, pages})
}

// DisplaySource 把产物目录里的副本名（02-invoice.pdf）还原成用户看得懂的名字。
func DisplaySource(name string) string {
	if name == "" {
		return ""
	}
	base := filepath.Base(name)
	// 去掉 SaveInput 加的两位序号前缀
	if len(base) > 3 && base[2] == '-' {
		if _, err := strconv.Atoi(base[:2]); err == nil {
			return base[3:]
		}
	}
	return base
}

// boxText 把四边形坐标压成一行，导出的表格里看得懂就行。
func boxText(box [][]float64) string {
	if len(box) == 0 {
		return ""
	}
	parts := make([]string, 0, len(box))
	for _, point := range box {
		if len(point) < 2 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%.0f,%.0f", point[0], point[1]))
	}
	return strings.Join(parts, " ")
}

// inputName 由原文件名得出安全的副本名：去路径、去怪字符、保留扩展名。
func inputName(sourcePath, sourceName string) string {
	name := filepath.Base(strings.TrimSpace(sourceName))
	if name == "" || name == "." {
		name = filepath.Base(sourcePath)
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(sourcePath))
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	stem = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '-'
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, stem)
	stem = strings.Trim(stem, " .-")
	if stem == "" {
		stem = "input"
	}
	if len([]rune(stem)) > 40 {
		stem = string([]rune(stem)[:40])
	}
	if ext == "" {
		ext = ".bin"
	}
	return stem + ext
}
