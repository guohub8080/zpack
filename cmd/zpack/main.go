// zpack —— 单文件命令行工具箱：压缩/解压、刻录、图片、视频、音频、PDF、文档转换、加密与校验
// （Go 单二进制，自包含运行时）。
//
// 2026-11-02 从 exkit-cli 剥离孵化；单引擎双面孔——同一个二进制既服务 CLI 也服务 GUI
// （GUI 是经 stdout JSON 契约调用的薄壳）。布局：各功能的引擎在顶层包（core / image / media /
// office / pdf），命令面在 cli/ 下按功能分子包，各功能的规格在 specs/（入口 specs/README.md）。
//
// 构建注入版本号（开发期纯 Go 构建；交付与装机产物恒为完整版，走 build_cli/ 产线）：
//
//	CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$(git rev-parse --short HEAD)-dev" -o bin/zpack-dev ./cmd/zpack
package main

import "github.com/guohub8080/zpack/cli"

// version 构建时注入；go run / 未注入时显示 dev
var version = "dev"

func main() {
	cli.Execute(version)
}
