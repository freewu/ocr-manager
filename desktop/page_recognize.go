package main

import (
	"fmt"
	"github.com/oneo-me/nsgo/base"
	"strings"

	"github.com/oneo-me/nsgo/core"
)

// recognizePage 是「识别」页：填路径 → 选模型 → 跑 → 看结果。
func (u *ui) recognizePage() core.Node {
	actions := []core.Node{
		u.button(fixed("清空结果"), buttonQuiet, nil, func() {
			u.post(func() {
				u.st.result = nil
				u.st.job = nil
				u.bump()
			})
		}),
		u.button(fixed("刷新状态"), buttonQuiet, nil, u.refresh),
	}

	job := u.data().job
	running := job != nil && job.Status == "running"

	input := u.card(14,
		row(10,
			u.strong(fixed("输入"), 14, func() core.Color { return u.pal().Text }),
			fill(u.muted(fixed("图片 / PDF 的本机路径，每行一个"), 11)),
		),
		u.input(u.paths, "/home/me/page.png\n/home/me/report.pdf", true, 96, "recognize-paths"),
		row(10,
			fill(u.input(u.maxPages, "PDF 最多页数", false, 34, "recognize-max-pages")),
			fill(u.input(u.pdfDPI, "PDF 渲染 DPI", false, 34, "recognize-dpi")),
		),
		u.modelPicker(),
		row(10,
			u.button(func() string {
				if running {
					return "识别中…"
				}
				return "开始识别"
			}, buttonPrimary, func() bool { return running || u.data().busy != "" }, u.recognize, "recognize-start"),
			core.Show{
				When: func() bool { return running },
				Then: func(*core.Scope) core.Node {
					return u.button(fixed("取消"), buttonDanger, nil, u.cancelJob, "recognize-cancel")
				},
			},
		),
	)

	status := core.Show{
		When: func() bool { return u.data().job != nil },
		Then: func(*core.Scope) core.Node {
			return u.jobCard()
		},
	}

	result := u.resultCard()

	return u.pageRoot("识别", "选输入 → 选模型 → 出 text / markdown / 分块结果", actions,
		core.Scroll{
			TestID: "page-scroll",
			Style:  core.Style{FlexGrow: 1, Width: core.Pct(100)},
			Children: []core.Node{core.View{
				Style: core.Style{Gap: 12, Width: core.Pct(100)},
				Children: []core.Node{
					input,
					status,
					result,
				},
			}},
		},
	)
}

// modelPicker 是识别页的模型选择：一排可点的胶囊，只列本机能用的。
func (u *ui) modelPicker() core.Node {
	usable := u.usableModels()
	chips := make([]core.Node, 0, len(usable))
	for _, m := range usable {
		id := m.ID
		label := m.Name
		if m.Status.State == "ready" || m.Status.State == "running" {
			label += " ·已就绪"
		}
		chips = append(chips, u.modelChip(id, label))
	}
	if len(chips) == 0 {
		return u.card(14,
			u.muted(fixed("还没有可用模型：先去「模型」页下载/启用一个（RapidOCR 最省事）。"), 12),
		)
	}
	return core.View{Style: core.Style{Gap: 6}, Children: []core.Node{
		u.muted(fixed("识别模型"), 11),
		core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 6}, Children: chips},
	}}
}

// modelChip 是一颗可点选的模型胶囊。
func (u *ui) modelChip(id, label string) core.Node {
	selected := func() bool { return u.model.Get() == id }
	return core.View{
		StyleFunc: func() core.Style {
			p := u.pal()
			s := core.Style{
				Padding:      core.Symmetric(4, 10),
				CornerRadius: 7,
				BorderWidth:  1,
				BorderColor:  p.Border,
				Background:   p.Surface,
			}
			if selected() {
				s.BorderColor = p.Accent
				s.Background = p.Accent.Faded(0.16)
			}
			return s
		},
		Children: []core.Node{base.Button{
			OnPress: func(core.PressEvent) { u.model.Set(id) },
			Style:   core.Style{Padding: core.All(0)},
			Children: []core.Node{u.text(fixed(label), 12, func() core.Color {
				if selected() {
					return u.pal().Accent
				}
				return u.pal().TextMuted
			})},
		}},
	}
}

// jobCard 显示当前任务进度。
func (u *ui) jobCard() core.Node {
	return u.card(14,
		row(10,
			u.strong(func() string {
				job := u.st.job
				switch {
				case job == nil:
					return "没有任务"
				case job.Status == "running":
					return "识别中"
				case job.Status == "done":
					return "已完成"
				case job.Status == "canceled":
					return "已取消"
				default:
					return "失败"
				}
			}, 14, func() core.Color {
				job := u.st.job
				if job == nil {
					return u.pal().TextMuted
				}
				switch job.Status {
				case "done":
					return u.pal().Success
				case "running":
					return u.pal().Accent
				case "canceled":
					return u.pal().Warning
				default:
					return u.pal().Danger
				}
			}),
			fill(u.muted(func() string {
				job := u.st.job
				if job == nil {
					return ""
				}
				return job.ID
			}, 11)),
		),
		u.progressBar(func() *float64 { return percentOf(u.st.job) }),
		u.muted(func() string {
			job := u.st.job
			if job == nil {
				return ""
			}
			text := job.Message
			if text == "" {
				text = job.Stage
			}
			if job.Status == "done" {
				text = fmt.Sprintf("%d 页 / %d 块 / %d 字 · %.2fs",
					job.Pages, job.Blocks, job.Chars, float64(job.DurationMS)/1000)
			}
			if job.Error != "" {
				text = job.Error
			}
			return text
		}, 12),
	)
}

// resultCard 显示识别结果：纯文本 / Markdown / 分块三种视图 + 导出。
func (u *ui) resultCard() core.Node {
	show := func() bool { return u.data().result != nil || u.data().detail != nil }
	return core.Show{
		When: show,
		Then: func(*core.Scope) core.Node {
			result, id := u.currentResult()
			if result == nil {
				return u.card(14, u.muted(fixed("还没有结果"), 12))
			}
			pages := result.Pages
			idx := u.pageIdx.Get()
			if idx >= len(pages) {
				idx = 0
			}
			var page *ResultPage
			if len(pages) > 0 {
				page = &pages[idx]
			}

			head := row(10,
				u.strong(fixed("识别结果"), 14, func() core.Color { return u.pal().Text }),
				u.chip(func() string { return result.Engine }, func() core.Color { return u.pal().Accent }),
				fill(u.muted(func() string {
					if page == nil {
						return ""
					}
					return fmt.Sprintf("第 %d/%d 页 · %d×%d · %d 块", idx+1, len(pages), page.Width, page.Height, len(page.Blocks))
				}, 11)),
			)

			tabs := row(6,
				u.resultTabButton("text", "纯文本"),
				u.resultTabButton("markdown", "Markdown"),
				u.resultTabButton("blocks", "分块"),
				fill(u.muted(fixed(""), 11)),
				u.button(fixed("导出 .md"), buttonQuiet, nil, func() { u.openResultFile(id, "result.md") }),
				u.button(fixed("导出 .txt"), buttonQuiet, nil, func() { u.openResultFile(id, "result.txt") }),
			)

			var body core.Node
			switch u.resultTab.Get() {
			case "blocks":
				body = u.blocksView(page)
			case "markdown":
				body = u.preText(func() string {
					if page == nil {
						return ""
					}
					if page.Markdown != "" {
						return page.Markdown
					}
					return page.Text
				})
			default:
				body = u.preText(func() string {
					if page == nil {
						return ""
					}
					if page.Text != "" {
						return page.Text
					}
					return page.Markdown
				})
			}

			children := []core.Node{head, tabs}
			// 多页时给一排页码
			if len(pages) > 1 {
				children = append(children, u.pageChips(len(pages)))
			}
			children = append(children, body)
			return u.card(14, children...)
		},
	}
}

// currentResult 返回要展示的结果和它的产物 id。
func (u *ui) currentResult() (*OcrResult, string) {
	if d := u.data(); d.result != nil {
		id := ""
		if d.job != nil {
			id = d.job.ID
		}
		return d.result, id
	}
	return nil, ""
}

// resultTabButton 是结果视图的切换按钮。
func (u *ui) resultTabButton(id, label string) core.Node {
	selected := func() bool { return u.resultTab.Get() == id }
	return core.View{
		StyleFunc: func() core.Style {
			p := u.pal()
			s := core.Style{Padding: core.Symmetric(3, 9), CornerRadius: 6}
			if selected() {
				s.Background = p.Accent.Faded(0.16)
			}
			return s
		},
		Children: []core.Node{base.Button{
			OnPress: func(core.PressEvent) { u.resultTab.Set(id) },
			Style:   core.Style{Padding: core.All(0)},
			Children: []core.Node{u.text(fixed(label), 12, func() core.Color {
				if selected() {
					return u.pal().Accent
				}
				return u.pal().TextMuted
			})},
		}},
	}
}

// pageChips 是多页结果的页码条。
func (u *ui) pageChips(total int) core.Node {
	chips := make([]core.Node, 0, total)
	for i := 0; i < total; i++ {
		idx := i
		selected := func() bool { return u.pageIdx.Get() == idx }
		chips = append(chips, base.Button{
			OnPress: func(core.PressEvent) { u.pageIdx.Set(idx) },
			StyleFunc: func() core.Style {
				p := u.pal()
				s := core.Style{Padding: core.Symmetric(3, 9), CornerRadius: 6, BorderWidth: 1, BorderColor: p.Border}
				if selected() {
					s.BorderColor, s.Background = p.Accent, p.Accent.Faded(0.16)
				}
				return s
			},
			Children: []core.Node{u.text(fixed(fmt.Sprintf("第 %d 页", idx+1)), 11, func() core.Color {
				if selected() {
					return u.pal().Accent
				}
				return u.pal().TextMuted
			})},
		})
	}
	return core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 6}, Children: chips}
}

// preText 是一段可滚动的等宽文本（结果正文）。
func (u *ui) preText(get func() string) core.Node {
	return core.View{
		StyleFunc: func() core.Style {
			p := u.pal()
			return core.Style{
				MaxHeight:    core.Pt(420),
				Padding:      core.All(11),
				CornerRadius: 9,
				BorderWidth:  1,
				BorderColor:  p.Border,
				Background:   p.Background,
				// 识别结果要能选中复制
				UserSelect: core.UserSelectText,
			}
		},
		Children: []core.Node{core.Scroll{
			Style: core.Style{Width: core.Pct(100)},
			Children: []core.Node{u.mono(func() string {
				text := get()
				if strings.TrimSpace(text) == "" {
					return "（空）"
				}
				return text
			}, 12, func() core.Color { return u.pal().Text })},
		}},
	}
}

// blocksView 把分块列表渲染成「置信度 + 类型 + 文字」。
func (u *ui) blocksView(page *ResultPage) core.Node {
	if page == nil || len(page.Blocks) == 0 {
		return u.card(12, u.muted(fixed("这一页没有分块信息。"), 12))
	}
	rows := make([]core.Node, 0, len(page.Blocks))
	for _, b := range page.Blocks {
		block := b
		conf := "—"
		if block.Confidence != nil {
			conf = fmt.Sprintf("%.3f", *block.Confidence)
		}
		kind := block.Kind
		if kind == "" {
			kind = "text"
		}
		rows = append(rows, row(8,
			core.View{Style: core.Style{Width: core.Pt(64)}, Children: []core.Node{
				u.mono(fixed(conf), 11, func() core.Color { return u.pal().TextMuted }),
			}},
			core.View{Style: core.Style{Width: core.Pt(60)}, Children: []core.Node{
				u.chip(fixed(kind), func() core.Color { return u.pal().Accent }),
			}},
			fill(u.text(fixed(block.Text), 12, func() core.Color { return u.pal().Text })),
		))
	}
	return core.View{
		StyleFunc: func() core.Style {
			p := u.pal()
			return core.Style{
				MaxHeight:    core.Pt(420),
				Padding:      core.All(11),
				CornerRadius: 9,
				BorderWidth:  1,
				BorderColor:  p.Border,
				Background:   p.Background,
				Gap:          4,
			}
		},
		Children: []core.Node{core.Scroll{
			Style:    core.Style{Width: core.Pct(100)},
			Children: []core.Node{core.View{Style: core.Style{Gap: 4}, Children: rows}},
		}},
	}
}

// usableModels 只留本机跑得起来的模型。
func (u *ui) usableModels() []ModelInfo {
	all := u.data().models
	out := make([]ModelInfo, 0, len(all))
	for _, m := range all {
		if m.Status.State == "unavailable" {
			continue
		}
		out = append(out, m)
	}
	return out
}
