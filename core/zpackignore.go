// .zpackignore 文件支持：gitignore 语义的归档排除规则。
// 查找顺序：每个输入目录根部的 .zpackignore（模式相对该目录）+ 当前工作目录的 .zpackignore（全局补充）。
// .zpackignore 文件本身始终排除。语法：glob 模式 / `#` 注释 / 空行 / `!` 反选 / `/` 前缀根限定 / `/` 后缀目录限定。
package core

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// ZPACKIGNORE_FILENAME 归档排除规则文件名
const ZPACKIGNORE_FILENAME = ".zpackignore"

// ZpackIgnorePattern 单条排除规则
type ZpackIgnorePattern struct {
	Pattern  string // glob 模式（去除 ! / 前后缀后的纯净形式）
	Negate   bool   // `!` 前缀：反选（不被排除）
	DirOnly  bool   // `/` 后缀：仅匹配目录
	RootOnly bool   // `/` 前缀：仅匹配根级别
}

// ZpackIgnore 一个 .zpackignore 文件的解析结果
type ZpackIgnore struct {
	Patterns []ZpackIgnorePattern
	RootDir  string // 此 .zpackignore 所在目录（模式相对此目录解析）
}

// ParseZpackignoreContent 解析 .zpackignore 内容
func ParseZpackignoreContent(content string) []ZpackIgnorePattern {
	var patterns []ZpackIgnorePattern
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pattern := ZpackIgnorePattern{Pattern: line}
		if strings.HasPrefix(pattern.Pattern, "!") {
			pattern.Negate = true
			pattern.Pattern = strings.TrimPrefix(pattern.Pattern, "!")
		}
		if strings.HasSuffix(pattern.Pattern, "/") {
			pattern.DirOnly = true
			pattern.Pattern = strings.TrimSuffix(pattern.Pattern, "/")
		}
		if strings.HasPrefix(pattern.Pattern, "/") {
			pattern.RootOnly = true
			pattern.Pattern = strings.TrimPrefix(pattern.Pattern, "/")
		}
		if pattern.Pattern != "" {
			patterns = append(patterns, pattern)
		}
	}
	return patterns
}

// LoadZpackignore 从目录读取 .zpackignore（不存在返回 nil，不报错）
func LoadZpackignore(dir string) *ZpackIgnore {
	content, readErr := os.ReadFile(filepath.Join(dir, ZPACKIGNORE_FILENAME))
	if readErr != nil {
		return nil // 不存在或读不了 → 无规则
	}
	patterns := ParseZpackignoreContent(string(content))
	if len(patterns) == 0 {
		return nil
	}
	return &ZpackIgnore{Patterns: patterns, RootDir: dir}
}

// Matches 检查相对路径是否被排除。isDir 标识当前条目是否目录。
// gitignore 语义：从上到下最后一条命中的规则生效（反选可覆盖前面的排除）。
func (ignore *ZpackIgnore) Matches(relativePath string, isDir bool) bool {
	slashPath := filepath.ToSlash(relativePath)
	matched := false
	for _, pattern := range ignore.Patterns {
		if pattern.DirOnly && !isDir {
			continue
		}
		if zpackIgnorePatternMatch(slashPath, pattern) {
			matched = !pattern.Negate // 最后命中生效
		}
	}
	return matched
}

// zpackIgnorePatternMatch 单条模式匹配：支持任意层级后缀匹配（非根限定模式匹配路径的任意尾部）
func zpackIgnorePatternMatch(slashPath string, pattern ZpackIgnorePattern) bool {
	if pattern.RootOnly {
		matched, _ := filepath.Match(pattern.Pattern, slashPath)
		return matched
	}
	// 非根限定：匹配路径本身或任何父目录开始的尾部
	segments := strings.Split(slashPath, "/")
	for startIdx := 0; startIdx < len(segments); startIdx++ {
		subPath := strings.Join(segments[startIdx:], "/")
		if matched, _ := filepath.Match(pattern.Pattern, subPath); matched {
			return true
		}
	}
	return false
}

// LoadEffectiveZpackignores 收集输入路径集合的生效 .zpackignore：
// 每个输入目录一个（相对该目录）+ CWD 一个（全局，相对各输入目录的公共父级）
func LoadEffectiveZpackignores(inputPaths []string) []*ZpackIgnore {
	var ignores []*ZpackIgnore
	seenDirs := map[string]bool{}
	for _, inputPath := range inputPaths {
		absPath, _ := filepath.Abs(inputPath)
		info, statErr := os.Stat(absPath)
		if statErr != nil {
			continue
		}
		var dir string
		if info.IsDir() {
			dir = absPath
		} else {
			dir = filepath.Dir(absPath)
		}
		if !seenDirs[dir] {
			seenDirs[dir] = true
			if ig := LoadZpackignore(dir); ig != nil {
				ignores = append(ignores, ig)
			}
		}
	}
	// CWD 全局 .zpackignore（如果不在已收集的目录里）
	cwd, _ := os.Getwd()
	if !seenDirs[cwd] {
		if ig := LoadZpackignore(cwd); ig != nil {
			ignores = append(ignores, ig)
		}
	}
	return ignores
}

// ZpackIgnoreExcludes 判断条目是否被任一 .zpackignore 排除。
// relativeToRoot 是条目相对输入目录根的路径（正斜杠分隔）
func ZpackIgnoreExcludes(ignores []*ZpackIgnore, relativeToRoot string, isDir bool) bool {
	for _, ignore := range ignores {
		if ignore.Matches(relativeToRoot, isDir) {
			return true
		}
	}
	return false
}
