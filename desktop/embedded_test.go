package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"
)

// 整个流程：标记对得上 → 从 fake 的 embed 里解开 → 落在 OCR_HOME 下。
func TestEmbeddedServerFrom(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OCR_HOME", home)
	mine := runtime.GOOS + "/" + runtime.GOARCH
	fsys := fstest.MapFS{
		embedTargetFile:         {Data: []byte(mine + "\n")},
		"embedded/" + exeName(): {Data: []byte("fake-server")},
		"embedded/README.md":    {Data: []byte("占位")},
	}
	path, ok := embeddedServerFrom(fsys)
	if !ok {
		t.Fatal("带齐内置后端时应该解包成功")
	}
	if filepath.Dir(path) != filepath.Join(home, "runtime") {
		t.Fatalf("应该解到 %s，实际 %s", filepath.Join(home, "runtime"), path)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "fake-server" {
		t.Fatalf("解出来的内容不对：%q %v", data, err)
	}

	// 标记对不上（在 Windows 上交叉编了个 linux 版）：不能硬跑内置后端
	wrong := fstest.MapFS{
		embedTargetFile:         {Data: []byte("plan9/mips\n")},
		"embedded/" + exeName(): {Data: []byte("fake-server")},
	}
	if path, ok := embeddedServerFrom(wrong); ok {
		t.Fatalf("平台不匹配不该用内置后端，却返回了 %s", path)
	}
}

// 内置后端只在自己平台的 exe 里用；别的平台或开发构建一律忽略。
func TestEmbeddedTargetMatches(t *testing.T) {
	mine := runtime.GOOS + "/" + runtime.GOARCH
	if !embeddedTargetMatches(mine) {
		t.Fatalf("%s 应该匹配", mine)
	}
	if !embeddedTargetMatches(mine + "\n") {
		t.Fatal("打包脚本写的标记带换行，应该先 TrimSpace 再比")
	}
	if embeddedTargetMatches("") {
		t.Fatal("空标记不能当成匹配")
	}
	other := "windows/amd64"
	if mine == other {
		other = "linux/amd64"
	}
	if embeddedTargetMatches(other) {
		t.Fatalf("\"%s\" 不是当前平台（%s），不应匹配", other, mine)
	}
}

// 解包要落在 OCR_HOME 里（跟后端同一个数据目录），同内容重复解包不重写。
func TestInstallEmbedded(t *testing.T) {
	root := t.TempDir()
	data := []byte("fake-server")

	first, ok := installEmbedded(root, data)
	if !ok {
		t.Fatal("解包失败")
	}
	if want := filepath.Join(root, "runtime"); filepath.Dir(first) != want {
		t.Fatalf("解包路径应在 %s，实际 %s", want, first)
	}
	if got, err := os.ReadFile(first); err != nil || string(got) != string(data) {
		t.Fatalf("解包内容不对：%q %v", got, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(first)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Fatalf("解包出来的后端要可执行，实际 %v", info.Mode())
		}
	}

	// 同一份内容：复用同一个文件，不产生第二个副本
	second, ok := installEmbedded(root, data)
	if !ok || second != first {
		t.Fatalf("同内容应复用 %s，实际 %s（ok=%v）", first, second, ok)
	}

	// 内容变了：换个文件名（Windows 上不能覆盖正在运行的 exe）
	third, ok := installEmbedded(root, []byte("newer-server"))
	if !ok {
		t.Fatal("新版本解包失败")
	}
	if third == first {
		t.Fatalf("内容变了应该换文件，实际还是 %s", first)
	}

	// 旧版本别堆在数据目录里（升级一次躺一个 exe）
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("旧版本 %s 应该被清掉（err=%v）", first, err)
	}
}
