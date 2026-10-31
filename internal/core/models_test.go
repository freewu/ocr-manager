package core

import (
	"strings"
	"testing"

	"github.com/bluefrog/ocr-manager/internal/models"
	"github.com/bluefrog/ocr-manager/internal/paths"
)

// testPaths 拿一个只活在本用例里的数据目录。
func testPaths(t *testing.T) *paths.Paths {
	t.Helper()
	t.Setenv("OCR_HOME", t.TempDir())
	p, err := paths.Resolve()
	if err != nil {
		t.Fatalf("解析数据目录失败：%v", err)
	}
	return p
}

// TestWeightsInfoManagedByEngine 只由引擎自己管权重的模型（weights = "auto"）
// 一律算权重就绪。
//
// 这类模型永远不会写「权重已就绪」标记文件，如果还去读标记，就会一直判成
// 「还没权重」，界面上「启用」永远出不来，只剩一个「安装依赖」按钮。
func TestWeightsInfoManagedByEngine(t *testing.T) {
	c := &Core{}
	for _, spec := range []models.ModelSpec{
		{ID: "rapidocr", Weights: models.WeightsAuto},
		{ID: "paddleocr", Weights: models.WeightsAuto},
		{ID: "tesseract", Weights: models.WeightsNone},
	} {
		if info := c.weightsInfo(spec); !info.Ready {
			t.Errorf("%s（weights=%s）应该算权重就绪", spec.ID, spec.WeightsMode())
		}
	}
}

// TestWeightsInfoHubNeedsMarker 走 Hub 下载的模型没标记文件就不算就绪。
func TestWeightsInfoHubNeedsMarker(t *testing.T) {
	c := &Core{Paths: testPaths(t)}
	spec := models.ModelSpec{ID: "dots", Weights: models.WeightsHub, Repo: "owner/dots"}
	if info := c.weightsInfo(spec); info.Ready {
		t.Error("没下过权重的 hub 模型不该算就绪")
	}

	if err := c.writeWeightsMarker(spec, WeightsInfo{Path: "/tmp/dots", Files: 3}); err != nil {
		t.Fatalf("写标记失败：%v", err)
	}
	info := c.weightsInfo(spec)
	if !info.Ready {
		t.Fatal("写过标记之后应该算就绪")
	}
	if info.Path != "/tmp/dots" || info.Files != 3 {
		t.Errorf("标记内容没读回来：%+v", info)
	}
}

// TestWeightsGateSkipsEngineManaged 权重由引擎自管的模型不该被「先下载权重」挡住：
// 启用和识别都走这一关，卡住的话界面上怎么点都点不动（用户看到的就是
// 「启用失败：还没下载权重」）。
func TestWeightsGateSkipsEngineManaged(t *testing.T) {
	c := &Core{Paths: testPaths(t)}
	for _, spec := range []models.ModelSpec{
		{ID: "rapidocr", Name: "RapidOCR（PP-OCR ONNX）", Weights: models.WeightsAuto, Extras: []string{"rapidocr"}},
		{ID: "mock", Name: "离线演示（Mock）", Weights: models.WeightsNone},
	} {
		if err := c.weightsGate(spec); err != nil {
			t.Errorf("%s（weights=%s）不该被权重挡住：%v", spec.ID, spec.WeightsMode(), err)
		}
	}
}

// TestWeightsGateBlocksHub 要下 Hub 权重的模型没下之前得拦住，而且报错要点名界面上的按钮。
func TestWeightsGateBlocksHub(t *testing.T) {
	c := &Core{Paths: testPaths(t)}
	spec := models.ModelSpec{ID: "dots", Name: "dots.ocr", Weights: models.WeightsHub, Repo: "owner/dots"}

	err := c.weightsGate(spec)
	if err == nil {
		t.Fatal("没下过权重的 hub 模型应该被挡住")
	}
	if !strings.Contains(err.Error(), "下载权重") {
		t.Errorf("报错要指向界面上的「下载权重」按钮：%v", err)
	}

	if err := c.writeWeightsMarker(spec, WeightsInfo{Path: "/tmp/dots"}); err != nil {
		t.Fatalf("写标记失败：%v", err)
	}
	if err := c.weightsGate(spec); err != nil {
		t.Errorf("下载完（有标记）之后不该再拦：%v", err)
	}
}
