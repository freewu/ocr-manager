package main

import (
	"testing"

	"github.com/oneo-me/nsgo/core"
)

// TestPreviewFitContain 适应面板时按 contain 铺，长边贴边、短边留白。
func TestPreviewFitContain(t *testing.T) {
	u := &ui{previewNatW: 1000, previewNatH: 500}
	u.previewBox = core.Bounds{Size: core.Size{Width: 400, Height: 400}}
	w, h := u.previewFit()
	if w != 400 || h != 200 {
		t.Errorf("宽图应该铺满宽度：得到 %.0fx%.0f，想要 400x200", w, h)
	}

	u.previewNatW, u.previewNatH = 500, 1000
	w, h = u.previewFit()
	if w != 200 || h != 400 {
		t.Errorf("高图应该铺满高度：得到 %.0fx%.0f，想要 200x400", w, h)
	}
}

// TestClampPanKeepsImageOverPanel 没放大就不许拖；放大后最远只能拖到边缘对齐。
func TestClampPanKeepsImageOverPanel(t *testing.T) {
	u := &ui{previewNatW: 1000, previewNatH: 1000}
	u.previewBox = core.Bounds{Size: core.Size{Width: 400, Height: 400}}

	if got := u.clampPan(core.Point{X: 120, Y: -50}, 1); got != (core.Point{}) {
		t.Errorf("没放大时位移应该归零，得到 %+v", got)
	}

	// 放大 2 倍：图占 800，面板 400，可拖范围 ±200
	got := u.clampPan(core.Point{X: 999, Y: -999}, 2)
	if got.X != 200 || got.Y != -200 {
		t.Errorf("超出范围的位移应该夹到 ±200，得到 %+v", got)
	}

	got = u.clampPan(core.Point{X: 30, Y: 20}, 2)
	if got.X != 30 || got.Y != 20 {
		t.Errorf("范围内应该原样保留，得到 %+v", got)
	}
}

// TestZoomClampsToFit 缩到「适应」就停住，放大不超过上限。
func TestZoomClampsToFit(t *testing.T) {
	if got := clampZoom(0.2); got != previewZoomFit {
		t.Errorf("缩到 0.2 应该夹成 1，得到 %.2f", got)
	}
	if got := clampZoom(previewZoomMax * 4); got != previewZoomMax {
		t.Errorf("超过上限应该夹成 %.0f，得到 %.2f", float64(previewZoomMax), got)
	}
	if got := clampZoom(2.5); got != 2.5 {
		t.Errorf("范围内应该原样保留，得到 %.2f", got)
	}
}
