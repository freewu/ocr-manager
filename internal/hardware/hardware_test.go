package hardware

import (
	"runtime"
	"testing"
)

// TestMemoryProbe 在支持的平台上要读得到总内存；读不到也得给出错误原因，
// 不能静默返回 0（否则「内存不够」的判断会失去依据）。
func TestMemoryProbe(t *testing.T) {
	info := RefreshMemory()
	if info.CheckedAt == 0 {
		t.Error("检测结果应该带时间戳")
	}
	switch runtime.GOOS {
	case "windows", "linux", "darwin":
		if info.TotalMB <= 0 && info.Error == "" {
			t.Errorf("%s 上没读到内存，也没给错误原因：%+v", runtime.GOOS, info)
		}
	default:
		if info.TotalMB != 0 {
			t.Errorf("没有实现的平台应该返回 0，拿到 %+v", info)
		}
	}
}
