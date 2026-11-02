//go:build !cgo

package media

import "fmt"

var ErrInsertFrameNeedsFullBuild = fmt.Errorf("帧插入需要完整版（CGO 构建——remux + 编码引擎）")

func InsertFrame(input, coverImagePath, output, position string) (*InsertFrameResult, error) {
	return nil, ErrInsertFrameNeedsFullBuild
}
