package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// healthServer 起一个只回 /api/health 的假后端，uptime / pid 由用例指定。
func healthServer(t *testing.T, uptime int64, pid int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"ok": true, "service": "ocr-server", "version": "0.1.0",
			"uptime": uptime, "pid": pid,
		})
	})
	return httptest.NewServer(mux)
}

// fakeBin 造一个假 ocr-server 文件，返回它的路径。
func fakeBin(t *testing.T, mod time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ocr-server")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("写假二进制失败：%v", err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatalf("改假二进制的 mtime 失败：%v", err)
	}
	return path
}

// TestStaleServer 二进制比进程还新 → 认定是旧进程；同期的不算。
func TestStaleServer(t *testing.T) {
	now := time.Now()
	fresh := fakeBin(t, now)
	old := fakeBin(t, now.Add(-time.Hour))

	cases := []struct {
		name string
		h    healthInfo
		bin  string
		want bool
	}{
		{"进程 1 小时前起，二进制刚编好", healthInfo{Uptime: 3600, PID: 42}, fresh, true},
		{"进程和二进制都是老早的", healthInfo{Uptime: 3600, PID: 42}, old, false},
		{"进程刚起来（二进制刚编好）", healthInfo{Uptime: 1, PID: 42}, fresh, false},
		{"老二进制没有 pid 也照样能判", healthInfo{Uptime: 3600}, fresh, true},
		{"uptime 缺失时不下判断", healthInfo{PID: 42}, fresh, false},
		{"找不到二进制时不下判断", healthInfo{Uptime: 3600, PID: 42}, "", false},
		{"二进制不存在时不下判断", healthInfo{Uptime: 3600, PID: 42}, filepath.Join(t.TempDir(), "nope"), false},
	}
	for _, c := range cases {
		if got := staleServer(c.h, c.bin); got != c.want {
			t.Errorf("%s：staleServer=%v，想要 %v", c.name, got, c.want)
		}
	}
}

// TestStartServerReusesRunning 端口上已经有一台同期启动的 ocr-server → 直接复用，不再拉进程。
func TestStartServerReusesRunning(t *testing.T) {
	server := healthServer(t, 3600, 4242)
	defer server.Close()
	t.Setenv("OCR_API", server.URL)
	t.Setenv("OCR_NO_SPAWN", "")
	t.Setenv("OCR_SERVER", fakeBin(t, time.Now().Add(-time.Hour)))

	if err := startServer(context.Background(), newClient(server.URL), nil); err != nil {
		t.Fatalf("复用现成后端失败：%v", err)
	}
	procMu.Lock()
	spawned := running != nil
	procMu.Unlock()
	if spawned {
		t.Fatal("端口上已经有后端了，不该再拉一个进程")
	}
}

// TestStartServerStaleWithoutPID 旧进程自报家门但不带 pid：没法自动结束，得给用户指路。
func TestStartServerStaleWithoutPID(t *testing.T) {
	server := healthServer(t, 3600, 0)
	defer server.Close()
	t.Setenv("OCR_API", server.URL)
	t.Setenv("OCR_NO_SPAWN", "")
	t.Setenv("OCR_SERVER", fakeBin(t, time.Now()))

	err := startServer(context.Background(), newClient(server.URL), nil)
	if err == nil {
		t.Fatal("旧进程没法自动重启，应该报错")
	}
	if !strings.Contains(err.Error(), "重连") {
		t.Fatalf("报错要告诉用户点哪里：%v", err)
	}
	if !strings.Contains(err.Error(), apiPort()) {
		t.Fatalf("报错要带上端口，方便用户自己去看：%v", err)
	}
}

// TestWaitGone 等旧进程退出：端口没动静了才算等到了。
func TestWaitGone(t *testing.T) {
	server := healthServer(t, 3600, 4242)
	cl := newClient(server.URL)

	// 端口还活着 → 等不到（ctx 先断，函数得老实返回 false，不能卡着）
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if waitGone(ctx, cl) {
		t.Fatal("端口上的后端还在应答，不该判定成已经退出")
	}

	// 进程收掉 → 端口不再应答，等到
	server.Close()
	gone, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if !waitGone(gone, cl) {
		t.Fatal("端口已经没人应答了，应该判定成已退出")
	}
}
