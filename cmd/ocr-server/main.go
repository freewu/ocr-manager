// ocr-server 是本地 HTTP 服务（也可以被桌面壳直接嵌进进程里跑）。
//
//	go run ./cmd/ocr-server -addr 127.0.0.1:8899
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	ocr "github.com/bluefrog/ocr-manager"
	"github.com/bluefrog/ocr-manager/internal/api"
	"github.com/bluefrog/ocr-manager/internal/core"
)

func main() {
	addr := flag.String("addr", envOr("OCR_ADDR", "127.0.0.1:8899"), "监听地址")
	root := flag.String("root", "", "数据目录（默认按 OCR_HOME / 系统数据目录推导）")
	web := flag.String("web", "", "前端构建产物目录（默认自动找 web/dist）")
	token := flag.String("token", os.Getenv("OCR_TOKEN"), "访问口令（可选）")
	open := flag.Bool("open", false, "启动后打开浏览器")
	noPrewarm := flag.Bool("no-prewarm", os.Getenv("OCR_NO_PREWARM") == "1", "跳过启动时的 Hub 元数据探测")
	flag.Parse()

	app, err := core.New(core.Options{
		Root:           *root,
		BuiltinCatalog: ocr.DefaultCatalog,
		Version:        ocr.Version,
		NoPrewarm:      *noPrewarm,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		os.Exit(1)
	}
	defer app.Close()

	webDir := *web
	if webDir == "" {
		webDir = ocr.ResolveWebDir(app.Paths.RuntimeDir())
	}
	if webDir == "" {
		fmt.Fprintln(os.Stderr, "提示: 没有内置前端资源（web/dist 缺失），只提供 /api 接口")
	}

	server := api.New(app, api.Options{Addr: *addr, WebDir: webDir, Token: *token})

	fmt.Printf("OCR Manager %s\n", ocr.Version)
	fmt.Printf("  数据目录: %s\n", app.Root())
	fmt.Printf("  权重目录: %s\n", app.Paths.ModelsDir())
	fmt.Printf("  产物目录: %s\n", app.Paths.OutDir())
	fmt.Printf("  服务地址: http://%s\n", *addr)
	if webDir == "" {
		fmt.Println("  前端: 未构建（只提供 API）")
	} else {
		fmt.Printf("  前端: %s\n", webDir)
	}

	if *open {
		go func() {
			time.Sleep(400 * time.Millisecond)
			openBrowser("http://" + *addr)
		}()
	}

	// Ctrl+C 时先收尾（关 worker / 关日志）再退出
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-done:
		if err != nil {
			fmt.Fprintln(os.Stderr, "服务退出:", err)
			os.Exit(1)
		}
	case sig := <-signals:
		fmt.Println("\n收到", sig, "，正在退出…")
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = ctx
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
