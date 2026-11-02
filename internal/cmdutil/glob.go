// Package cmdutil —— glob 模式匹配（--exclude 用）。
// 语义对齐 .gitignore 子集：`*` 通配单层、`**` 通配多层、`?` 单字符、`[abc]` 字符集；
// 尾部 `/` 限定目录名；不带 `/` 的模式匹配路径中任意一层的文件名或目录名。
package cmdutil

import (
	"path/filepath"
	"strings"
)

// MatchGlob 判断路径是否命中排除模式（相对路径或基名匹配）
// 模式示例：`*.bak`（任意层 .bak）、`node_modules/`（目录）、`**/test/**`（深层 test）
func MatchGlob(relPath string, isDir bool, pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	// 尾部 / → 仅匹配目录
	if strings.HasSuffix(pattern, "/") {
		if !isDir {
			return false
		}
		pattern = strings.TrimSuffix(pattern, "/")
	}
	// 基名匹配（模式不含 / → 匹配路径任意一层的名）
	if !strings.Contains(pattern, "/") {
		base := filepath.Base(relPath)
		return globMatch(base, pattern) || globMatch(relPath, pattern)
	}
	// 含 / → 全路径匹配
	return globMatch(relPath, pattern)
}

// MatchAnyGlob 任一模式命中即 true
func MatchAnyGlob(relPath string, isDir bool, patterns []string) bool {
	for _, pattern := range patterns {
		if MatchGlob(relPath, isDir, pattern) {
			return true
		}
	}
	return false
}

// globMatch 简化版路径 glob：`*` 任意非分隔符序列、`**` 任意含分隔符、`?` 单字符、`[...]` 字符集
func globMatch(path, pattern string) bool {
	// 先尝试标准 filepath.Match（处理 *、?、[]——不含 **）
	if matched, err := filepath.Match(pattern, path); err == nil && matched {
		return true
	}
	// ** 支持：把 ** 段拆开递归匹配
	if strings.Contains(pattern, "**") {
		return matchDoubleStar(path, pattern)
	}
	// 模式含 / 但标准 Match 不支持多段——退化为逐段
	// （filepath.Match 的 * 不跨 /，路径按段匹配）
	return matchSegments(path, pattern)
}

func matchSegments(path, pattern string) bool {
	pathParts := strings.Split(path, "/")
	patParts := strings.Split(pattern, "/")
	return matchSegsRec(pathParts, patParts)
}

func matchSegsRec(path, pat []string) bool {
	if len(pat) == 0 {
		return len(path) == 0
	}
	if pat[0] == "**" {
		// ** 匹配零或多段
		for skip := 0; skip <= len(path); skip++ {
			if matchSegsRec(path[skip:], pat[1:]) {
				return true
			}
		}
		return false
	}
	if len(path) == 0 {
		return false
	}
	if ok, _ := filepath.Match(pat[0], path[0]); ok {
		return matchSegsRec(path[1:], pat[1:])
	}
	return false
}

func matchDoubleStar(path, pattern string) bool {
	return matchSegments(path, pattern)
}
