package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/oneo-me/nsgo/nsgotest"
)

// TestMain 让 `nsgo test` 能在真实平台上跑同一批用例。
func TestMain(m *testing.M) { nsgotest.Main(m) }

// fakeServer 起一个只回静态 JSON 的假后端：UI 只关心形状，不关心真识别。
func fakeServer(t *testing.T) *httptest.Server {
	t.Helper()

	info := map[string]any{
		"version": "0.1.0",
		"root":    "/tmp/ocr-home",
		"dirs": map[string]string{
			"root": "/tmp/ocr-home", "models": "/tmp/ocr-home/models",
			"output": "/tmp/ocr-home/out", "logs": "/tmp/ocr-home/logs",
			"venv": "/tmp/ocr-home/venv", "python": "/tmp/ocr-home/runtime/python",
		},
		"active": "",
		"settings": map[string]any{
			"default_model": "rapidocr", "active_model": "", "hf_endpoint": "https://huggingface.co",
			"auto_load": true, "max_pages": 20, "pdf_dpi": 200, "max_side": 0, "options": map[string]any{},
		},
		"env": map[string]any{
			"state": "waiting", "message": "venv 已建好，还没装引擎依赖",
			"base": "/usr/bin/python3", "venv_python": "/tmp/ocr-home/venv/bin/python",
			"venv_ready": true, "extras": map[string]bool{"rapidocr": true},
			"probe_errors": []string{}, "updated_at": 1,
		},
		"gpu": map[string]any{
			"available": false, "devices": []any{}, "error": "没找到 nvidia-smi", "checked_at": 1,
		},
		"models": 2, "visible": 1,
		"sources": []string{"builtin"}, "tesseract": "", "docs": map[string]string{},
	}

	mock := map[string]any{
		"id": "mock", "name": "离线演示（Mock）", "engine": "mock", "description": "不加载模型，返回固定示例",
		"languages": []string{"zh", "en"}, "tasks": []string{"text"}, "weights": "none",
		"extras": []string{}, "requires_gpu": false, "platforms": []any{}, "hidden": false,
		"size_hint": "", "tags": []string{"不需要下载"}, "featured": false, "probe": false, "options": map[string]any{},
		"status": map[string]any{
			"id": "mock", "state": "ready", "message": "已加载", "error": "", "loaded": true,
			"weights_ready": true, "needs_download": false, "extras": []string{}, "extras_ready": true,
			"requires_gpu": false, "gpu_ok": true, "workers": 1,
			"weights": map[string]any{"ready": true, "path": "", "bytes": 0, "files": 0, "at": 0},
			"repo":    nil, "updated_at": 1,
		},
	}
	rapid := map[string]any{
		"id": "rapidocr", "name": "RapidOCR（PP-OCR ONNX）", "engine": "rapidocr", "description": "CPU 上就能跑的中英文 OCR",
		"languages": []string{"zh", "en"}, "tasks": []string{"text"}, "weights": "auto",
		"extras": []string{"rapidocr"}, "requires_gpu": false, "platforms": []any{}, "hidden": false,
		"size_hint": "~15 MB", "tags": []string{"CPU 友好"}, "featured": true, "probe": true, "options": map[string]any{},
		"status": map[string]any{
			"id": "rapidocr", "state": "idle", "message": "还没下载权重", "error": "", "loaded": false,
			"weights_ready": false, "needs_download": true, "extras": []string{"rapidocr"}, "extras_ready": false,
			"requires_gpu": false, "gpu_ok": true, "workers": 0,
			"weights": map[string]any{"ready": false, "path": "", "bytes": 0, "files": 0, "at": 0},
			"repo":    nil, "updated_at": 1,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"ok": true, "service": "ocr-server", "version": "0.1.0", "uptime": 1})
	})
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, _ *http.Request) { writeJSON(t, w, info) })
	mux.HandleFunc("/api/models", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"models": []any{mock, rapid}})
	})
	mux.HandleFunc("/api/env", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"env": info["env"]})
	})
	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"events": []any{
			map[string]any{"type": "log", "ts": 1, "source": "api", "level": "info", "message": "监听 http://127.0.0.1:8899"},
			map[string]any{"type": "env", "ts": 2, "source": "env", "level": "info", "message": "venv 就绪", "state": "waiting"},
		}})
	})
	mux.HandleFunc("/api/history", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"items": []any{}})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"ok": true, "job": nil})
	})

	return httptest.NewServer(mux)
}

func writeJSON(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("写假响应失败：%v", err)
	}
}

// TestShellRenders 验证外壳能起来、五个页面都能渲染。
func TestShellRenders(t *testing.T) {
	server := fakeServer(t)
	defer server.Close()
	t.Setenv("OCR_API", server.URL)
	t.Setenv("OCR_NO_SPAWN", "1")

	app := nsgotest.Start(t, Root, nsgotest.Size(1200, 840))
	app.Find("id:brand-logo") // 图标来自 logo.png（scripts/make_icon.py 生成）
	app.Find("text:OCR Manager")
	app.Find("text:多引擎文字识别")

	// 识别页：多文件选择 + 预览分栏 + 模型下拉 + 手动路径兜底
	app.Find("text:选文件 → 选模型 → 出 text / markdown / 分块，可导出 Excel")
	app.Find("id:recognize-pick")
	app.Find("id:recognize-split")
	app.Find("id:recognize-paths")
	app.Find("id:recognize-toggle-preview")

	// 模型页
	app.Press("id:nav-models")
	app.Settle()
	app.WaitFor("text:RapidOCR（PP-OCR ONNX）", 5*time.Second)

	// 模型列表回来之后，识别页的模型选择是个下拉
	app.Press("id:nav-recognize")
	app.Settle()
	app.WaitFor("id:recognize-model-select", 5*time.Second)
	app.Find("id:page-scroll")

	// 历史记录
	app.Press("id:nav-history")
	app.Settle()
	app.Find("text:还没有记录：先跑一次识别。")

	// 日志
	app.Press("id:nav-logs")
	app.Settle()
	app.Find("text:进度、状态变化、依赖安装输出都在这里（服务端同时写 logs/日期.log）")

	// 设置
	app.Press("id:nav-settings")
	app.Settle()
	app.WaitFor("text:PDF 最多页数", 5*time.Second)
	app.Find("id:settings-save")
}

// TestToastOnEmptyRecognize 识别页什么都不填就点开始 → 右下角弹提示。
func TestToastOnEmptyRecognize(t *testing.T) {
	server := fakeServer(t)
	defer server.Close()
	t.Setenv("OCR_API", server.URL)
	t.Setenv("OCR_NO_SPAWN", "1")

	app := nsgotest.Start(t, Root, nsgotest.Size(1200, 840))
	app.WaitFor("id:recognize-start", 5*time.Second)
	app.Press("id:recognize-start")
	app.WaitFor("id:toast", 3*time.Second)
}

// TestPagesScroll 每个页面的滚动容器高度必须有上界，不能被内容撑开。
func TestPagesScroll(t *testing.T) {
	server := fakeServer(t)
	defer server.Close()
	t.Setenv("OCR_API", server.URL)
	t.Setenv("OCR_NO_SPAWN", "1")

	app := nsgotest.Start(t, Root, nsgotest.Size(1200, 840))
	for _, nav := range []string{"nav-recognize", "nav-models", "nav-logs", "nav-settings"} {
		app.Press("id:" + nav)
		app.Settle()
		if !app.Exists("id:page-scroll") {
			t.Fatalf("%s 页没有滚动容器", nav)
		}
		scroll := app.Find("id:page-scroll")
		if scroll.Frame.Size.Height >= 841 {
			t.Fatalf("%s 页的滚动容器被内容撑到 %v 高", nav, scroll.Frame.Size.Height)
		}
		space := scroll.Content.Height - scroll.Frame.Size.Height
		if space <= 0 {
			continue // 这页正好放得下
		}
		app.Scroll("id:page-scroll", space+50)
		app.Settle()
		if got := app.Find("id:page-scroll").Offset.Y; got <= 0 {
			t.Fatalf("%s 页滚不动：滚了 %v 还是停在顶部", nav, space)
		}
	}
}
