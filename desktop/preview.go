package main

import (
	"bytes"
	"fmt"
	"image"
	// 预览要认图片头，格式注册在这里
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/oneo-me/nsgo/core"
)

// 预览的放大与拖动。
//
// nsgo 的 Scroll 虽然带 ZoomScale，但只有 iOS / Android / web 真放得动，
// 桌面端（Windows / Linux）忽略它；所以这里自己算：图片按 contain 铺满面板
// （zoom = 1 就是「适应面板」），放大用 Transform 的 Scale，拖动改 Translate，
// 父容器 Overflow Hidden 负责裁掉出界的部分。
const (
	// previewZoomFit 是「适应面板」的倍数。
	previewZoomFit float32 = 1
	// previewZoomMax 是最多放大到「适应」的多少倍。
	previewZoomMax float32 = 12
	// previewZoomStep 是点一次 ＋ / － 的倍率。
	previewZoomStep float32 = 1.25
	// previewPanSlop 是拖多少点才开始平移，小一点更跟手。
	previewPanSlop core.Points = 4
)

// imageSize 只读文件头拿原始像素宽高（解不出来就返回 0，退化成不定界拖动）。
func imageSize(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// setPreviewSize 记下当前预览图的原始尺寸（换图时调用）。
func (u *ui) setPreviewSize(data []byte) {
	u.previewNatW, u.previewNatH = imageSize(data)
}

// zoomIn / zoomOut 按档位缩放。
func (u *ui) zoomIn()  { u.setZoom(u.previewZoom.Get() * previewZoomStep) }
func (u *ui) zoomOut() { u.setZoom(u.previewZoom.Get() / previewZoomStep) }

// clampZoom 把倍数夹在「适应」和上限之间。
func clampZoom(next float32) float32 {
	if next < previewZoomFit {
		return previewZoomFit
	}
	if next > previewZoomMax {
		return previewZoomMax
	}
	return next
}

// setZoom 改缩放倍数，顺手把位移夹回合法范围。
func (u *ui) setZoom(next float32) {
	next = clampZoom(next)
	u.previewZoom.Set(next)
	u.previewPan.Set(u.clampPan(u.previewPan.Get(), next))
}

// resetZoom 回到「适应面板」，位移清零。
func (u *ui) resetZoom() {
	u.previewZoom.Set(previewZoomFit)
	u.previewPan.Set(core.Point{})
}

// previewFit 是图片按 contain 摆进面板后的实际尺寸（点）。
func (u *ui) previewFit() (float32, float32) {
	boxW, boxH := float32(u.previewBox.Size.Width), float32(u.previewBox.Size.Height)
	if boxW <= 0 || boxH <= 0 || u.previewNatW <= 0 || u.previewNatH <= 0 {
		return boxW, boxH
	}
	aspect := float32(u.previewNatW) / float32(u.previewNatH)
	fitW, fitH := boxW, boxW/aspect
	if fitH > boxH {
		fitH, fitW = boxH, boxH*aspect
	}
	return fitW, fitH
}

// clampPan 把位移夹在「放大后多出来的量」里，别把图拖出面板。
func (u *ui) clampPan(pan core.Point, zoom float32) core.Point {
	boxW, boxH := float32(u.previewBox.Size.Width), float32(u.previewBox.Size.Height)
	fitW, fitH := u.previewFit()
	limitX := max((fitW*zoom-boxW)/2, 0)
	limitY := max((fitH*zoom-boxH)/2, 0)
	x := min(max(float32(pan.X), -limitX), limitX)
	y := min(max(float32(pan.Y), -limitY), limitY)
	return core.Point{X: core.Points(x), Y: core.Points(y)}
}

// onPreviewPan 处理预览里的拖动：位移 = 按下时的位移 + 这次拖了多少。
func (u *ui) onPreviewPan(ev core.PanEvent) {
	if u.previewZoom.Get() <= previewZoomFit {
		return // 没放大就没得拖，别把页面滚动抢走
	}
	switch ev.Phase {
	case core.GestureBegan:
		u.panBase = u.previewPan.Get()
	case core.GestureChanged, core.GestureEnded:
		pan := core.Point{X: u.panBase.X + ev.Translation.X, Y: u.panBase.Y + ev.Translation.Y}
		u.previewPan.Set(u.clampPan(pan, u.previewZoom.Get()))
	}
}

// onPreviewPinch 处理触控板 / Ctrl+滚轮的缩放（Windows 把它报成 pinch）。
func (u *ui) onPreviewPinch(ev core.GestureEvent) {
	switch ev.Phase {
	case core.GestureBegan:
		u.pinchBase = u.previewZoom.Get()
	case core.GestureChanged:
		scale := u.pinchBase
		if ev.Value > 0 {
			scale *= ev.Value
		}
		u.setZoom(scale)
	}
}

// previewZoomText 是给界面看的百分比（100% = 适应面板）。
func (u *ui) previewZoomText() string {
	return fmt.Sprintf("%.0f%%", u.previewZoom.Get()*100)
}
