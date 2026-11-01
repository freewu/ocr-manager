package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
)

const (
	filterAll        = "all"
	filterReady      = "ready"
	filterNeedAction = "todo"
)

// modelsPage 是「模型」页：卡片列表 + 过滤。
func (u *ui) modelsPage() core.Node {
	actions := []core.Node{
		u.button(fixed("刷新仓库信息"), buttonQuiet, nil, func() {
			u.guard("刷新仓库信息", func(ctx context.Context) error {
				_, err := u.cl.models(ctx, true)
				return err
			})
		}),
	}
	cards := u.visibleModels()
	nodes := make([]core.Node, 0, len(cards))
	for _, m := range cards {
		nodes = append(nodes, u.modelCard(m))
	}
	if len(nodes) == 0 {
		nodes = append(nodes, u.card(14, u.muted(fixed("这个过滤条件下没有模型。"), 12)))
	}

	enabled := 0
	for _, m := range u.data().models {
		if m.Status.State == "ready" || m.Status.State == "running" {
			enabled++
		}
	}
	subtitle := fmt.Sprintf("共 %d 个 · 已启用 %d 个 · 可以同时加载多个引擎", len(u.data().models), enabled)

	return u.pageRoot("模型", subtitle, actions,
		row(6,
			u.filterButton(filterAll, "全部"),
			u.filterButton(filterReady, "已就绪"),
			u.filterButton(filterNeedAction, "待下载"),
			fill(u.muted(func() string {
				env := u.data().env
				return "venv：" + env.State + " " + env.Message
			}, 11)),
		),
		scrollList(nodes),
	)
}

func (u *ui) filterButton(id, label string) core.Node {
	selected := func() bool { return u.filter.Get() == id }
	return core.View{
		StyleFunc: func() core.Style {
			p := u.pal()
			s := core.Style{Padding: core.Symmetric(3, 10), CornerRadius: 6}
			if selected() {
				s.Background = p.Accent.Faded(0.16)
			}
			return s
		},
		Children: []core.Node{base.Button{
			OnPress: func(core.PressEvent) { u.filter.Set(id) },
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

// visibleModels 按过滤条件挑出要显示的模型。
func (u *ui) visibleModels() []ModelInfo {
	out := []ModelInfo{}
	for _, m := range u.data().models {
		switch u.filter.Get() {
		case filterReady:
			if m.Status.State != "ready" && m.Status.State != "running" {
				continue
			}
		case filterNeedAction:
			// 需要下载权重、或依赖还没装的
			if m.Weights != "hub" || m.Status.Ready {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

// modelCard 是一条模型的卡片：名称、状态、操作、详情。
func (u *ui) modelCard(m ModelInfo) core.Node {
	id := m.ID
	expanded := func() bool { return u.data().expanded[id] }
	toggle := func() {
		u.post(func() {
			u.st.expanded[id] = !u.st.expanded[id]
			u.bump()
		})
	}

	head := row(10,
		core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, Gap: 2}, Children: []core.Node{
			row(7,
				u.strong(fixed(m.Name), 15, func() core.Color { return u.pal().Text }),
				u.chip(func() string { return stateLabel(m.Status.State) }, u.stateTone(func() string { return m.Status.State })),
				core.Show{
					When: fixedBool(m.Featured),
					Then: func(*core.Scope) core.Node {
						return u.chip(fixed("推荐"), func() core.Color { return u.pal().Success })
					},
				},
				core.Show{
					When: fixedBool(m.RequiresGPU),
					Then: func(*core.Scope) core.Node {
						return u.chip(fixed("需要 GPU"), func() core.Color { return u.pal().Warning })
					},
				},
			),
			u.muted(fixed(m.Description), 12),
		}},
		row(6, u.modelActions(m)...),
	)

	meta := row(8,
		u.chip(fixed(m.Engine), func() core.Color { return u.pal().Accent }),
		u.chip(func() string { return weightsLabel(m.Weights) }, func() core.Color { return u.pal().TextMuted }),
		core.Show{
			When: func() bool { return len(m.Extras) > 0 },
			Then: func(*core.Scope) core.Node {
				return u.chip(fixed("依赖 "+strings.Join(m.Extras, ",")), func() core.Color { return u.pal().TextMuted })
			},
		},
		core.Show{
			When: func() bool { return m.SizeHint != "" },
			Then: func(*core.Scope) core.Node {
				return u.chip(fixed(m.SizeHint), func() core.Color { return u.pal().TextMuted })
			},
		},
		core.Show{
			When: func() bool { return m.Status.NeedsDl && !m.Status.Ready },
			Then: func(*core.Scope) core.Node {
				return u.chip(fixed("权重未下载"), func() core.Color { return u.pal().Warning })
			},
		},
		core.Show{
			When: func() bool { return len(m.Tags) > 0 },
			Then: func(*core.Scope) core.Node { return u.tagChips(m.Tags) },
		},
	)

	children := []core.Node{head, meta}

	// 状态消息：有错就把错误亮出来，能直接告诉用户下一步做什么。
	// 本机不可用时逐条列原因（没显卡 / 显存不够 / 内存不够 / 外部工具没装）。
	if msg := strings.TrimSpace(m.Status.Error); msg != "" {
		children = append(children, u.text(fixed(msg), 12, func() core.Color { return u.pal().Danger }))
	} else if m.Status.State == "unavailable" && len(m.Status.Reasons) > 0 {
		for _, reason := range m.Status.Reasons {
			reason := reason
			children = append(children, u.text(fixed("· "+reason.Message), 12, func() core.Color { return u.pal().Warning }))
		}
	} else if msg := strings.TrimSpace(m.Status.Message); msg != "" {
		children = append(children, u.muted(fixed(msg), 12))
	}

	if m.Status.State == "preparing" || m.Status.State == "loading" {
		// 下载 / 装依赖的百分比来自事件流（worker 的 progress），有就画真的，没有就来回跑
		children = append(children, u.progressBar(func() *float64 {
			pct, _ := u.modelProgress(id)
			return pct
		}))
		if _, note := u.modelProgress(id); note != "" {
			children = append(children, u.muted(fixed(note), 11))
		}
	}

	children = append(children, row(8,
		u.button(func() string {
			if expanded() {
				return "收起详情"
			}
			return "展开详情"
		}, buttonQuiet, nil, toggle),
		core.Show{
			When: func() bool { return m.Homepage != "" },
			Then: func(*core.Scope) core.Node {
				return u.button(fixed("官网"), buttonQuiet, nil, func() { u.openModelHome(m.Homepage) })
			},
		},
		fill(u.muted(func() string {
			if m.Status.Updated == 0 {
				return ""
			}
			return "更新于 " + clockText(m.Status.Updated)
		}, 11)),
	))

	if expanded() {
		children = append(children, u.modelDetail(m))
	}

	return u.card(14, children...)
}

// modelActions 是主操作按钮：下载 / 启用 / 卸载 / 重试。
func (u *ui) modelActions(m ModelInfo) []core.Node {
	id := m.ID
	busy := func() bool { return u.data().busy != "" }
	st := m.Status.State
	// 权重要下载的（hub）没下完就不给「启用」；weights=auto/none 由引擎自管，只看依赖。
	canUse := m.Status.ExtOK && !m.NeedsDownload()

	out := []core.Node{}
	switch {
	case st == "unavailable":
		return []core.Node{u.chip(fixed("本机不可用"), func() core.Color { return u.pal().TextMuted })}
	case st == "preparing":
		// 下载 / 装依赖在后台跑：这时候再点一次只会排第二遍，干脆只给状态
		out = append(out, u.chip(fixed("正在准备…"), func() core.Color { return u.pal().Warning }))
		return out
	case st == "loading":
		return []core.Node{u.chip(fixed("正在加载…"), func() core.Color { return u.pal().Accent })}
	case st == "ready" || st == "running" || m.Status.Loaded:
		out = append(out, u.button(fixed("卸载"), buttonQuiet, busy, func() { u.deactivate(id) }))
	case st == "error":
		out = append(out, u.button(fixed("重试"), buttonPrimary, busy, func() { u.prepare(id, true) }))
	case m.NeedsDownload():
		out = append(out, u.button(fixed("下载权重"), buttonPrimary, busy, func() { u.prepare(id, false) }))
	case !canUse:
		// 外部工具（MinerU / Surya）装的是命令行，不是 python 依赖，文案跟着改
		label := "安装依赖"
		if m.Engine == "external" {
			label = "安装命令"
		}
		out = append(out, u.button(fixed(label), buttonPrimary, busy, func() { u.prepare(id, false) }))
	default:
		out = append(out, u.button(fixed("启用"), buttonPrimary, busy, func() { u.activate(id) }))
	}
	if m.Weights == "hub" && !m.Status.Ready && st != "unavailable" {
		out = append(out, u.button(fixed("检查权重"), buttonQuiet, busy, func() { u.prepare(id, false) }))
	}
	return out
}

// NeedsDownload 判断这条模型是否还需要拉权重。
func (m ModelInfo) NeedsDownload() bool {
	return m.Status.NeedsDl || (m.Weights == "hub" && !m.Status.Ready)
}

// modelDetail 是展开后的详情：路径、依赖、GPU、仓库文件。
func (u *ui) modelDetail(m ModelInfo) core.Node {
	id := m.ID
	rows := []core.Node{
		u.divider(),
		u.kv("模型 ID", fixed(m.ID)),
		u.kv("引擎", fixed(m.Engine)),
		u.kv("任务", fixed(joinOr(m.Tasks, "—"))),
		u.kv("语言", fixed(joinOr(m.Languages, "—"))),
		u.kv("权重策略", fixed(weightsLabel(m.Weights))),
		u.kv("依赖 extras", fixed(joinOr(m.Extras, "无"))),
		u.kv("权重目录", func() string {
			if m.Status.Weights.Path == "" {
				return "（未下载）"
			}
			return m.Status.Weights.Path
		}),
		u.kv("权重文件", func() string {
			w := m.Status.Weights
			if w.Files == 0 {
				return "—"
			}
			return fmt.Sprintf("%d 个 · %s", w.Files, fmtBytes(w.Bytes))
		}),
		u.kv("仓库", func() string {
			if m.Repo == "" {
				return "—"
			}
			return m.Repo
		}),
	}

	if m.Repo != "" {
		rows = append(rows, u.muted(func() string {
			info := m.Status.Repo
			switch {
			case info == nil:
				return "还没探测仓库（设置里可以关掉预探测）"
			case info.Error != nil:
				return "仓库探测失败：" + *info.Error
			case !info.Exists:
				return "仓库不存在或没权限：" + info.Repo
			default:
				text := fmt.Sprintf("仓库可访问 · %d 个文件 · %s", len(info.Files), fmtBytes(info.TotalBytes))
				if info.LicenseOrTags() != "" {
					text += " · " + info.LicenseOrTags()
				}
				return text
			}
		}, 11))
	}

	if m.RequiresGPU {
		rows = append(rows, u.muted(func() string {
			gpu := u.data().info
			if gpu == nil {
				return "GPU：未探测"
			}
			g := gpu.GPU
			if !g.Available {
				return "GPU：没检测到 CUDA，VLM 模型跑不了（先用 RapidOCR 在 CPU 上跑）"
			}
			names := make([]string, 0, len(g.Devices))
			for _, d := range g.Devices {
				names = append(names, fmt.Sprintf("%s (%d MiB)", d.Name, d.MemoryMB))
			}
			return "GPU：" + strings.Join(names, " · ")
		}, 11))
	}

	if m.MinRAMGB > 0 || m.MinVRAMGB > 0 {
		rows = append(rows, u.muted(func() string {
			need := []string{}
			if m.MinVRAMGB > 0 {
				need = append(need, fmt.Sprintf("显存 ≥ %d GB", m.MinVRAMGB))
			}
			if m.MinRAMGB > 0 {
				need = append(need, fmt.Sprintf("内存 ≥ %d GB", m.MinRAMGB))
			}
			text := "硬件要求：" + strings.Join(need, " · ")
			if info := u.data().info; info != nil && info.Memory.TotalMB > 0 {
				text += fmt.Sprintf("；本机内存 %d GB", info.Memory.TotalMB/1024)
			}
			return text
		}, 11))
	}

	rows = append(rows, row(8,
		u.button(fixed("复制模型 ID"), buttonQuiet, nil, func() { u.copyText(id, "模型 ID") }),
		core.Show{
			When: func() bool { return m.Repo != "" },
			Then: func(*core.Scope) core.Node {
				return u.button(fixed("复制仓库名"), buttonQuiet, nil, func() { u.copyText(m.Repo, "仓库名") })
			},
		},
		core.Show{
			When: func() bool { return m.Homepage != "" },
			Then: func(*core.Scope) core.Node {
				return u.button(fixed("打开官网"), buttonQuiet, nil, func() { u.openModelHome(m.Homepage) })
			},
		},
	))
	return core.View{Style: core.Style{Gap: 6}, Children: rows}
}

func fixedBool(v bool) func() bool { return func() bool { return v } }

func stateLabel(state string) string {
	switch state {
	case "ready":
		return "已就绪"
	case "running":
		return "识别中"
	case "preparing":
		return "准备中"
	case "loading":
		return "加载中"
	case "error":
		return "出错"
	case "unavailable":
		return "不可用"
	default:
		return "未加载"
	}
}

func weightsLabel(w string) string {
	switch w {
	case "hub":
		return "需下载权重"
	case "auto":
		return "引擎自管权重"
	default:
		return "无需权重"
	}
}
