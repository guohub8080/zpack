//go:build cgo

// zpack ffmpeg 进程内直调：ffmpeg CLI 自己的 main（-Dmain 改名后链入）。
// 无临时释放、无缓存目录、无子进程——命令即进程内的函数调用。
// 语义注记：ffmpeg 的 main 结尾走 exit_program→exit()，调用即进程终结，
// 退出码/stdio 全为 ffmpeg 原生语义（对 CLI 形态正合适）。
package ffmpegcli

/*
#include <stdlib.h>
#include <stdio.h>
int zpack_ffmpeg_cli_main(int argc, char **argv);
*/
import "C"

import (
	"errors"
	"unsafe"
)

// ErrNeedsFullBuild 不可达防御（cgo 构建恒有 CLI）
var ErrNeedsFullBuild = errors.New("zpack ffmpeg 需完整版（CGO 构建 + 静态产线）")

// Available 完整版可用（cgo 构建恒 true）
func Available() bool { return true }

// Run 进程内执行 ffmpeg CLI（正常情况下不返回——exit() 终结进程；返回值仅防御路径）
func Run(args []string) int32 {
	// cgo 环境下 C exit() 不 flush stdio（管道/重定向丢输出）——stdout 钉成行缓冲
	C.setvbuf(C.stdout, nil, C._IOLBF, 0)
	// C argv 惯例：argv[0]=程序名（ffmpeg 报错/usage 引用它），实参从 argv[1] 起，末位 NULL
	argv := make([]*C.char, len(args)+2)
	argv[0] = C.CString("ffmpeg")
	defer C.free(unsafe.Pointer(argv[0]))
	for idx, arg := range args {
		argv[idx+1] = C.CString(arg)
		defer C.free(unsafe.Pointer(argv[idx+1]))
	}
	return int32(C.zpack_ffmpeg_cli_main(C.int(len(args)+1), &argv[0]))
}
