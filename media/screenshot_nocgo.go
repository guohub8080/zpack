//go:build !cgo

// 截图纯构建桩——视频解码需要 astiav/CGO。
package media

import "fmt"

// ErrScreenshotNeedsFullBuild 纯构建不支持截图
var ErrScreenshotNeedsFullBuild = fmt.Errorf("视频截图需要完整版（build_cli 产线 CGO 构建）；纯 Go 构建不含 astiav/libav 引擎")

func Screenshot(input string, timestamp float64, output string) (*ScreenshotResult, error) {
	return nil, ErrScreenshotNeedsFullBuild
}

func ScreenshotBatch(input string, timestamps []float64, outputDir string, format string) (*ScreenshotBatchResult, error) {
	return nil, ErrScreenshotNeedsFullBuild
}
