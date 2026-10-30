package main

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestClientAgainstRealServer 是手动跑的联调用例：需要一台真后端。
//
//	OCR_E2E=1 OCR_API=http://127.0.0.1:8899 go test -run RealServer ./...
//
// 它验证客户端的结构体字段和 HTTP 形状跟服务端一致（字段名/时间戳/嵌套）。
func TestClientAgainstRealServer(t *testing.T) {
	if os.Getenv("OCR_E2E") == "" {
		t.Skip("设置 OCR_E2E=1 才会跑（需要一台运行中的 ocr-server）")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cl := newClient(os.Getenv("OCR_API"))
	if os.Getenv("OCR_API") == "" {
		cl = newClient(apiBase())
	}
	if !cl.ping(ctx) {
		t.Fatalf("连不上后端 %s", cl.base)
	}

	info, err := cl.info(ctx)
	if err != nil {
		t.Fatalf("取 info 失败：%v", err)
	}
	if info.Version == "" || info.Dirs["output"] == "" {
		t.Fatalf("info 字段没对上：%+v", info)
	}

	models, err := cl.models(ctx, false)
	if err != nil {
		t.Fatalf("取模型失败：%v", err)
	}
	if len(models) == 0 {
		t.Fatal("模型清单是空的")
	}
	for _, m := range models {
		if m.ID == "" || m.Name == "" || m.Status.State == "" {
			t.Fatalf("模型字段没对上：%+v", m)
		}
	}

	env, err := cl.env(ctx)
	if err != nil {
		t.Fatalf("取 env 失败：%v", err)
	}
	if env.State == "" {
		t.Fatal("env 没状态")
	}

	events, err := cl.logs(ctx, 20)
	if err != nil {
		t.Fatalf("取日志失败：%v", err)
	}
	t.Logf("info=%s 模型=%d 日志=%d 环境=%s", info.Version, len(models), len(events), env.State)

	// 有 mock 模型就跑一次识别，验证 job / history / 结果三段都能解出来
	hasMock := false
	for _, m := range models {
		if m.ID == "mock" {
			hasMock = true
		}
	}
	if !hasMock {
		return
	}
	if err := cl.activate(ctx, "mock"); err != nil {
		t.Fatalf("激活 mock 失败：%v", err)
	}
	path := os.Getenv("OCR_E2E_IMAGE")
	if path == "" {
		return
	}
	job, err := cl.recognize(ctx, "mock", []string{path}, map[string]any{"max_pages": 20, "pdf_dpi": 200})
	if err != nil {
		t.Fatalf("提交识别失败：%v", err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for job.Status == "running" && time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		job, err = cl.job(ctx, job.ID)
		if err != nil {
			t.Fatalf("取任务失败：%v", err)
		}
	}
	if job.Status != "done" {
		t.Fatalf("任务没完成：%s %s", job.Status, job.Error)
	}
	entry, result, err := cl.historyItem(ctx, job.ID)
	if err != nil {
		t.Fatalf("取结果失败：%v", err)
	}
	if entry.ID != job.ID || len(result.Pages) == 0 || entry.Pages == 0 {
		t.Fatalf("结果字段没对上：entry=%+v pages=%d", entry, len(result.Pages))
	}
	t.Logf("识别完成 %s：%d 页 %d 块，首块 %q", job.ID, job.Pages, len(result.Pages[0].Blocks), result.Pages[0].Text)
}
