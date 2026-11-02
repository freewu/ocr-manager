package ocr

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// 内置源码必须跟仓库里的 `python/` 一一对应。
//
// 踩过的坑：`//go:embed python` 会跳过 `_` 开头的文件，`__init__.py` 和
// `__main__.py` 全被漏掉，于是打包出来的应用一启用模型就报
// `ImportError: cannot import name '__version__' from 'ocr_worker'`，
// 而开发态（直接读仓库 python/）完全正常，很难看出来。
func TestEmbeddedPythonAssetsCoverRepo(t *testing.T) {
	want := []string{}
	err := filepath.WalkDir("python", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(filepath.ToSlash(path), "python/"))
		// 生成物不算源码：__pycache__、*.egg-info
		if strings.Contains(rel, "__pycache__") || strings.Contains(rel, ".egg-info") {
			return nil
		}
		if !strings.HasSuffix(rel, ".py") && rel != "pyproject.toml" {
			return nil
		}
		want = append(want, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 python/ 失败: %v", err)
	}

	got := PythonFiles()
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("内置源码和仓库 python/ 不一致（少的多半是 //go:embed 的 glob 漏了）：\n内置(%d): %v\n仓库(%d): %v",
			len(got), got, len(want), want)
	}

	// 这三个文件是 worker 能不能起来的关键，单独点一下名
	for _, must := range []string{"ocr_worker/__init__.py", "ocr_worker/__main__.py", "ocr_worker/engines/__init__.py"} {
		if !contains(got, must) {
			t.Fatalf("内置源码缺 %s，打包出来的应用起不了 worker", must)
		}
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
