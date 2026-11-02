//go:build !cgo

package media

import "fmt"

var ErrMergeNeedsFullBuild = fmt.Errorf("视频拼接需要完整版（CGO 构建）")

func Merge(inputs []string, output, referencePath string) (*MergeResult, error) {
	return nil, ErrMergeNeedsFullBuild
}
