//go:build !cgo

package media

import "fmt"

var ErrCoverNeedsFullBuild = fmt.Errorf("封面添加需要完整版（CGO 构建——remux 引擎）")

func AddCover(input, coverImagePath, output string) (*CoverResult, error) {
	return nil, ErrCoverNeedsFullBuild
}
