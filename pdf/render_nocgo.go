//go:build !cgo

// render 的纯构建 stub：MuPDF 引擎在纯 Go 构建下不可用（与 media 惯例同构——
// 纯构建是开发期快速循环工具，交付/装机恒为完整版，见 AGENTS.md 产物定案）。
package pdf

import "errors"

// ErrRenderNeedsFullBuild render 子命令的 stub 语义
var ErrRenderNeedsFullBuild = errors.New("pdf render 需完整版（CGO 构建 + MuPDF 引擎）")

// fitzRenderPage stub：纯构建恒报需完整版
func fitzRenderPage(input string, pageIndex int, dpi int, format string, outPath string) (int, int, error) {
	return 0, 0, ErrRenderNeedsFullBuild
}
