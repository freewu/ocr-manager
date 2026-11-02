package main

import (
	"testing"
	"time"

	"github.com/oneo-me/nsgo/nsgotest"
)

// TestLogsSelectAcrossRows 日志页要能拖选多行再 Ctrl+C。
//
// NSGo 里「跨文字一起选」靠祖先 View 的 `Selectable`（内部叫 SelectionArea，
// 等同 web 的 selection area）；只在文字样式上给 `UserSelectText` 时，每行
// 各自是一个选区，拖到下一行就断了。这里就卡住那个字段，别被顺手删掉。
func TestLogsSelectAcrossRows(t *testing.T) {
	server := fakeServer(t)
	defer server.Close()
	t.Setenv("OCR_API", server.URL)
	t.Setenv("OCR_NO_SPAWN", "1")

	app := nsgotest.Start(t, Root, nsgotest.Size(1200, 840))
	app.Press("id:nav-logs")
	app.Settle()
	// 等假后端的历史日志回来（/api/logs 是异步拉的）
	app.WaitUntil(5*time.Second, func() bool {
		return app.Exists("text:监听 http://127.0.0.1:8899")
	})

	area := app.Find("id:logs-select")
	if !area.Props.SelectionArea {
		t.Fatal("日志容器没开 Selectable，拖选跨不过行（只能一行一行选）")
	}

	texts := 0
	var walk func(v *nsgotest.View)
	walk = func(v *nsgotest.View) {
		if v.Kind == nsgotest.KindText {
			texts++
		}
		for _, child := range v.Children {
			walk(child)
		}
	}
	walk(area)
	if texts < 2 {
		t.Fatalf("日志选区里只有 %d 段文字，多行拖选无从谈起", texts)
	}
}
