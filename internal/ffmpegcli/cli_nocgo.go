//go:build !cgo

// zpack ffmpeg 进程内直调——纯构建桩
package ffmpegcli

import "errors"

// ErrNeedsFullBuild 纯构建无 ffmpeg CLI（进程内直调需 CGO + 产线前缀）
var ErrNeedsFullBuild = errors.New("zpack ffmpeg 需完整版（CGO 构建 + 静态产线）")

// Available 纯构建不可用
func Available() bool { return false }

// Run 纯构建桩：直接报错退出（负值返回与 ffmpeg 真实失败语义冲突——由桩层终结）
func Run(args []string) int32 {
	return 0 // 不可达：桩层已 exit
}
