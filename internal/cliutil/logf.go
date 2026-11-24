package cliutil

import (
	"fmt"

	"github.com/guohub8080/zpack/internal/cmdutil"
	"github.com/guohub8080/zpack/internal/stdout"
)

// StderrLogf 把引擎的过程日志接到 stderr 契约通道（stdout 只留给最终结果）。
// 可直接作为引擎的 Logf 回调传入
func StderrLogf(format string, args ...any) {
	stdout.LoggerInfo(fmt.Sprintf(format, args...))
}

// EngineLogf 按本次调用的参数决定过程日志的去向：带 --quiet 返回 nil
// （引擎侧所有进度 / 提示 / 跳过明细静默），否则接 stderr。
//
// 日志去向必须按「本次调用的参数」决定，不能放包级变量：命令面按功能拆包后各命令各管各的，
// 且进程内只会执行一条命令，全局状态本来就是多余的耦合
func EngineLogf(args []string) func(format string, args ...any) {
	if cmdutil.HasFlag(args, "--quiet") {
		return nil
	}
	return StderrLogf
}
