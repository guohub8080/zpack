// 优雅取消：SIGINT/SIGTERM → 原子标记 → 工作协程/主循环检测 → 返回 CancelledError
// → 既有清理路径（abortArchive/tempFile/os.Remove）自然收尾。
// 第二次 Ctrl+C 强制退出（有些用户不想等清理）。
package core

import (
	"errors"
	"sync/atomic"
)

// ErrCancelled 用户取消错误（CLI 输出「操作已被用户取消」+ exit 1）
var ErrCancelled = errors.New("操作已被用户取消")

// cancellationFlag 优雅取消标记
var cancellationFlag atomic.Bool

// RequestCancel 请求取消（信号处理器调用）——工作协程在下一个检查点退出
func RequestCancel() {
	cancellationFlag.Store(true)
}

// IsCancelled 检查是否已请求取消
func IsCancelled() bool {
	return cancellationFlag.Load()
}

// ResetCancel 重置取消标记（测试用）
func ResetCancel() {
	cancellationFlag.Store(false)
}

// checkCancelled 检查点：已取消则返回 ErrCancelled，否则返回 nil
func checkCancelled() error {
	if cancellationFlag.Load() {
		return ErrCancelled
	}
	return nil
}
