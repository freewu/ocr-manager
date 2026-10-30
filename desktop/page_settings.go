package main

import (
	"fmt"
	"github.com/oneo-me/nsgo/base"
	"strings"

	"github.com/oneo-me/nsgo/core"
)

// settingsPage 是「设置」页：目录、识别参数、Python 环境、GPU、文档链接。
func (u *ui) settingsPage() core.Node {
	actions := []core.Node{
		u.button(fixed("保存"), buttonPrimary, func() bool { return u.data().busy != "" }, u.saveSettings, "settings-save"),
	}
	info := u.data().info
	if info == nil {
		return u.pageRoot("设置", "还没连上后端", actions,
			scrollList([]core.Node{u.card(16, u.muted(fixed("正在连接后端…"), 12))}),
		)
	}

	return u.pageRoot("设置", "改完点「保存」；目录改动会影响权重与产物的存放位置", actions,
		scrollList([]core.Node{
			u.recognizeSettingsCard(),
			u.dirsCard(),
			u.envCard(info),
			u.gpuCard(info),
			u.aboutCard(info),
		}),
	)
}

// recognizeSettingsCard 是识别默认参数。
func (u *ui) recognizeSettingsCard() core.Node {
	return u.card(16,
		u.strong(fixed("识别参数"), 14, func() core.Color { return u.pal().Text }),
		u.muted(fixed("PDF 超过页数上限会截断；DPI 越高越清晰但越慢。"), 11),
		row(12,
			field("默认模型", u.defaultModelPicker()),
			field("PDF 最多页数", u.input(u.maxPages, "20", false, 34, "settings-max-pages")),
			field("PDF 渲染 DPI", u.input(u.pdfDPI, "200", false, 34, "settings-dpi")),
			field("长边像素上限", u.input(u.maxSide, "0 = 不限制", false, 34, "settings-max-side")),
		),
	)
}

func field(label string, node core.Node) core.Node {
	return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, Gap: 4}, Children: []core.Node{
		core.Text{ContentFunc: fixed(label), StyleFunc: func() core.Style {
			return core.Style{FontSize: 11, Color: core.RGB(0x8a9bc0)}
		}},
		node,
	}}
}

// defaultModelPicker 是默认模型的下拉（用胶囊代替）。
func (u *ui) defaultModelPicker() core.Node {
	all := u.data().models
	chips := make([]core.Node, 0, len(all))
	for _, m := range all {
		id := m.ID
		selected := func() bool { return u.model.Get() == id }
		chips = append(chips, base.Button{
			OnPress: func(core.PressEvent) { u.model.Set(id) },
			StyleFunc: func() core.Style {
				p := u.pal()
				s := core.Style{Padding: core.Symmetric(3, 9), CornerRadius: 6, BorderWidth: 1, BorderColor: p.Border}
				if selected() {
					s.BorderColor, s.Background = p.Accent, p.Accent.Faded(0.16)
				}
				return s
			},
			Children: []core.Node{core.Text{ContentFunc: fixed(m.Name), StyleFunc: func() core.Style {
				color := u.pal().TextMuted
				if selected() {
					color = u.pal().Accent
				}
				return core.Style{FontSize: 12, Color: color}
			}}},
		})
	}
	return core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 6}, Children: chips}
}

// dirsCard 是目录与镜像配置。
func (u *ui) dirsCard() core.Node {
	info := u.data().info
	return u.card(16,
		u.strong(fixed("目录与下载源"), 14, func() core.Color { return u.pal().Text }),
		u.kv("数据根目录", func() string { return info.Root }),
		row(12,
			field("模型 / 权重目录", u.input(u.modelsDir, info.Dirs["models"], false, 34, "settings-models-dir")),
			field("产物目录", u.input(u.outDir, info.Dirs["output"], false, 34, "settings-out-dir")),
		),
		field("HuggingFace 镜像（HF_ENDPOINT）", u.input(u.hfEp, "https://huggingface.co", false, 34, "settings-hf")),
		row(8,
			u.button(fixed("打开数据目录"), buttonQuiet, nil, func() { u.openDir(info.Root) }),
			u.button(fixed("打开产物目录"), buttonQuiet, nil, func() { u.openDir(info.Dirs["output"]) }),
			u.button(fixed("打开日志目录"), buttonQuiet, nil, func() { u.openDir(info.Dirs["logs"]) }),
		),
	)
}

// envCard 是 Python 环境：基础解释器、依赖安装、重建。
func (u *ui) envCard(info *AppInfo) core.Node {
	env := u.data().env
	extras := []string{"rapidocr", "tesseract", "paddle", "vlm"}
	buttons := make([]core.Node, 0, len(extras)+2)
	for _, extra := range extras {
		name := extra
		installed, known := env.Extras[name]
		label := "装 " + name
		if known && installed {
			label = name + " ✓"
		}
		buttons = append(buttons, u.button(fixed(label), buttonQuiet,
			func() bool { return u.data().busy != "" },
			func() { u.installEnv([]string{name}) }))
	}
	buttons = append(buttons,
		u.button(fixed("装全部"), buttonQuiet, func() bool { return u.data().busy != "" }, func() {
			u.installEnv(nil)
		}),
		u.button(fixed("重建 venv"), buttonDanger, func() bool { return u.data().busy != "" }, u.recreateEnv),
	)

	probes := "—"
	if len(env.ProbeErrors) > 0 {
		probes = strings.Join(env.ProbeErrors, "；")
	}

	return u.card(16,
		row(10,
			u.strong(fixed("Python 环境"), 14, func() core.Color { return u.pal().Text }),
			u.chip(func() string { return env.State }, u.stateTone(func() string {
				switch env.State {
				case "ready":
					return "ready"
				case "missing":
					return "error"
				default:
					return "idle"
				}
			})),
			fill(u.muted(fixed(env.Message), 11)),
		),
		row(12,
			field("基础解释器", u.input(u.basePy, "/usr/bin/python3", false, 34, "settings-base-python")),
			core.View{Style: core.Style{Padding: core.Edges{Top: 14}}, Children: []core.Node{
				u.button(fixed("切换解释器"), buttonQuiet, func() bool { return u.data().busy != "" }, func() {
					base := strings.TrimSpace(u.basePy.Get())
					if base == "" {
						u.post(func() { u.st.fail("先填基础解释器路径，例如 /usr/bin/python3") })
						return
					}
					u.selectBasePython(base)
				}),
			}},
		),
		u.kv("venv 解释器", func() string {
			if env.VenvPython == "" {
				return "（还没建）"
			}
			return env.VenvPython
		}),
		u.kv("依赖探测", func() string { return probes }),
		u.divider(),
		row(6, buttons...),
	)
}

// gpuCard 显示显卡探测结果。
func (u *ui) gpuCard(info *AppInfo) core.Node {
	gpu := info.GPU
	children := []core.Node{
		row(10,
			u.strong(fixed("GPU"), 14, func() core.Color { return u.pal().Text }),
			u.chip(func() string {
				if gpu.Available {
					return "可用"
				}
				return "未检测到"
			}, func() core.Color {
				if gpu.Available {
					return u.pal().Success
				}
				return u.pal().Warning
			}),
			fill(u.muted(func() string {
				if gpu.Error != "" {
					return gpu.Error
				}
				return "VLM 类模型（dots.ocr / DeepSeek-OCR）必须有 CUDA"
			}, 11)),
			u.button(fixed("重新检测"), buttonQuiet, nil, u.refresh),
		),
	}
	for _, d := range gpu.Devices {
		children = append(children, u.kv(fmt.Sprintf("GPU %d", d.Index), fixed(
			fmt.Sprintf("%s · %d MiB · 驱动 %s", d.Name, d.MemoryMB, d.Driver))))
	}
	return u.card(16, children...)
}

// aboutCard 是版本与文档链接。
func (u *ui) aboutCard(info *AppInfo) core.Node {
	children := []core.Node{
		row(10,
			u.strong(fixed("关于"), 14, func() core.Color { return u.pal().Text }),
			u.chip(fixed("v"+info.Version), func() core.Color { return u.pal().Accent }),
			fill(u.muted(func() string {
				return fmt.Sprintf("%d 个模型 · 清单来源 %d 个", info.Models, len(info.Sources))
			}, 11)),
		),
		u.kv("Tesseract", func() string {
			if info.Tesseract == "" {
				return "（系统里没找到 tesseract 可执行文件）"
			}
			return info.Tesseract
		}),
	}
	for _, key := range sortedKeys(info.Docs) {
		url := info.Docs[key]
		children = append(children, row(8,
			core.View{Style: core.Style{Width: core.Pt(96)}, Children: []core.Node{u.muted(fixed(key), 12)}},
			fill(u.button(fixed(url), buttonQuiet, nil, func() { u.openModelHome(url) })),
		))
	}
	return u.card(16, children...)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
