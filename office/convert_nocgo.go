//go:build !cgo

// office 转换——纯构建桩
package office

import "errors"

// ErrNeedsFullBuild 需完整版
var ErrNeedsFullBuild = errors.New("office 转换需完整版（CGO 构建 + office 产线）")

// Available 纯构建不可用
func Available() bool { return false }

// Convert 桩
func Convert(input, output string) (*ConvertResult, error) {
	return nil, ErrNeedsFullBuild
}

// ConvertResult 与 cgo 侧同形（纯层引用安全）
type ConvertResult struct {
	Input        string `json:"input"`
	Output       string `json:"output"`
	SourceFormat string `json:"source_format"`
	TargetFormat string `json:"target_format"`
	SizeBytes    int64  `json:"size_bytes"`
}
