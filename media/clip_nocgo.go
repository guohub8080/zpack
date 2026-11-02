//go:build !cgo

package media

import "fmt"

var ErrClipNeedsFullBuild = fmt.Errorf("视频截取需要完整版（CGO 构建）")

func Clip(input, output string, startTime, endTime float64) (*ClipResult, error) {
	return nil, ErrClipNeedsFullBuild
}
