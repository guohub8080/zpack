// Package cli 里所有命令共用的小型参数解析器。
//
// 为什么不用 cobra 的 flag 解析而手写：需要完全控制语义——保留旧版的
// 宽容行为（未知 flag 静默忽略），同时对数值参数做严格校验（非法值
// 报错而非旧版的静默回退/钳位，属与旧版的故意分歧）。
// DisableFlagParsing + 手写解析才能精确实现这套混合语义。
package cmdutil

import (
	"strconv"
	"strings"
)

// HasFlag 判断 flag 是否出现
func HasFlag(args []string, flagName string) bool {
	for _, arg := range args {
		if arg == flagName {
			return true
		}
	}
	return false
}

// FlagValue 取 flag 的下一个参数值；flag 缺值或不存在时第二个返回值为 false
func FlagValue(args []string, flagName string) (string, bool) {
	for argIndex, arg := range args {
		if arg == flagName && argIndex+1 < len(args) {
			return args[argIndex+1], true
		}
	}
	return "", false
}

// ParseIntStrict 严格解析整数：整个字符串必须是合法十进制整数（可带正负号）。
// 与 JS parseInt 的前缀语义不同——"5abc"/"" 这类输入一律返回 false。
func ParseIntStrict(text string) (int, bool) {
	parsed, parseErr := strconv.Atoi(text)
	if parseErr != nil {
		return 0, false
	}
	return parsed, true
}

// FirstPositional 取首个非旗标位置参数（子命令名常用）；没有返回空串
func FirstPositional(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

// StringValue any→string 的容错转换（JSON 数值/字符串场景）
func StringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if number, ok := value.(float64); ok {
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	return ""
}

// EnsureSubMap 确保并返回子 map（配置写入用）
func EnsureSubMap(parent map[string]any, key string) map[string]any {
	if child, ok := parent[key].(map[string]any); ok {
		return child
	}
	child := map[string]any{}
	parent[key] = child
	return child
}

// ToSlice any→[]any 容错
func ToSlice(value any) []any {
	if slice, ok := value.([]any); ok {
		return slice
	}
	return []any{}
}
