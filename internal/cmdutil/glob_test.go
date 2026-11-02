package cmdutil

import "testing"

// TestMatchGlob glob 排除模式（--exclude 语义）
func TestMatchGlob(t *testing.T) {
	cases := []struct {
		path    string
		isDir   bool
		pattern string
		expect  bool
	}{
		// 简单后缀
		{"backup.bak", false, "*.bak", true},
		{"src/backup.bak", false, "*.bak", true},   // 任意层匹配基名
		{"src/deep/old.bak", false, "*.bak", true}, // 深层也中
		{"src/main.go", false, "*.bak", false},     // 不中
		// 目录限定（尾部 /）
		{"node_modules", true, "node_modules/", true},
		{"node_modules/pkg.json", false, "node_modules/", false}, // 文件不中
		{"src/node_modules", true, "node_modules/", true},        // 任意层目录名
		// 路径模式（含 /）
		{"dist/output.js", false, "dist/*", true},
		{"dist/sub/output.js", false, "dist/*", false}, // * 不跨 /
		{"dist/sub/output.js", false, "dist/**", true}, // ** 跨 /
		{"a/b/c/d.txt", false, "**/d.txt", true},       // ** 前缀
		{"x/test/y/z.txt", false, "**/test/**", true},  // ** 中间
		// 单字符
		{"file1.txt", false, "file?.txt", true},
		{"file12.txt", false, "file?.txt", false},
		// 字符集
		{"log_a.txt", false, "log_[abc].txt", true},
		{"log_x.txt", false, "log_[abc].txt", false},
	}
	for _, tc := range cases {
		if got := MatchGlob(tc.path, tc.isDir, tc.pattern); got != tc.expect {
			t.Errorf("MatchGlob(%q, %v, %q) = %v, expect %v", tc.path, tc.isDir, tc.pattern, got, tc.expect)
		}
	}
}

func TestMatchGlobDirPattern(t *testing.T) {
	// node_modules/ 尾部斜杠 → 仅目录；不带斜杠匹配任意类型
	if !MatchGlob("node_modules", true, "node_modules/") {
		t.Error("node_modules/ 应匹配目录 node_modules")
	}
	if MatchGlob("node_modules", false, "node_modules/") {
		t.Error("node_modules/ 不应匹配文件")
	}
	if !MatchGlob("src/node_modules", true, "node_modules/") {
		t.Error("node_modules/ 应匹配子层目录")
	}
	// 交叉验证：isExcludedPath 路径——可能问题在 core 侧 relInRoot 计算
}
