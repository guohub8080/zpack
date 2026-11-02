//go:build !cgo

// 非 cgo 构建桩：纯 Go 产线（CGO_ENABLED=0）无媒体引擎——决策/计划层照常可测，
// 引擎调用返回明确指引（用 build_cli 产线的完整版）。
package media

import "errors"

// ErrRequiresFullBuild 完整版提示（CLI 转成 error_info 输出）
var ErrRequiresFullBuild = errors.New("媒体功能需要完整版（build_cli 产线 CGO 构建）；纯 Go 版不含 libav 引擎")

func probeFile(input string) (ProbeResult, error) {
	return ProbeResult{}, ErrRequiresFullBuild
}

func executeFile(input, output string, mode Mode, probe ProbeResult, params execParams, logf Logf) (ExecResult, error) {
	return ExecResult{}, ErrRequiresFullBuild
}
