// Package cmdutil —— glob 模式匹配（--exclude 用）。
// 语义对齐 .gitignore 子集：`*` 通配单层、`**` 通配多层、`?` 单字符、`[abc]` 字符集；
// 尾部 `/` 限定目录名；不带 `/` 的模式匹配路径中任意一层的文件名或目录名。
// 分隔符纪律（2026-11-08 Windows 实测修复）：匹配统一在 `/` 坐标系——入参先 ToSlash 归一，
// 匹配用 path.Match（恒 `/` 语义）。此前用 filepath.Match，其在 Windows 上 `*` 只避开 `\`
// 不避开 `/`，`dist/*` 会误匹配 `dist/sub/output.js`（跨层语义漂移）。
package cmdutil

import (
	"path"
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
	relPath = filepath.ToSlash(relPath) // Windows 收集器给 `\` 风格——归一到 `/` 再匹配
	// 尾部 / → 仅匹配目录
	if strings.HasSuffix(pattern, "/") {
		if !isDir {
			return false
		}
		pattern = strings.TrimSuffix(pattern, "/")
	}
	// 基名匹配（模式不含 / → 匹配路径任意一层的名）
	if !strings.Contains(pattern, "/") {
		base := path.Base(relPath)
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
func globMatch(relPath, pattern string) bool {
	// 先尝试标准 path.Match（处理 *、?、[]——不含 **；恒 `/` 分隔语义，跨平台一致）
	if matched, err := path.Match(pattern, relPath); err == nil && matched {
		return true
	}
	// ** 支持：把 ** 段拆开递归匹配
	if strings.Contains(pattern, "**") {
		return matchDoubleStar(relPath, pattern)
	}
	// 模式含 / 但标准 Match 不支持多段——退化为逐段
	// （path.Match 的 * 不跨 /，路径按段匹配）
	return matchSegments(relPath, pattern)
}

func matchSegments(relPath, pattern string) bool {
	pathParts := strings.Split(relPath, "/")
	patParts := strings.Split(pattern, "/")
	return matchSegsRec(pathParts, patParts)
}

func matchSegsRec(pathSegs, patSegs []string) bool {
	if len(patSegs) == 0 {
		return len(pathSegs) == 0
	}
	if patSegs[0] == "**" {
		// ** 匹配零或多段
		for skip := 0; skip <= len(pathSegs); skip++ {
			if matchSegsRec(pathSegs[skip:], patSegs[1:]) {
				return true
			}
		}
		return false
	}
	if len(pathSegs) == 0 {
		return false
	}
	if ok, _ := path.Match(patSegs[0], pathSegs[0]); ok {
		return matchSegsRec(pathSegs[1:], patSegs[1:])
	}
	return false
}

func matchDoubleStar(relPath, pattern string) bool {
	return matchSegments(relPath, pattern)
}
