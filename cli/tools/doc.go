// Package tools 是零依赖消费层：base64（编码解码）、json（读 zpack 自己的结果——替代 python3 -c / jq）、
// skill（输出 / 安装 Agent 技能文档，SKILL.md 经 go:embed 内嵌）。让调用方只靠 zpack 一个二进制就能闭环。
//
// 行为与设计的权威说明见 specs/tools.md。
package tools
