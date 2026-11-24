package cliutil

import (
	"os"
	"time"
)

// CANCEL_GRACE_PERIOD 第一次 Ctrl+C 之后留给引擎自己收尾的时间（2026-11-24 用户定案：长操作都要响应
// 第一次取消）。有可取消循环的引擎（压缩包一族）在这段时间里清掉半成品、输出取消 JSON 后退出；
// 没有可取消循环的（刻录、转码……）不会理会取消请求，过了宽限期就由装配层强制收尾
const CANCEL_GRACE_PERIOD = 2 * time.Second

// CancelSequence 取消信号的处理序列——阻塞到该退出为止；信号源与三个动作由调用方注入，纯逻辑可单测：
//   - 第一次信号：立刻 request（发出取消请求）；
//   - 宽限期内又来一次信号：immediate（立即退出，不清理）；
//   - 宽限期到了进程还在：force（强制收尾）——它应当让进程退出；万一没有，之后再来信号仍然 immediate。
func CancelSequence(grace time.Duration, signals <-chan os.Signal, request, force, immediate func()) {
	<-signals
	request()
	select {
	case <-signals:
		immediate()
		return
	case <-time.After(grace):
	}
	force()
	<-signals
	immediate()
}
