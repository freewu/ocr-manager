package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
)

// recognizePage 是「识别」页：选文件 → 选模型 → 跑 → 按文件看结果。
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

	input := u.inputCard(running)

	status := core.Show{
		When: func() bool { return u.data().job != nil },
		Then: func(*core.Scope) core.Node { return u.jobCard() },
	}

	result := u.resultCard()

	return u.pageRoot("识别", "选文件 → 选模型 → 出 text / markdown / 分块，可导出 Excel", actions,
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

// inputCard 是输入区：上面一排按钮，中间「文件列表 | 预览」可拖动分栏，下面是参数与模型。
func (u *ui) inputCard(running bool) core.Node {
	count := func() string {
		files := u.st.files
		if len(files) == 0 {
			return "还没选文件"
		}
		var bytes int64
		for _, file := range files {
			bytes += file.Size
		}
		return fmt.Sprintf("%d 个文件 · %s", len(files), fmtBytes(uint64(bytes)))
	}

	return u.card(14,
		row(10,
			u.strong(fixed("输入文件"), 14, func() core.Color { return u.pal().Text }),
			fill(u.muted(count, 11)),
			// 识别跑在后台，选文件 / 清空随时能用，不用等任务做完
			u.button(fixed("选择文件"), buttonPrimary, nil, u.pickInputs, "recognize-pick"),
			u.button(fixed("清空"), buttonQuiet, func() bool { return len(u.st.files) == 0 }, u.clearInputs, "recognize-clear"),
		),
		u.splitPane(),
		// 只有系统里压根弹不出对话框（没有 zenity / kdialog）才把「粘贴路径」露出来
		core.Show{
			When: func() bool { return u.st.needPaste },
			Then: func(*core.Scope) core.Node {
				return row(10,
					fill(u.input(u.pickPath, "粘贴图片 / PDF 的完整路径，回车添加", false, 34, "recognize-paths")),
					u.button(fixed("添加"), buttonQuiet, nil, u.addInputFromText, "recognize-add"),
				)
			},
		},
		u.modelSelect(),
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
			fill(u.muted(func() string {
				if running {
					return "识别在后台跑：可以继续选文件、看预览、切到别的页"
				}
				return ""
			}, 11)),
		),
	)
}

// splitPane 是「文件列表 1/4 | 预览 3/4」的可拖动分栏。预览可以整块收起。
func (u *ui) splitPane() core.Node {
	list := base.ResizablePanel{
		TestID:  "recognize-file-panel",
		Size:    func() core.Points { return core.Points(260) },
		MinSize: core.Points(170),
		Style:   core.Style{Height: core.Pt(300)},
		Children: []core.Node{core.View{
			Style:    core.Style{Width: core.Pct(100), Height: core.Pct(100)},
			Children: []core.Node{u.fileList()},
		}},
	}
	// 预览只在「有文件」时出现：选文件自然就开，点右上角 ✕ 就关。
	// 没文件时它只能显示一句「在左边点一个文件」，白白占着宽度。
	preview := base.ResizablePanel{
		TestID:   "recognize-preview-panel",
		MinSize:  core.Points(180),
		Visible:  func() bool { return u.previewOpen.Get() && len(u.st.files) > 0 },
		Style:    core.Style{Height: core.Pt(300)},
		Children: []core.Node{u.previewPane()},
	}
	return core.View{Style: core.Style{Gap: 6, Width: core.Pct(100)}, Children: []core.Node{
		base.ResizablePanelGroup{
			TestID:      "recognize-split",
			Axis:        base.Horizontal,
			Panels:      []base.ResizablePanel{list, preview},
			HandleLabel: "拖动能调整文件列表与预览的宽度",
			HandleStyleFunc: func() core.Style {
				p := u.pal()
				return core.Style{Background: p.Border}
			},
			HandleThumbStyle: core.Style{Background: u.pal().Border},
			Style:            core.Style{Width: core.Pct(100), Height: core.Pt(300)},
		},
	}}
}

// fileList 是左侧的文件列表（选中看预览，✕ 移除）。
func (u *ui) fileList() core.Node {
	files := u.st.files
	if len(files) == 0 {
		return core.View{
			StyleFunc: func() core.Style {
				p := u.pal()
				return core.Style{
					Width: core.Pct(100), Height: core.Pct(100), Padding: core.All(12), Gap: 6,
					CornerRadius: 9, BorderWidth: 1, BorderColor: p.Border, Background: p.Background,
					JustifyContent: core.JustifyCenter,
				}
			},
			Children: []core.Node{
				u.muted(fixed("点「选择文件」可以一次选多张图片和 PDF"), 11),
				u.muted(fixed("选中的文件会排队，一次识别全部"), 11),
			},
		}
	}
	rows := make([]core.Node, 0, len(files))
	for _, file := range files {
		rows = append(rows, u.fileRow(file))
	}
	return core.View{
		StyleFunc: func() core.Style {
			p := u.pal()
			return core.Style{
				Width: core.Pct(100), Height: core.Pct(100), Padding: core.All(6),
				CornerRadius: 9, BorderWidth: 1, BorderColor: p.Border, Background: p.Background,
			}
		},
		Children: []core.Node{core.Scroll{
			TestID: "recognize-file-scroll",
			Style:  core.Style{Width: core.Pct(100), FlexGrow: 1},
			Children: []core.Node{core.View{
				Style:    core.Style{Gap: 4, Width: core.Pct(100)},
				Children: rows,
			}},
		}},
	}
}

// fileRow 是文件列表的一行：文件名 + 大小，点一下看预览，✕ 移除。
func (u *ui) fileRow(file pickedFile) core.Node {
	selected := func() bool { return u.selFile.Get() == file.Path }
	return core.View{
		StyleFunc: func() core.Style {
			p := u.pal()
			s := core.Style{
				Width: core.Pct(100), Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8,
				Padding: core.Symmetric(6, 8), CornerRadius: 7,
			}
			if selected() {
				s.Background = p.Accent.Faded(0.14)
			}
			return s
		},
		Children: []core.Node{
			base.Button{
				TestID:  "recognize-file-" + file.Name,
				OnPress: func(core.PressEvent) { u.selectInput(file.Path) },
				StyleFunc: func() core.Style {
					// 名字占满剩余宽度但要能被压缩：写死 Width(100%) 的话
					// 后面的 ✕ 会被挤出可视区，用户就再也删不掉文件了。
					return core.Style{
						FlexGrow: 1, FlexShrink: 1,
						Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8,
					}
				},
				Children: []core.Node{
					core.View{Style: core.Style{Gap: 2}, Children: []core.Node{
						u.text(fixed(file.Name), 12, func() core.Color {
							if selected() {
								return u.pal().Accent
							}
							return u.pal().Text
						}),
						u.muted(fixed(fmt.Sprintf("%s · %s", file.Kind, fmtBytes(uint64(file.Size)))), 10),
					}},
				},
			},
			base.Button{
				TestID:  "recognize-remove-" + file.Name,
				OnPress: func(core.PressEvent) { u.removeInput(file.Path) },
				StyleFunc: func() core.Style {
					return core.Style{
						Width: core.Pt(20), Height: core.Pt(20),
						AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
						Padding: core.All(0), CornerRadius: 6,
						Hover: &core.Style{Background: u.pal().Danger.Faded(0.16)},
					}
				},
				Children: []core.Node{u.text(fixed("✕"), 13, func() core.Color {
					return u.pal().TextMuted
				})},
			},
		},
	}
}

// previewPane 是右侧预览：图片能放大、能拖着看，PDF 给一句说明。
func (u *ui) previewPane() core.Node {
	file, hasFile := u.inputAt(u.selFile.Get())
	zoomable := hasFile && len(u.st.previewData) > 0

	head := row(8,
		fill(u.text(func() string {
			if !hasFile {
				return "预览"
			}
			return file.Name
		}, 12, func() core.Color { return u.pal().Text })),
		core.Show{
			When: func() bool { return zoomable },
			Then: func(*core.Scope) core.Node {
				return row(4,
					u.button(fixed("－"), buttonQuiet, func() bool {
						return u.previewZoom.Get() <= previewZoomFit
					}, u.zoomOut, "recognize-zoom-out"),
					u.muted(func() string { return u.previewZoomText() }, 11),
					u.button(fixed("＋"), buttonQuiet, nil, u.zoomIn, "recognize-zoom-in"),
					u.button(fixed("适应"), buttonQuiet, nil, u.resetZoom, "recognize-zoom-fit"),
				)
			},
		},
		base.Button{
			TestID:  "recognize-close-preview",
			OnPress: func(core.PressEvent) { u.previewOpen.Set(false) },
			Style: core.Style{
				Padding: core.Symmetric(2, 7), CornerRadius: 6,
				Hover: &core.Style{Background: u.pal().SurfaceHover},
			},
			Children: []core.Node{u.muted(fixed("✕"), 12)},
		},
	)

	var body core.Node
	switch {
	case !hasFile:
		body = core.View{
			Style:    core.Style{FlexGrow: 1, JustifyContent: core.JustifyCenter, AlignItems: core.AlignCenter},
			Children: []core.Node{u.muted(fixed("在左边点一个文件，这里就显示预览"), 11)},
		}
	case len(u.st.previewData) > 0:
		body = core.View{
			TestID:   "recognize-preview-stage",
			OnPan:    u.onPreviewPan,
			OnPinch:  u.onPreviewPinch,
			OnLayout: func(b core.Bounds) { u.previewBox = b },
			StyleFunc: func() core.Style {
				p := u.pal()
				return core.Style{
					FlexGrow: 1, Width: core.Pct(100), MinHeight: core.Pt(120),
					CornerRadius: 8, Overflow: core.Hidden,
					Background: p.SurfacePressed.Faded(0.5),
				}
			},
			Children: []core.Node{core.Image{
				TestID:     "recognize-preview-image",
				SourceFunc: func() core.ImageSource { return core.ImageSource{Data: u.st.previewData} },
				ResizeMode: core.Contain,
				StyleFunc: func() core.Style {
					zoom := u.previewZoom.Get()
					pan := u.previewPan.Get()
					return core.Style{
						Width: core.Pct(100), Height: core.Pct(100),
						Transform: core.Transform{
							Scale:      zoom,
							TranslateX: pan.X,
							TranslateY: pan.Y,
						},
					}
				},
			}},
		}
	default:
		body = core.View{
			Style:    core.Style{FlexGrow: 1, Padding: core.All(10), Gap: 6, JustifyContent: core.JustifyCenter},
			Children: []core.Node{u.muted(fixed(u.st.previewNote), 11)},
		}
	}

	children := []core.Node{head, body}
	if zoomable {
		children = append(children, u.muted(func() string {
			if u.previewZoom.Get() > previewZoomFit {
				return "拖动可以拉看局部 · Ctrl+滚轮（或触控板捏合）也能缩放 · 点「适应」回到全图"
			}
			return "点「＋」放大后可以拖着看局部"
		}, 10))
	}

	return core.View{
		StyleFunc: func() core.Style {
			p := u.pal()
			return core.Style{
				Width: core.Pct(100), Height: core.Pct(100), Padding: core.All(8), Gap: 6,
				CornerRadius: 9, BorderWidth: 1, BorderColor: p.Border, Background: p.Background,
			}
		},
		Children: children,
	}
}

// modelSelect 是识别模型的下拉选择（只列本机能用的模型）。
func (u *ui) modelSelect() core.Node {
	usable := u.usableModels()
	if len(usable) == 0 {
		// 一个都没启用：别给个空下拉，直接指路去模型页
		return u.card(12,
			row(10,
				fill(u.muted(fixed("还没有启用的模型：先去「模型」页启用一个（RapidOCR 最省事）。"), 12)),
				u.button(fixed("去模型页"), buttonPrimary, nil, func() { u.page.Set("models") }, "recognize-go-models"),
			),
		)
	}
	items := make([]core.Node, 0, len(usable))
	for _, model := range usable {
		m := model
		items = append(items, base.MenuItem{
			Label:    m.Name,
			TestID:   "recognize-model-" + m.ID,
			OnSelect: func() { u.model.Set(m.ID) },
			Children: []core.Node{row(8,
				u.text(fixed(m.Name), 12, func() core.Color {
					if u.model.Get() == m.ID {
						return u.pal().Accent
					}
					return u.pal().Text
				}),
				fill(core.View{}),
				u.chip(fixed(m.Status.State), u.stateTone(func() string { return m.Status.State })),
			)},
		})
	}

	label := func() string {
		current := u.model.Get()
		for _, m := range usable {
			if m.ID == current {
				return m.Name
			}
		}
		return "请选择模型"
	}

	return core.View{Style: core.Style{Gap: 6, Width: core.Pct(100)}, Children: []core.Node{
		u.muted(fixed("识别模型"), 11),
		base.Menu{
			TestID: "recognize-model-select",
			Label:  "识别模型",
			Offset: 4,
			Margin: 8,
			PopupStyleFunc: func() core.Style {
				p := u.pal()
				return core.Style{
					Padding: core.All(4), CornerRadius: 10, BorderWidth: 1,
					BorderColor: p.Border, Background: p.Surface,
				}
			},
			BackdropStyle: core.Style{Background: core.RGBA(0x000000, 0.35)},
			Trigger: core.View{
				StyleFunc: func() core.Style {
					p := u.pal()
					return core.Style{
						Width: core.Pct(100), Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8,
						Padding: core.Symmetric(8, 11), CornerRadius: 9, BorderWidth: 1,
						BorderColor: p.Border, Background: p.Background,
						Hover: &core.Style{Background: p.SurfaceHover},
					}
				},
				Children: []core.Node{
					fill(u.text(label, 13, func() core.Color { return u.pal().Text })),
					u.muted(fixed("▾"), 12),
				},
			},
			Children: items,
		},
	}}
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

// sourceGroup 是结果里一个输入文件对应的那一组页。
type sourceGroup struct {
	key   string
	label string
	pages []ResultPage
}

// groupBySource 按来源文件把页分组；老结果没有 source 字段就全归到一组。
func groupBySource(pages []ResultPage) []sourceGroup {
	groups := make([]sourceGroup, 0, 4)
	index := map[string]int{}
	for _, page := range pages {
		key := page.Source
		where, ok := index[key]
		if !ok {
			where = len(groups)
			index[key] = where
			label := displaySource(key)
			if label == "" {
				label = "全部页"
			}
			groups = append(groups, sourceGroup{key: key, label: label})
		}
		groups[where].pages = append(groups[where].pages, page)
	}
	return groups
}

// displaySource 把产物目录里的副本名（02-invoice.pdf）还原成用户看得懂的名字。
func displaySource(name string) string {
	if name == "" {
		return ""
	}
	base := filepath.Base(name)
	if len(base) > 3 && base[2] == '-' {
		prefix := base[:2]
		if prefix[0] >= '0' && prefix[0] <= '9' && prefix[1] >= '0' && prefix[1] <= '9' {
			return base[3:]
		}
	}
	return base
}

// resultCard 显示识别结果：一个文件一个 tab，tab 内再看 text / markdown / 分块。
func (u *ui) resultCard() core.Node {
	show := func() bool { return u.data().result != nil }
	return core.Show{
		When: show,
		Then: func(*core.Scope) core.Node {
			result, id := u.currentResult()
			if result == nil {
				return u.card(14, u.muted(fixed("还没有结果"), 12))
			}
			groups := groupBySource(result.Pages)
			if len(groups) == 0 {
				return u.card(14, u.muted(fixed("这次识别没有返回任何页。"), 12))
			}
			active := u.activeGroup(groups)
			pages := groups[active].pages
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
					return fmt.Sprintf("%d 个文件 · 当前第 %d/%d 页 · %d×%d · %d 块",
						len(groups), idx+1, len(pages), page.Width, page.Height, len(page.Blocks))
				}, 11)),
				u.button(fixed("导出 Excel"), buttonPrimary, nil, u.exportExcel, "recognize-export-xlsx"),
				u.button(fixed("导出 .md"), buttonQuiet, nil, func() { u.openResultFile(id, "result.md") }),
				u.button(fixed("导出 .txt"), buttonQuiet, nil, func() { u.openResultFile(id, "result.txt") }),
			)

			views := row(6,
				u.resultTabButton("text", "纯文本"),
				u.resultTabButton("markdown", "Markdown"),
				u.resultTabButton("blocks", "分块"),
				fill(u.muted(fixed(""), 11)),
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

			children := []core.Node{head, u.fileTabs(groups, active), views}
			if len(pages) > 1 {
				children = append(children, u.pageChips(len(pages)))
			}
			children = append(children, body)
			return u.card(14, children...)
		},
	}
}

// activeGroup 返回当前选中的文件 tab 下标；选的那个已经不在结果里就回到第一个。
func (u *ui) activeGroup(groups []sourceGroup) int {
	want := u.resultFile.Get()
	for i, group := range groups {
		if group.key == want {
			return i
		}
	}
	return 0
}

// fileTabs 是结果区的文件 tab 条，一个文件一个 tab。
func (u *ui) fileTabs(groups []sourceGroup, active int) core.Node {
	tabs := make([]core.Node, 0, len(groups))
	for i, group := range groups {
		idx, g := i, group
		selected := func() bool { return active == idx }
		tabs = append(tabs, base.Tab{
			TestID:   "recognize-tab-" + g.label,
			Label:    g.label,
			Selected: selected,
			OnPress: func(core.PressEvent) {
				u.resultFile.Set(g.key)
				u.pageIdx.Set(0)
			},
			StyleFunc: func() core.Style {
				p := u.pal()
				s := core.Style{
					Padding:      core.Symmetric(5, 11),
					CornerRadius: 7,
					BorderWidth:  1,
					BorderColor:  p.Border,
					Background:   p.Background,
				}
				if selected() {
					s.BorderColor, s.Background = p.Accent, p.Accent.Faded(0.14)
				}
				return s
			},
			Children: []core.Node{u.text(fixed(fmt.Sprintf("%s (%d)", g.label, len(g.pages))), 12, func() core.Color {
				if selected() {
					return u.pal().Accent
				}
				return u.pal().TextMuted
			})},
		})
	}
	return base.Tabs{
		TestID:   "recognize-result-tabs",
		Label:    "识别结果（按文件）",
		Style:    core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 6, Width: core.Pct(100)},
		Children: tabs,
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
				UserSelect:   core.UserSelectText,
			}
		},
		Children: []core.Node{core.Scroll{
			Style:    core.Style{Width: core.Pct(100)},
			Children: []core.Node{core.View{Style: core.Style{Gap: 4}, Children: rows}},
		}},
	}
}

// usableModels 只留本机跑得起来的模型。
// usableModels 只列「已经启用」的模型：没加载的引擎选了也跑不起来。
func (u *ui) usableModels() []ModelInfo {
	all := u.data().models
	out := make([]ModelInfo, 0, len(all))
	for _, m := range all {
		if m.Status.State == "ready" || m.Status.State == "running" {
			out = append(out, m)
		}
	}
	return out
}
