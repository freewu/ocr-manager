package main

import (
	"fmt"
	"strings"

	"github.com/oneo-me/nsgo/core"
)

// logsPage 是「日志」页：最近事件的流水，错在最后一行也能看见。
func (u *ui) logsPage() core.Node {
	lines := u.data().logs
	rows := make([]core.Node, 0, len(lines))
	for _, line := range lines {
		l := line
		rows = append(rows, row(8,
			core.View{Style: core.Style{Width: core.Pt(56)}, Children: []core.Node{
				u.mono(fixed(l.at.Format("15:04:05")), 11, func() core.Color { return u.pal().TextMuted }),
			}},
			core.View{Style: core.Style{Width: core.Pt(64)}, Children: []core.Node{
				u.mono(fixed(l.source), 11, u.levelTone(l.level)),
			}},
			fill(u.mono(fixed(l.message), 11, func() core.Color {
				if strings.ToLower(l.level) == "error" {
					return u.pal().Danger
				}
				return u.pal().Text
			})),
		))
	}
	if len(rows) == 0 {
		rows = append(rows, u.muted(fixed("还没有日志。"), 12))
	}

	// 倒序显示（最新在最上面）
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}

	actions := []core.Node{
		u.button(fixed("复制全部"), buttonQuiet, nil, func() {
			var b strings.Builder
			for _, line := range u.data().logs {
				fmt.Fprintf(&b, "%s %-8s %s\n", line.at.Format("15:04:05"), line.source, line.message)
			}
			u.copyText(b.String(), "日志")
		}),
		u.button(func() string { return "刷新" }, buttonQuiet, nil, u.refresh),
	}

	return u.pageRoot("日志", "进度、状态变化、依赖安装输出都在这里（服务端同时写 logs/日期.log）", actions,
		core.View{
			StyleFunc: func() core.Style {
				p := u.pal()
				return core.Style{
					FlexGrow:     1,
					Height:       core.Pct(100),
					Padding:      core.All(12),
					CornerRadius: 12,
					BorderWidth:  1,
					BorderColor:  p.Border,
					Background:   p.Surface,
					// 日志要能用鼠标选中、Ctrl+C 复制
					UserSelect: core.UserSelectText,
				}
			},
			Children: []core.Node{core.Scroll{
				TestID: "page-scroll",
				// HideIndicator：见 ui.go 里 scrollList 的注释（NSGo 拖滚动条会 panic）
				HideIndicator: true,
				Style:         core.Style{FlexGrow: 1, Width: core.Pct(100)},
				Children:      []core.Node{core.View{Style: core.Style{Gap: 4, Width: core.Pct(100)}, Children: rows}},
			}},
		},
	)
}
