//go:build windows

package burn

import (
	"strings"
	"testing"
)

// TestBurnWindowsVolumeName Windows 卷标的温和规范化（表驱动）。引擎规则为 2026-11-24 探针实测：
// 中文名可用、控制字符拒收、路径非法字符会原样留在 UDF 卷标里、超长不报错（各文件系统自行截断）
func TestBurnWindowsVolumeName(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"中文名原样", "备份", "备份"},
		{"首尾空格去掉", "  我的照片  ", "我的照片"},
		{"大小写与连字符保留", "My_Photos-2024", "My_Photos-2024"},
		{"路径非法字符换成下划线", `a/b\c:d*e?f"g<h>i|j`, "a_b_c_d_e_f_g_h_i_j"},
		{"控制字符丢弃", "tab\tx\ny", "tabxy"},
		{"超长按字符截到 32", strings.Repeat("A", 40), strings.Repeat("A", 32)},
		{"中文按字符而非字节截断", strings.Repeat("备", 33), strings.Repeat("备", 32)},
		{"恰好 32 个字符不动", strings.Repeat("A", 32), strings.Repeat("A", 32)},
		{"空串回退 ZPACK", "", "ZPACK"},
		{"只有控制字符回退 ZPACK", "\t\n", "ZPACK"},
	}
	for _, testCase := range cases {
		if got := burnWindowsVolumeName(testCase.raw); got != testCase.want {
			t.Errorf("%s: %q ≠ %q", testCase.name, got, testCase.want)
		}
	}
}
