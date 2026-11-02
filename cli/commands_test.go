package cli

import "testing"

// TestParseVolumeSize 分卷尺寸解析：模板精确字节（SI 标称）+ 数字后缀（二进制 1024 基）+ 退化
func TestParseVolumeSize(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		expected   int64
		shouldFail bool
	}{
		// 模板：介质标称容量（SI 十进制）
		{"模板 cd", "cd", 700_000_000, false},
		{"模板 CD 大小写归一", "CD", 700_000_000, false},
		{"模板 vcd 与 cd 同规格", "vcd", 700_000_000, false},
		{"模板 cd650", "cd650", 650_000_000, false},
		{"模板 gmail", "gmail", 25_000_000, false},
		{"模板 fat32 = 4GiB-1", "fat32", 4_294_967_295, false},
		{"模板 dvd", "dvd", 4_700_000_000, false},
		{"模板 dvd5 别名", "dvd5", 4_700_000_000, false},
		{"模板 dvd9", "dvd9", 8_500_000_000, false},
		{"模板 bd", "bd", 25_000_000_000, false},
		{"模板 bd25 别名", "bd25", 25_000_000_000, false},
		{"模板 bd50", "bd50", 50_000_000_000, false},
		// 数字后缀：二进制 1024 基（沿用既有约定）
		{"数字裸字节", "65536", 65536, false},
		{"数字 64KB", "64KB", 65536, false},
		{"数字 100MB", "100MB", 100 * 1024 * 1024, false},
		{"数字 1GB", "1GB", 1 << 30, false},
		{"数字 700B", "700B", 700, false},
		{"数字带空格", " 100MB ", 100 * 1024 * 1024, false},
		// 退化
		{"未知模板 bluray", "bluray", 0, true},
		{"无此模板 cd700", "cd700", 0, true},
		{"负数", "-5MB", 0, true},
		{"零", "0", 0, true},
		{"空串", "", 0, true},
		{"非数字", "abcMB", 0, true},
	}
	for _, testCase := range cases {
		got, err := parseVolumeSize(testCase.input)
		if testCase.shouldFail {
			if err == nil {
				t.Errorf("%s: 期望失败，实际得到 %d", testCase.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: 意外失败: %v", testCase.name, err)
			continue
		}
		if got != testCase.expected {
			t.Errorf("%s: 期望 %d 字节，实际 %d", testCase.name, testCase.expected, got)
		}
	}
}
