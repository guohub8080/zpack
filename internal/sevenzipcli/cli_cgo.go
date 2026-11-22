//go:build cgo && darwin

// zpack 7z 写入进程内直调：官方 7-Zip C++ 源码（Alone2 bundle，DISABLE_RAR=1 剥 RAR）
// 的 main 改名（extern "C" zpack_7z_main）后链入——ffmpeg 直调同款模式。
// 语义注记：7z 的 main 正常返回退出码（不 exit），stdio 由本层接管（-bso0 下 stdout 恒空）。
// 链接注记：库是全部对象 ld -r 合并后的单一成员归档——7z 的格式/编码器注册靠静态
// ctor，普通按需拉取的 .a 永远拉不到这些注册对象（注册表全空、命令全 E_NOTIMPL），
// 单成员归档使 zpack_7z_main 的拉取连带全部对象，等价 whole-archive。
package sevenzipcli

/*
#include <stdlib.h>
#include <stdio.h>
int zpack_7z_main(int argc, char **argv);
#cgo LDFLAGS: -L${SRCDIR}/libs -lzpack_7zcli -lc++
*/
import "C"

import (
	"bufio"
	"errors"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ErrNeedsFullBuild 不可达防御（cgo 构建恒有 CLI）
var ErrNeedsFullBuild = errors.New("zpack 7z 写入需完整版（CGO 构建 + 7z 静态产线）")

// Available 完整版可用（cgo 构建恒 true）
func Available() bool { return true }

// Run 进程内执行 7z CLI main，返回退出码
// （0=成功 1=警告 2=致命错误 7=命令行错误 8=内存不足 255=用户中断）
func Run(args []string) int32 {
	// 管道/重定向下 C stdio 不 flush 的防御（stderr 默认不缓冲，stdout 钉行缓冲双保险）
	C.setvbuf(C.stdout, nil, C._IOLBF, 0)
	// C argv 惯例：argv[0]=程序名，实参从 argv[1] 起，末位 NULL
	argv := make([]*C.char, len(args)+2)
	argv[0] = C.CString("7z")
	defer C.free(unsafe.Pointer(argv[0]))
	for idx, arg := range args {
		argv[idx+1] = C.CString(arg)
		defer C.free(unsafe.Pointer(argv[idx+1]))
	}
	return int32(C.zpack_7z_main(C.int(len(args)+1), &argv[0]))
}

// RunCaptured 执行期间临时接管 fd2：7z 的输出全部经管道捕获（逐行记录 + 实时回放到
// 真 stderr，终端与 GUI 子进程 stderr 均不失明），返回退出码与捕获的全部行。
// 同窗口内本进程其他 stderr 写入（Go 进度条等）也走同一管道被忠实转发。
func RunCaptured(args []string) (int32, []string) {
	pipeReader, pipeWriter, pipeErr := os.Pipe()
	if pipeErr != nil {
		return Run(args), nil
	}
	savedStderr, dupErr := unix.Dup(unix.Stderr)
	if dupErr != nil {
		pipeReader.Close()
		pipeWriter.Close()
		return Run(args), nil
	}
	var captureMutex sync.Mutex
	var capturedLines []string
	forwardDone := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(pipeReader)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			captureMutex.Lock()
			capturedLines = append(capturedLines, line)
			captureMutex.Unlock()
			// 直写接管前的 fd（不经 os.Stderr——它此刻指向管道，会成环）
			_, _ = unix.Write(savedStderr, []byte(line+"\n"))
		}
		close(forwardDone)
	}()
	unix.Dup2(int(pipeWriter.Fd()), unix.Stderr)
	exitCode := Run(args)
	// 先恢复 fd2 再关写端：读协程随 EOF 收尾
	unix.Dup2(savedStderr, unix.Stderr)
	unix.Close(savedStderr)
	pipeWriter.Close()
	<-forwardDone
	pipeReader.Close()
	captureMutex.Lock()
	defer captureMutex.Unlock()
	return exitCode, capturedLines
}
