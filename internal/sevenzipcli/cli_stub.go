//go:build !cgo || !darwin

// zpack 7z 写入进程内直调——纯构建/非 darwin 桩（Windows 产线 M2 接入时替换为真实现）
package sevenzipcli

import "errors"

// ErrNeedsFullBuild 纯构建无 7z CLI（进程内直调需 CGO + 产线库）
var ErrNeedsFullBuild = errors.New("zpack 7z 写入需完整版（CGO 构建 + 7z 静态产线）")

// Available 桩不可用
func Available() bool { return false }

// Run 桩：返回致命错误码，调用方以 ErrNeedsFullBuild 先行拦截
func Run(args []string) int32 { return 2 }

// RunCaptured 桩：同 Run 语义（不可达路径）
func RunCaptured(args []string) (int32, []string) { return 2, nil }
