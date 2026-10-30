package main

import (
	"time"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
)

// Root 是 NSGo 应用的入口组件。
func Root(cx *core.Scope) core.Node {
	u := newUI(cx)
	u.setupTheme()
	cx.SetTitle("OCR Manager")
	cx.SetWindowFrame(core.WindowFrame{Width: 1200, Height: 840})
	// 托盘菜单里的「打开数据目录」用同一个数据根。
	trayHome = func() string {
		if info := u.data().info; info != nil {
			return info.Dirs["root"]
		}
		return ""
	}
	u.connect()
	u.poll()

	return core.View{
		StyleFunc: func() core.Style {
			p := u.pal()
			return core.Style{
				Direction:  core.Row,
				Width:      core.Pct(100),
				Height:     core.Pct(100),
				Background: p.Background,
				Color:      p.Text,
				FontSize:   14,
			}
		},
		Children: []core.Node{u.sidebar(), u.content(), u.toast()},
	}
}

// poll 定时拉状态：识别进度、依赖安装、权重下载都靠它推进界面。
// 有任务在跑时快一点，闲着时慢一点。
func (u *ui) poll() {
	go func() {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-u.ctx.Done():
				return
			case <-timer.C:
				u.refresh()
				interval := 3 * time.Second
				if job := u.data().job; job != nil && job.Status == "running" {
					interval = time.Second
				}
				if u.data().op != nil {
					// 后台在装依赖 / 下权重：快一点刷，进度条才跟得上
					interval = time.Second
				}
				for _, m := range u.data().models {
					if m.Status.State == "preparing" || m.Status.State == "loading" {
						interval = time.Second
						break
					}
				}
				timer.Reset(interval)
			}
		}
	}()
}

func (u *ui) sidebar() core.Node {
	return core.View{
		StyleFunc: func() core.Style {
			p := u.pal()
			return core.Style{
				Width:       core.Pt(226),
				Padding:     core.All(14),
				Gap:         4,
				Background:  p.Surface,
				Borders:     core.Edges{Right: 1},
				BorderColor: p.Border,
			}
		},
		Children: []core.Node{
			core.View{
				Style: core.Style{Padding: core.Edges{Left: 4, Bottom: 12}, Gap: 2},
				Children: []core.Node{
					row(9,
						core.Image{
							Source:     core.ImageSource{Data: iconPNG},
							ResizeMode: core.Contain,
							Label:      "logo",
							TestID:     "brand-logo",
							Style:      core.Style{Width: core.Pt(30), Height: core.Pt(30)},
						},
						core.View{
							Style: core.Style{Gap: 1},
							Children: []core.Node{
								u.strong(fixed("OCR Manager"), 16, func() core.Color { return u.pal().Text }),
								u.muted(fixed("多引擎文字识别"), 11),
							},
						},
					),
				},
			},
			u.navItem("recognize", "识别", "图片 / PDF → 文本", lucide.ScanText()),
			u.navItem("models", "模型", "下载 · 启用 · 卸载", lucide.Boxes()),
			u.navItem("history", "历史记录", "产物与导出", lucide.Timeline()),
			u.navItem("logs", "日志", "进度与错误", lucide.ScrollText()),
			u.navItem("settings", "设置", "目录与识别参数", lucide.Settings()),
			core.View{Style: core.Style{FlexGrow: 1}},
			u.divider(),
			u.footer(),
		},
	}
}

// footer 显示后端连接情况。
func (u *ui) footer() core.Node {
	dot := core.View{StyleFunc: func() core.Style {
		c := u.pal().Danger
		if u.data().connected {
			c = u.pal().Success
		}
		return core.Style{Width: core.Pt(8), Height: core.Pt(8), CornerRadius: 4, Background: c}
	}}
	return core.View{
		Style: core.Style{Padding: core.Edges{Top: 10}, Gap: 8},
		Children: []core.Node{
			row(7, dot,
				u.text(func() string { return u.data().status }, 12, func() core.Color { return u.pal().Text }),
			),
			u.muted(func() string {
				if e := u.data().connErr; e != "" {
					return e
				}
				return "接口 " + u.cl.base
			}, 11),
			u.text(func() string {
				if d := u.data(); d.busy != "" {
					return d.busy
				}
				return u.opLabel()
			}, 11, func() core.Color { return u.pal().Accent }),
			core.View{
				Style:    core.Style{Direction: core.Row, Gap: 6, Padding: core.Edges{Top: 2}},
				Children: []core.Node{u.button(fixed("重连"), buttonQuiet, nil, u.connect)},
			},
		},
	}
}

func (u *ui) content() core.Node {
	return core.View{
		StyleFunc: func() core.Style {
			return core.Style{FlexGrow: 1, FlexShrink: 1, Height: core.Pct(100), Padding: core.All(20), Gap: 14}
		},
		Children: []core.Node{
			core.Dynamic(func(*core.Scope) core.Node {
				switch u.page.Get() {
				case "models":
					return u.modelsPage()
				case "history":
					return u.historyPage()
				case "logs":
					return u.logsPage()
				case "settings":
					return u.settingsPage()
				default:
					return u.recognizePage()
				}
			}),
		},
	}
}
