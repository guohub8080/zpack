// --password-table：密码映射文件解析——compress 逐文件加密与 extract 逐文件解密共用。
// 每行「匹配模式=密码」（精确文件名或 glob，*=default 兜底，# 注释）。
package archive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// parsePasswordTable 解析密码映射文件：每行一条"匹配模式=密码"；
// 支持 # 注释、空行忽略；匹配模式为精确文件名或 glob（* 通配）。第二个返回值是 *= 兜底项的密码
// （没有 *= 项时为空串）：没给 --password 时，compress 用它当基准密码——加密是否生效取决于基准密码是否非空
func parsePasswordTable(tablePath string) (resolver func(string) (string, bool), fallbackPassword string, err error) {
	tableBytes, readErr := os.ReadFile(tablePath)
	if readErr != nil {
		return nil, "", fmt.Errorf("无法读取密码表: %s", readErr.Error())
	}
	type passwordRule struct {
		pattern  string
		password string
	}
	var rules []passwordRule
	for lineIdx, line := range strings.Split(string(tableBytes), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		equalsIdx := strings.Index(line, "=")
		if equalsIdx < 1 {
			return nil, "", fmt.Errorf("密码表第 %d 行格式错误（应为 匹配模式=密码）: %s", lineIdx+1, line)
		}
		rules = append(rules, passwordRule{
			pattern:  strings.TrimSpace(line[:equalsIdx]),
			password: strings.TrimSpace(line[equalsIdx+1:]),
		})
	}
	if len(rules) == 0 {
		return nil, "", fmt.Errorf("密码表为空: %s", tablePath)
	}
	// 兜底密码 = 顺序上的第一个 * 规则（与下面解析器的匹配顺序一致）
	for _, rule := range rules {
		if rule.pattern == "*" {
			fallbackPassword = rule.password
			break
		}
	}
	resolver = func(fileName string) (string, bool) {
		baseName := filepath.Base(fileName)
		for _, rule := range rules {
			if rule.pattern == baseName {
				return rule.password, true
			}
			if matched, matchErr := filepath.Match(rule.pattern, baseName); matchErr == nil && matched {
				return rule.password, true
			}
		}
		return "", false
	}
	return resolver, fallbackPassword, nil
}
