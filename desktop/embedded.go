package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// 发布物只有一个 ocr-manager.exe：打包时把同一平台的 ocr-server 塞进 embedded/，
// 启动时如果旁边找不到 ocr-server，就把内置的那份解包到数据目录再跑。
//
// 开发机（`just dev` / `just desktop`）不需要这套：仓库里 bin/ocr-server 优先，
// 改了后端立刻生效；内置那份只在发布包里用。
//
//go:embed embedded
var embeddedAssets embed.FS

// 打包脚本写在 embedded/target.txt 里的平台标记（`goos/goarch`）。
const embedTargetFile = "embedded/target.txt"

// embeddedServer 解包内置后端，返回可执行文件的绝对路径。
//
// 返回 false 表示这个 exe 没带内置后端（开发构建）、或者带的是别的平台的
// （比如在 Windows 上交叉编了个 linux 版）。这两种情况都要让调用方继续
// 走「旁边找 ocr-server」的老路，别硬塞一个跑不起来的东西。
func embeddedServer() (string, bool) { return embeddedServerFrom(embeddedAssets) }

// embeddedServerFrom 是 embeddedServer 的真身，fsys 可替换（测试用）。
func embeddedServerFrom(fsys fs.FS) (string, bool) {
	marker, err := fs.ReadFile(fsys, embedTargetFile)
	if err != nil || !embeddedTargetMatches(string(marker)) {
		return "", false
	}
	data, err := fs.ReadFile(fsys, "embedded/"+exeName())
	if err != nil || len(data) == 0 {
		return "", false
	}
	return installEmbedded(embeddedRoot(), data)
}

// installEmbedded 把内置后端写成 `root/runtime/ocr-server-<哈希><.exe>`。
//
// 文件名带内容哈希：升级后换个文件写，不会碰到「上个版本的 ocr-server
// 还在跑、Windows 不让覆盖正在运行的 exe」这种破事。
func installEmbedded(root string, data []byte) (string, bool) {
	sum := sha256.Sum256(data)
	tag := hex.EncodeToString(sum[:])[:8]
	dir := filepath.Join(root, "runtime")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false
	}
	dest := filepath.Join(dir, fmt.Sprintf("ocr-server-%s%s", tag, exeExt()))
	if info, err := os.Stat(dest); err == nil && info.Size() == int64(len(data)) {
		return dest, true
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return "", false
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", false
	}
	pruneEmbedded(dir, filepath.Base(dest))
	return dest, true
}

// pruneEmbedded 清掉旧版本解包出来的后端（升级一次躺一个 11MB，不清会越积越多）。
//
// 删不掉的（正在跑的那个）忽略错误： Windows 不允许删运行中的 exe。
func pruneEmbedded(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == keep || !strings.HasPrefix(name, "ocr-server-") {
			continue
		}
		if exeExt() != "" && !strings.HasSuffix(name, exeExt()) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// embeddedTargetMatches 判断内置后端是不是当前平台的。
//
// 在 Windows 上交叉编了个 linux 版、或者 dev 构建根本没带后端时，这个 exe
// 不能把不对的内置后端解包出去跑——那种失败很难看出原因。
func embeddedTargetMatches(marker string) bool {
	return strings.TrimSpace(marker) == runtime.GOOS+"/"+runtime.GOARCH
}

// embeddedRoot 是解包目录的根：跟后端用同一套规则（OCR_HOME > 系统数据目录），
// 免得解包出来的 exe 和后端的数据目录分家。
func embeddedRoot() string {
	if home := strings.TrimSpace(os.Getenv("OCR_HOME")); home != "" {
		return home
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "ocr-manager")
	}
	return filepath.Join(os.TempDir(), "ocr-manager")
}

func exeExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
