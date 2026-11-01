package main

import (
	"fmt"
	"github.com/oneo-me/nsgo/base"
	"strings"

	"github.com/oneo-me/nsgo/core"
)

// historyPage 是「历史记录」页：左列记录，右边详情。
func (u *ui) historyPage() core.Node {
	items := u.data().history
	actions := []core.Node{
		u.button(fixed("刷新"), buttonQuiet, nil, u.refresh),
	}

	list := make([]core.Node, 0, len(items))
	for _, item := range items {
		list = append(list, u.historyRow(item))
	}
	if len(list) == 0 {
		list = append(list, u.card(12, u.muted(fixed("还没有记录：先跑一次识别。"), 12)))
	}

	left := core.View{
		StyleFunc: func() core.Style {
			p := u.pal()
			return core.Style{
				Width:        core.Pt(340),
				Height:       core.Pct(100),
				Gap:          8,
				Padding:      core.All(10),
				CornerRadius: 12,
				BorderWidth:  1,
				BorderColor:  p.Border,
				Background:   p.Surface,
			}
		},
		Children: []core.Node{
			row(8,
				u.strong(fixed("记录"), 13, func() core.Color { return u.pal().Text }),
				fill(u.muted(func() string { return fmt.Sprintf("%d 条", len(items)) }, 11)),
			),
			core.Scroll{
				// HideIndicator：见 ui.go 里 scrollList 的注释（NSGo 拖滚动条会 panic）
				HideIndicator: true,
				Style:         core.Style{FlexGrow: 1, Width: core.Pct(100)},
				Children:      []core.Node{core.View{Style: core.Style{Gap: 6}, Children: list}},
			},
		},
	}

	right := core.View{
		Style:    core.Style{FlexGrow: 1, FlexShrink: 1, Height: core.Pct(100)},
		Children: []core.Node{u.historyDetail()},
	}

	return u.pageRoot("历史记录", "每次识别的输入副本、result.json / .md / .txt 都存在产物目录里", actions,
		core.View{
			Style:    core.Style{Direction: core.Row, Gap: 12, FlexGrow: 1, Height: core.Pct(100)},
			Children: []core.Node{left, right},
		},
	)
}

// historyRow 是左列的一条记录。
func (u *ui) historyRow(e HistoryEntry) core.Node {
	id := e.ID
	selected := func() bool { return u.histSel.Get() == id }
	return core.View{
		StyleFunc: func() core.Style {
			p := u.pal()
			s := core.Style{
				Padding:      core.Symmetric(8, 10),
				CornerRadius: 9,
				BorderWidth:  1,
				BorderColor:  p.Border,
				Gap:          3,
			}
			if selected() {
				s.BorderColor = p.Accent
				s.Background = p.Accent.Faded(0.12)
			}
			return s
		},
		Children: []core.Node{base.Button{
			OnPress: func(core.PressEvent) { u.selectHistory(id) },
			Style:   core.Style{Padding: core.All(0)},
			Children: []core.Node{
				row(6,
					core.View{Style: core.Style{Width: core.Pt(6), Height: core.Pt(6), CornerRadius: 3, Background: statusColor(u, e.Status)}},
					fill(u.text(fixed(e.SourceName), 12, func() core.Color { return u.pal().Text })),
					u.chip(fixed(e.SourceKind), func() core.Color { return u.pal().TextMuted }),
				),
				u.muted(func() string {
					text := fmt.Sprintf("%d 页 · %d 字", e.Pages, e.Chars)
					if e.Status != "done" {
						text = statusText(e.Status)
					}
					return text + " · " + clockText(e.CreatedAt)
				}, 11),
			},
		}},
	}
}

func statusColor(u *ui, status string) core.Color {
	switch status {
	case "done":
		return u.pal().Success
	case "error":
		return u.pal().Danger
	default:
		return u.pal().Warning
	}
}

func statusText(status string) string {
	switch status {
	case "done":
		return "完成"
	case "error":
		return "失败"
	case "canceled":
		return "已取消"
	case "running":
		return "进行中"
	default:
		return status
	}
}

// historyDetail 是右边的详情面板。
func (u *ui) historyDetail() core.Node {
	d := u.data().detail
	if d == nil {
		return u.card(16, u.muted(fixed("在左边选一条记录看详情。"), 12))
	}
	entry := d.entry
	id := entry.ID

	rows := []core.Node{
		row(10,
			fill(u.strong(fixed(entry.SourceName), 16, func() core.Color { return u.pal().Text })),
			u.chip(fixed(entry.ModelName), func() core.Color { return u.pal().Accent }),
			u.chip(fixed(statusText(entry.Status)), func() core.Color { return statusColor(u, entry.Status) }),
		),
		u.muted(func() string {
			return fmt.Sprintf("%s · %s · %s", entry.ID, clockText(entry.CreatedAt), entry.Engine)
		}, 11),
		u.divider(),
		row(20,
			core.View{Style: core.Style{FlexGrow: 1, Gap: 6}, Children: []core.Node{
				u.kv("输入", fixed(entry.InputFile)),
				u.kv("大小", fixed(fmtBytes(uint64(entry.Bytes)))),
				u.kv("页数", fixed(fmt.Sprintf("%d", entry.Pages))),
			}},
			core.View{Style: core.Style{FlexGrow: 1, Gap: 6}, Children: []core.Node{
				u.kv("分块", fixed(fmt.Sprintf("%d", entry.Blocks))),
				u.kv("字数", fixed(fmt.Sprintf("%d", entry.Chars))),
				u.kv("耗时", fixed(fmt.Sprintf("%.2fs", float64(entry.DurationMS)/1000))),
			}},
		),
	}

	if entry.Error != "" {
		rows = append(rows, u.text(fixed(entry.Error), 12, func() core.Color { return u.pal().Danger }))
	}

	rows = append(rows, u.divider())

	// 结果预览：Markdown / 纯文本切换，取第一页
	pageText := ""
	if len(d.result.Pages) > 0 {
		page := d.result.Pages[0]
		pageText = page.Markdown
		if pageText == "" {
			pageText = page.Text
		}
	}
	rows = append(rows, row(8,
		u.muted(fixed("结果预览"), 11),
		fill(u.muted(fixed(""), 11)),
		u.button(fixed("复制文本"), buttonQuiet, nil, func() { u.copyText(pageText, "识别文本") }),
		u.button(fixed("打开 .md"), buttonQuiet, nil, func() { u.openResultFile(id, "result.md") }),
		u.button(fixed("打开 .txt"), buttonQuiet, nil, func() { u.openResultFile(id, "result.txt") }),
		u.button(fixed("导出 Excel"), buttonPrimary, nil, func() {
			u.exportExcelFor(id, "ocr-"+id+".xlsx")
		}, "history-export-xlsx"),
		u.button(fixed("打开目录"), buttonQuiet, nil, func() { u.openHistoryDir(id) }),
		u.button(fixed("删除"), buttonDanger, nil, func() { u.deleteHistory(id) }),
	))
	rows = append(rows, u.preText(func() string {
		if strings.TrimSpace(pageText) == "" {
			return entry.Preview
		}
		return pageText
	}))

	return core.View{
		Style: core.Style{FlexGrow: 1, FlexShrink: 1, Height: core.Pct(100)},
		Children: []core.Node{core.Scroll{
			// HideIndicator：见 ui.go 里 scrollList 的注释（NSGo 拖滚动条会 panic）
			HideIndicator: true,
			Style:         core.Style{Width: core.Pct(100)},
			Children:      []core.Node{u.card(16, rows...)},
		}},
	}
}

// openHistoryDir 打开某次识别的产物目录。
func (u *ui) openHistoryDir(id string) {
	info := u.data().info
	if info == nil {
		return
	}
	root := info.Dirs["output"]
	if root == "" {
		u.post(func() { u.st.fail("不知道产物目录") })
		return
	}
	u.openDir(strings.TrimRight(root, "/") + "/" + id)
}
