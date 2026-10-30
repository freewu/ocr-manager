// Package ocr 把 python worker 源码编进二进制，运行时解包到 `$OCR_HOME/runtime/python`。
//
// 这样发布物只有一个可执行文件，首次启动即可用（无需用户手动准备 python 代码）。
package ocr

import (
	"embed"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// Version 是应用版本（发布时可用 -ldflags "-X ...Version=x.y.z" 覆盖）。
var Version = "0.1.0"

//go:embed python
var pythonAssets embed.FS

// pythonRoot 是 embed 里的顶层目录名。
const pythonRoot = "python"

// EnsurePythonAssets 解包内置 python 源码（内容不同才写入，可重复调用），返回写入的文件数。
func EnsurePythonAssets(target string) (int, error) {
	written := 0
	err := fs.WalkDir(pythonAssets, pythonRoot, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, ok := strings.CutPrefix(name, pythonRoot+"/")
		if !ok || rel == "" {
			return nil
		}
		content, err := pythonAssets.ReadFile(name)
		if err != nil {
			return nil
		}
		dest := filepath.Join(target, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if existing, err := os.ReadFile(dest); err == nil && string(existing) == string(content) {
			return nil
		}
		if err := os.WriteFile(dest, content, 0o644); err != nil {
			return err
		}
		written++
		return nil
	})
	return written, err
}

// PythonFiles 列出内置 python 源码的相对路径（给测试用）。
func PythonFiles() []string {
	out := []string{}
	_ = fs.WalkDir(pythonAssets, pythonRoot, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if rel, ok := strings.CutPrefix(name, pythonRoot+"/"); ok {
			out = append(out, rel)
		}
		return nil
	})
	return out
}

// ResolvePythonDir 返回 python 源码目录：优先仓库内开发目录，其次运行时解包目录。
func ResolvePythonDir(runtimeDir string) string {
	for _, candidate := range devPythonDirs() {
		if fileExists(filepath.Join(candidate, "ocr_worker", "__main__.py")) {
			return candidate
		}
	}
	return filepath.Join(runtimeDir, "python")
}

// devPythonDirs 是开发态可能放 `python/` 的目录（源码树 + 当前工作目录链）。
func devPythonDirs() []string {
	out := []string{}
	if _, file, _, ok := runtime.Caller(0); ok {
		out = append(out, filepath.Join(filepath.Dir(file), "python"))
	}
	if cwd, err := os.Getwd(); err == nil {
		for dir := cwd; ; dir = filepath.Dir(dir) {
			out = append(out, filepath.Join(dir, "python"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	for idx, dir := range out {
		out[idx] = filepath.Clean(dir)
	}
	return out
}

// ResolveWebDir 返回前端构建产物目录（`web/dist`），开发态直接用仓库里的。
func ResolveWebDir(runtimeDir string) string {
	candidates := []string{}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Join(filepath.Dir(file), "web", "dist"))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "web", "dist"))
	}
	candidates = append(candidates, filepath.Join(runtimeDir, "web"))
	for _, dir := range candidates {
		if fileExists(path.Join(dir, "index.html")) {
			return dir
		}
	}
	return ""
}

func fileExists(file string) bool {
	info, err := os.Stat(file)
	return err == nil && !info.IsDir()
}
