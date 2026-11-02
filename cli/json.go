// json 命令族：消费 JSON 结果的零依赖方式（替代 python3 -c / jq——单二进制闭环）。
//
//	zpack json get <字段路径>      # stdin 读 JSON，字段值裸打印（stdout 纯文本）
//	zpack json ok                  # stdin 读信封 JSON，is_success → exit 0/1
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/internal/stdout"
)

// NewJSONCmd json 命令
func NewJSONCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "json <get|ok> [参数]",
		Short:              "JSON 字段提取与信封判定（stdin 读入——零依赖消费 zpack 结果）",
		DisableFlagParsing: true,
		Run: func(command *cobra.Command, args []string) {
			helpAwareRun(command, args, runJSON)
		},
	}
}

func runJSON(args []string) {
	subcommand := firstPositional(args)
	restArgs := restAfterPositional(args, subcommand)
	switch subcommand {
	case "get":
		jsonGet(restArgs)
	case "ok":
		jsonOK(restArgs)
	default:
		stdout.ErrorPrint("用法: json get <字段路径>（stdin 读 JSON，值裸打印）\n或: json ok（stdin 读信封 JSON，is_success → exit 0/1）\n字段路径：点分段 + 数组下标，如 data.entries.0.name")
	}
}

// readStdinJSON stdin 全量读入并解析
func readStdinJSON() (any, error) {
	data, readErr := os.ReadFile("/dev/stdin")
	if readErr != nil {
		// 兼容非 /dev/stdin 平台：缓冲读
		buffer := make([]byte, 0, 4096)
		chunk := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(chunk)
			buffer = append(buffer, chunk[:n]...)
			if err != nil {
				break
			}
		}
		data = buffer
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("stdin 无输入")
	}
	var parsed any
	if jsonErr := json.Unmarshal([]byte(trimmed), &parsed); jsonErr != nil {
		return nil, fmt.Errorf("非法 JSON: %w", jsonErr)
	}
	return parsed, nil
}

// jsonGet 字段提取：路径点分段 + 数字段=数组下标
func jsonGet(args []string) {
	if len(args) < 1 {
		stdout.ErrorPrint("用法: json get <字段路径>（如 data.archive / data.entries.0.name / is_success）")
	}
	parsed, parseErr := readStdinJSON()
	if parseErr != nil {
		stdout.ErrorPrint(parseErr.Error())
	}
	current := parsed
	for _, segment := range strings.Split(args[0], ".") {
		switch node := current.(type) {
		case map[string]any:
			value, found := node[segment]
			if !found {
				stdout.ErrorPrint("字段不存在: " + args[0] + "（段 \"" + segment + "\"）")
			}
			current = value
		case []any:
			index, indexErr := strconv.Atoi(segment)
			if indexErr != nil || index < 0 || index >= len(node) {
				stdout.ErrorPrint("数组下标非法: " + segment + "（路径 " + args[0] + "）")
			}
			current = node[index]
		default:
			stdout.ErrorPrint("路径越界: " + args[0] + "（段 \"" + segment + "\" 处非对象/数组）")
		}
	}
	fmt.Println(jsonValueText(current))
}

// jsonValueText 值的人读形态：字符串裸打/数字裸打/布尔裸打/null 空/复合=紧凑 JSON
func jsonValueText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	case nil:
		return ""
	default: // 对象/数组 → 紧凑 JSON
		encoded, _ := json.Marshal(typed)
		return string(encoded)
	}
}

// jsonOK 信封判定：is_success → exit 0/1（消费方一行 `&&`/`||` 即用）
func jsonOK(args []string) {
	parsed, parseErr := readStdinJSON()
	if parseErr != nil {
		stdout.ErrorPrint(parseErr.Error())
	}
	envelope, isMap := parsed.(map[string]any)
	if !isMap {
		stdout.ErrorPrint("非信封 JSON（顶层应为对象）")
	}
	success, isBool := envelope["is_success"].(bool)
	if !isBool {
		stdout.ErrorPrint("缺少 is_success 字段（非 zpack 信封 JSON）")
	}
	if success {
		os.Exit(0)
	}
	os.Exit(1)
}
