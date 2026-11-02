//go:build !cgo

package media

import "fmt"

var ErrSpeedNeedsFullBuild = fmt.Errorf("速度渲染需要完整版（CGO 构建）")

func Speed(input, output string, factor float64, pitchMode string) (*SpeedResult, error) {
	return nil, ErrSpeedNeedsFullBuild
}
