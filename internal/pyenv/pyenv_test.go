package pyenv

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeVenv 造一个 venv 目录，只放指定文件（不真跑 python）。
func fakeVenv(t *testing.T, withConfig bool) *Env {
	t.Helper()
	dir := t.TempDir()
	venv := filepath.Join(dir, "venv")
	env := New(dir, venv)
	if err := os.MkdirAll(filepath.Dir(env.VenvPython()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.VenvPython(), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	if withConfig {
		if err := os.WriteFile(env.VenvConfig(), []byte("home = /usr/bin\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return env
}

// TestVenvReadyNeedsConfig 只有解释器、没有 pyvenv.cfg 的半截 venv 不算就绪。
func TestVenvReadyNeedsConfig(t *testing.T) {
	if !fakeVenv(t, true).VenvReady() {
		t.Error("有解释器又有 pyvenv.cfg，应该算就绪")
	}
	if fakeVenv(t, false).VenvReady() {
		t.Error("缺 pyvenv.cfg 的半截 venv 不能算就绪（pip 会报 No pyvenv.cfg file）")
	}
}

// TestStatusExplainsBrokenVenv 环境坏了要直说怎么修，不能只写「还没建好」。
func TestStatusExplainsBrokenVenv(t *testing.T) {
	status := fakeVenv(t, false).Status()
	if status.State != "error" {
		t.Errorf("半截 venv 应该报 error，得到 %q", status.State)
	}
	if !strings.Contains(status.Message, "重建 venv") {
		t.Errorf("提示里要告诉用户点「重建 venv」，得到 %q", status.Message)
	}
	if status.VenvUsed {
		t.Error("坏掉的 venv 不该标成已用")
	}
}

// TestStatusMissingVenv 完全没建 venv 时仍然是 missing（提示去建，而不是去修）。
func TestStatusMissingVenv(t *testing.T) {
	dir := t.TempDir()
	status := New(dir, filepath.Join(dir, "venv")).Status()
	if status.State != "missing" {
		t.Errorf("没有 venv 应该报 missing，得到 %q", status.State)
	}
}

// TestVenvPythonPath 解释器路径要按平台拼对（Windows 是 Scripts\python.exe）。
func TestVenvPythonPath(t *testing.T) {
	env := New("py", filepath.Join("root", "venv"))
	want := filepath.Join("root", "venv", "bin", "python")
	if runtime.GOOS == "windows" {
		want = filepath.Join("root", "venv", "Scripts", "python.exe")
	}
	if got := env.VenvPython(); got != want {
		t.Errorf("解释器路径不对：得到 %s，想要 %s", got, want)
	}
}
