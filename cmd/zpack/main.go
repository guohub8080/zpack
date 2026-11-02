// zpack —— 压缩/解压专项 CLI（Go 单二进制，自包含运行时）。
//
// 2026-11-02 从 exkit-cli 剥离孵化；同日目录重组：internal/core 核心算法引擎（纯库）+
// internal/cli 命令面（单引擎双面孔，GUI 壳届时新增 cmd/zpack-ui，见 AGENTS.md 北极星）。
// 7z/rar 解压与 GUI 规划见 AGENTS.md 路线图。
//
// 构建注入版本号：
//
//	go build -ldflags "-s -w -X main.version=$(git rev-parse --short HEAD)" -o bin/zpack ./cmd/zpack
package main

import "github.com/guohub8080/zpack/cli"

// version 构建时注入；go run / 未注入时显示 dev
var version = "dev"

func main() {
	cli.Execute(version)
}
