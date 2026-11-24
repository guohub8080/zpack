package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// platformName 同一规则在 Windows 与其他平台上的期望值
func platformName(windows, other string) string {
	if runtime.GOOS == "windows" {
		return windows
	}
	return other
}

// TestBurnResolveVolumeName 卷标的来源规则（表驱动）：未给 --volid 时单个目录输入用目录名
// （2026-11-24 用户定案），其余形态沿用旧规则取当前目录名；显式给的在 Windows 上做温和规范化，
// mac/Linux 原样交给外部工具（旧行为）
func TestBurnResolveVolumeName(t *testing.T) {
	photos := filepath.Join(t.TempDir(), "Photos")
	if err := os.MkdirAll(photos, 0o755); err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(loose, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwdName := burnPlatformVolumeName(filepath.Base(mustAbs(".")))
	cases := []struct {
		name   string
		inputs []string
		flags  burnFlags
		want   string
	}{
		{"单个目录 → 目录名", []string{photos}, burnFlags{}, platformName("Photos", "PHOTOS")},
		{"单个目录带尾分隔符同样取目录名", []string{photos + string(filepath.Separator)}, burnFlags{}, platformName("Photos", "PHOTOS")},
		{"单个文件不是目录 → 当前目录名", []string{loose}, burnFlags{}, cwdName},
		{"多输入里含目录 → 当前目录名", []string{loose, photos}, burnFlags{}, cwdName},
		{"显式 --volid", []string{photos}, burnFlags{volid: "我的:盘", volidGiven: true}, platformName("我的_盘", "我的:盘")},
	}
	for _, testCase := range cases {
		if got := burnResolveVolumeName(testCase.inputs, testCase.flags); got != testCase.want {
			t.Errorf("%s: %q ≠ %q", testCase.name, got, testCase.want)
		}
	}
}
