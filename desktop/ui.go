package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
)

// 视觉基线：深色为主的管理台，浅色跟随系统切换。

func (u *ui) setupTheme() {
	t := core.DefaultTheme()

	l := &t.Light
	l.Background = core.RGB(0xf5f6f8)
	l.Surface = core.RGB(0xffffff)
	l.SurfaceHover = core.RGB(0xeff1f4)
	l.SurfacePressed = core.RGB(0xe4e7ec)
	l.Border = core.RGB(0xdfe3e9)
	l.Text = core.RGB(0x1b1f27)
	l.TextMuted = core.RGB(0x616b7a)
	l.Accent = core.RGB(0x2f6fed)
	l.AccentHover = core.RGB(0x4a83f2)
	l.AccentPressed = core.RGB(0x2459c6)
	l.AccentText = core.RGB(0xffffff)
	l.Success = core.RGB(0x12885b)
	l.Warning = core.RGB(0xa8700c)
	l.Danger = core.RGB(0xc1343c)
	l.Selection = core.RGBA(0x2f6fed, 0.22)

	d := &t.Dark
	d.Background = core.RGB(0x080d17)
	d.Surface = core.RGB(0x101a2c)
	d.SurfaceHover = core.RGB(0x16233a)
	d.SurfacePressed = core.RGB(0x1d2b45)
	d.Border = core.RGB(0x1d2b45)
	d.Text = core.RGB(0xe7edff)
	d.TextMuted = core.RGB(0x8a9bc0)
	d.Accent = core.RGB(0x4f8cff)
	d.AccentHover = core.RGB(0x6ba0ff)
	d.AccentPressed = core.RGB(0x3b78e0)
	d.AccentText = core.RGB(0x06101f)
	d.Success = core.RGB(0x3ecf8e)
	d.Warning = core.RGB(0xf2b13c)
	d.Danger = core.RGB(0xf0616c)
	d.Selection = core.RGBA(0x4c8dff, 0.32)

	t.Radius = 10
	u.cx.SetTheme(t)
}

func (u *ui) pal() core.Palette { return u.cx.Palette() }

// ---------------------------------------------------------------- 文本

type colorFn func() core.Color

func (u *ui) text(get func() string, size core.Points, color colorFn) core.Node {
	return core.Text{
		ContentFunc: get,
		StyleFunc: func() core.Style {
			return core.Style{FontSize: size, Color: color(), LineHeightMultiple: 1.35}
		},
	}
}

func (u *ui) strong(get func() string, size core.Points, color colorFn) core.Node {
	return core.Text{
		ContentFunc: get,
		StyleFunc: func() core.Style {
			return core.Style{FontSize: size, Color: color(), FontWeight: core.FontWeightSemibold, LineHeightMultiple: 1.3}
		},
	}
}

func (u *ui) muted(get func() string, size core.Points) core.Node {
	return u.text(get, size, func() core.Color { return u.pal().TextMuted })
}

func (u *ui) mono(get func() string, size core.Points, color colorFn) core.Node {
	return core.Text{
		ContentFunc: get,
		StyleFunc: func() core.Style {
			return core.Style{FontSize: size, Color: color(), FontFamily: core.FontMonospace, LineHeightMultiple: 1.4}
		},
	}
}

// fixed 把常量包成绑定，省得每次写闭包。
func fixed(s string) func() string { return func() string { return s } }

// ---------------------------------------------------------------- 布局

func row(gap core.Points, children ...core.Node) core.Node {
	return core.View{
		Style:    core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: gap},
		Children: children,
	}
}

// fill 是「占满剩余空间、但可以被压缩」的容器，装文字的地方用它。
//
// nsgo 的默认值照 React Native：FlexShrink 是 0。行里放一段长文本时容器会按
// max-content 撑开而不是折行，右边按钮会被挤出屏幕。
func fill(children ...core.Node) core.Node {
	return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1}, Children: children}
}

func (u *ui) card(padding core.Points, children ...core.Node) core.Node {
	return core.View{
		StyleFunc: func() core.Style {
			p := u.pal()
			return core.Style{
				Padding:      core.All(padding),
				Gap:          10,
				CornerRadius: 12,
				BorderWidth:  1,
				Background:   p.Surface,
				BorderColor:  p.Border,
			}
		},
		Children: children,
	}
}

func (u *ui) divider() core.Node {
	return core.View{StyleFunc: func() core.Style {
		return core.Style{Height: core.Pt(1), Background: u.pal().Border, Width: core.Pct(100)}
	}}
}

// ---------------------------------------------------------------- 交互件

type buttonKind int

const (
	buttonQuiet buttonKind = iota
	buttonPrimary
	buttonDanger
)

func (u *ui) button(label func() string, kind buttonKind, disabled func() bool, onPress func(), testID ...string) core.Node {
	off := func() bool { return disabled != nil && disabled() }
	id := ""
	if len(testID) > 0 {
		id = testID[0]
	}
	return base.Button{
		TestID:   id,
		Disabled: off,
		OnPress: func(core.PressEvent) {
			if onPress != nil && !off() {
				onPress()
			}
		},
		StyleFunc: func() core.Style {
			p := u.pal()
			s := core.Style{
				Padding:      core.Symmetric(6, 13),
				CornerRadius: 8,
				BorderWidth:  1,
				Background:   p.Surface,
				BorderColor:  p.Border,
				Hover:        &core.Style{Background: p.SurfaceHover},
				Active:       &core.Style{Background: p.SurfacePressed},
			}
			switch kind {
			case buttonPrimary:
				s.Background, s.BorderColor = p.Accent, p.Accent
				s.Hover = &core.Style{Background: p.AccentHover, BorderColor: p.AccentHover}
				s.Active = &core.Style{Background: p.AccentPressed, BorderColor: p.AccentPressed}
			case buttonDanger:
				s.Hover = &core.Style{Background: p.Danger.Faded(0.16), BorderColor: p.Danger.Faded(0.5)}
			}
			if off() {
				s.Opacity = core.Alpha(0.42)
			}
			return s
		},
		Children: []core.Node{core.Text{
			ContentFunc: label,
			StyleFunc: func() core.Style {
				color := u.pal().Text
				if kind == buttonPrimary {
					color = u.pal().AccentText
				}
				return core.Style{FontSize: 13, Color: color, FontWeight: core.FontWeightMedium}
			},
		}},
	}
}

// chip 是一个小标签；tone 决定文字与底色。
func (u *ui) chip(get func() string, tone colorFn) core.Node {
	return core.View{
		StyleFunc: func() core.Style {
			c := tone()
			return core.Style{
				Padding:      core.Symmetric(2, 8),
				CornerRadius: 6,
				Background:   c.Faded(0.14),
			}
		},
		Children: []core.Node{u.text(get, 11, tone)},
	}
}

// tagChips 把模型的 tags 渲染成一行小标签。
func (u *ui) tagChips(tags []string) core.Node {
	chips := make([]core.Node, 0, len(tags))
	for _, tag := range tags {
		t := tag
		chips = append(chips, u.chip(fixed(t), func() core.Color {
			if isLicenseTag(t) {
				return u.pal().Warning
			}
			return u.pal().Accent
		}))
	}
	return core.View{
		Style:    core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 6},
		Children: chips,
	}
}

func isLicenseTag(tag string) bool {
	return tag == "non-commercial" || tag == "gpl" || tag == "cc-by-nc"
}

// progressBar 是一条细进度条；percent 为 nil 表示不确定态。
func (u *ui) progressBar(percent func() *float64) core.Node {
	width := func() float32 {
		p := percent()
		if p == nil {
			return 100
		}
		v := float32(*p)
		if v < 2 {
			v = 2
		}
		if v > 100 {
			v = 100
		}
		return v
	}
	return core.View{
		StyleFunc: func() core.Style {
			return core.Style{Height: core.Pt(6), CornerRadius: 3, Background: u.pal().SurfacePressed}
		},
		Children: []core.Node{core.View{
			StyleFunc: func() core.Style {
				c := u.pal().Accent
				if percent() == nil {
					c = c.Faded(0.35)
				}
				return core.Style{Width: core.Pct(width()), Height: core.Pt(6), CornerRadius: 3, Background: c}
			},
		}},
	}
}

// navItem 是左栏的一颗导航：名称 + 一行说明。
func (u *ui) navItem(id, label, hint string, svg *core.SVG) core.Node {
	active := func() bool { return u.page.Get() == id }
	tint := func() core.Color {
		if active() {
			return u.pal().Accent
		}
		return u.pal().TextMuted
	}
	return base.Button{
		TestID:   "nav-" + id,
		Selected: active,
		OnPress:  func(core.PressEvent) { u.page.Set(id) },
		StyleFunc: func() core.Style {
			p := u.pal()
			s := core.Style{
				Direction:    core.Row,
				AlignItems:   core.AlignCenter,
				Gap:          10,
				Padding:      core.Symmetric(7, 10),
				CornerRadius: 8,
				Hover:        &core.Style{Background: p.SurfaceHover},
			}
			if active() {
				s.Background = p.Accent.Faded(0.16)
			}
			return s
		},
		Children: []core.Node{
			core.Icon{SVG: svg, StyleFunc: func() core.Style {
				return core.Style{Width: core.Pt(16), Height: core.Pt(16), Color: tint()}
			}},
			core.View{Style: core.Style{Gap: 1}, Children: []core.Node{
				core.Text{ContentFunc: fixed(label), StyleFunc: func() core.Style {
					return core.Style{FontSize: 14, Color: tint(), FontWeight: core.FontWeightMedium}
				}},
				core.Text{ContentFunc: fixed(hint), StyleFunc: func() core.Style {
					return core.Style{FontSize: 11, Color: u.pal().TextMuted}
				}},
			}},
		},
	}
}

// ---------------------------------------------------------------- 语义色

// stateTone 把模型状态映射成颜色。
func (u *ui) stateTone(state func() string) colorFn {
	return func() core.Color {
		p := u.pal()
		switch state() {
		case "ready":
			return p.Success
		case "running":
			return p.Accent
		case "preparing", "loading":
			return p.Warning
		case "error":
			return p.Danger
		default:
			return p.TextMuted
		}
	}
}

func (u *ui) levelTone(level string) colorFn {
	return func() core.Color {
		p := u.pal()
		l := strings.ToLower(level)
		switch {
		case l == "error":
			return p.Danger
		case strings.HasPrefix(l, "warn"):
			return p.Warning
		case l == "progress" || l == "status":
			return p.Accent
		case l == "env":
			return p.Success
		default:
			return p.TextMuted
		}
	}
}

// ---------------------------------------------------------------- 组合

// kv 是一行「标签：值」。
func (u *ui) kv(key string, value func() string) core.Node {
	return row(10,
		core.View{
			Style:    core.Style{Width: core.Pt(96)},
			Children: []core.Node{u.muted(fixed(key), 12)},
		},
		core.View{
			Style:    core.Style{FlexGrow: 1, FlexShrink: 1},
			Children: []core.Node{u.mono(value, 12, func() core.Color { return u.pal().Text })},
		},
	)
}

// pageHeader 是每个页面的标题区。
func (u *ui) pageHeader(title, subtitle string, actions ...core.Node) core.Node {
	right := core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: actions}
	return row(12,
		core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, Gap: 2}, Children: []core.Node{
			u.strong(fixed(title), 20, func() core.Color { return u.pal().Text }),
			u.muted(fixed(subtitle), 12),
		}},
		right,
	)
}

// pageRoot 是各页面的共同外壳：标题、提示、内容。
func (u *ui) pageRoot(title, subtitle string, actions []core.Node, body ...core.Node) core.Node {
	head := u.pageHeader(title, subtitle, actions...)
	return core.View{
		Style:    core.Style{FlexGrow: 1, Height: core.Pct(100), Gap: 14},
		Children: append([]core.Node{head}, body...),
	}
}

// scrollList 把一组卡片放进可滚动的列里。
func scrollList(children []core.Node) core.Node {
	return core.Scroll{
		TestID: "page-scroll",
		Style:  core.Style{FlexGrow: 1, Width: core.Pct(100)},
		Children: []core.Node{core.View{
			Style:    core.Style{Gap: 12, Padding: core.Edges{Right: 4, Bottom: 8}, Width: core.Pct(100)},
			Children: children,
		}},
	}
}

// input 是平台原生输入框；value 用信号做受控绑定。
func (u *ui) input(value *core.Signal[string], placeholder string, multiline bool, minHeight core.Points, testID ...string) core.Node {
	id := ""
	if len(testID) > 0 {
		id = testID[0]
	}
	return base.Input{
		TestID:      id,
		Value:       value.Get,
		OnChange:    value.Set,
		Placeholder: placeholder,
		Multiline:   multiline,
		StyleFunc: func() core.Style {
			p := u.pal()
			return core.Style{
				Padding:      core.Symmetric(9, 11),
				CornerRadius: 9,
				BorderWidth:  1,
				BorderColor:  p.Border,
				Background:   p.Background,
				Color:        p.Text,
				FontSize:     14,
				MinHeight:    core.Pt(minHeight),
			}
		},
	}
}

// toastLifetime 是一条提示在右下角停多久。
const toastLifetime = 8 * time.Second

func (u *ui) toastText() (string, colorFn) {
	d := u.data()
	if d.toastAt == 0 {
		return "", nil
	}
	if time.Since(time.UnixMilli(d.toastAt)) > toastLifetime {
		return "", nil
	}
	if d.err != "" {
		return d.err, func() core.Color { return u.pal().Danger }
	}
	if d.notice != "" {
		return d.notice, func() core.Color { return u.pal().Success }
	}
	return "", nil
}

// toast 是右下角浮起来的提示：错误与成功都走它，不挤占页面布局。
func (u *ui) toast() core.Node {
	return core.Show{
		When: func() bool { text, _ := u.toastText(); return text != "" },
		Then: func(*core.Scope) core.Node {
			text, tone := u.toastText()
			return core.View{
				TestID: "toast",
				StyleFunc: func() core.Style {
					c := tone()
					return core.Style{
						Position:     core.Fixed,
						Right:        core.Pt(22),
						Bottom:       core.Pt(22),
						Width:        core.Pt(380),
						Padding:      core.Symmetric(10, 12),
						CornerRadius: 10,
						BorderWidth:  1,
						BorderColor:  c.Faded(0.45),
						Background:   u.pal().Surface,
					}
				},
				Children: []core.Node{
					core.View{
						Style:    core.Style{Padding: core.Edges{Right: 22}},
						Children: []core.Node{u.text(fixed(text), 12, tone)},
					},
					core.View{
						Style: core.Style{Position: core.Absolute, Top: core.Pt(2), Right: core.Pt(2)},
						Children: []core.Node{base.Button{
							TestID: "toast-close",
							OnPress: func(core.PressEvent) {
								u.post(func() { u.st.clearToast(); u.bump() })
							},
							StyleFunc: func() core.Style {
								p := u.pal()
								return core.Style{
									Padding:      core.Symmetric(1, 6),
									CornerRadius: 6,
									Hover:        &core.Style{Background: p.SurfaceHover},
								}
							},
							Children: []core.Node{u.text(fixed("×"), 14, func() core.Color { return u.pal().TextMuted })},
						}},
					},
				},
			}
		},
	}
}

func joinOr(values []string, or string) string {
	if len(values) == 0 {
		return or
	}
	return strings.Join(values, " · ")
}

func fmtBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	f, i := float64(n), 0
	for f >= unit && i < len(units) {
		f /= unit
		i++
	}
	if f < 10 {
		return fmt.Sprintf("%.1f %s", f, units[i-1])
	}
	return fmt.Sprintf("%.0f %s", f, units[i-1])
}

// clockText 是「2026-10-30 11:57」这种时间戳。
func clockText(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04")
}

func percentOf(job *Job) *float64 {
	if job == nil {
		return nil
	}
	v := job.Progress * 100
	return &v
}
