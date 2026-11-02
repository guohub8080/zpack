// ffmpeg 透明代理（进程内直调形态）：zpack ffmpeg <原版任意参数> ——
// ffmpeg CLI 的 main 以 -Dmain 改名后静态链入，直接函数调用，零释放零子进程。
// 契约例外（唯一）：stdout/stderr/exit code 均为 ffmpeg 原生语义
// （stdout 可为二进制媒体流，-f mp4 pipe:1 等场景必需），不走 JSON 信封。
package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/internal/ffmpegcli"
	"github.com/guohub8080/zpack/internal/stdout"
)

// NewFFmpegCmd ffmpeg 透传命令
func NewFFmpegCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "ffmpeg [原版任意参数...]",
		Short:              "内置 ffmpeg CLI 全量直调（原版命令面；stdout 为 ffmpeg 原生输出）",
		DisableFlagParsing: true,
		Run: func(command *cobra.Command, args []string) {
			helpAwareRun(command, args, runFFmpegInProcess)
		},
	}
}

// runFFmpegInProcess 进程内直调（正常不返回——ffmpeg main 以 exit() 终结进程）
func runFFmpegInProcess(args []string) {
	if len(args) == 0 {
		stdout.ErrorPrint("用法: zpack ffmpeg <原版任意参数...>（如 zpack ffmpeg -i in.mp4 out.mp3）\n原版 CLI 全量直调：stdout/stderr/退出码均为 ffmpeg 原生语义")
	}
	if !ffmpegcli.Available() {
		stdout.ErrorPrint(ffmpegcli.ErrNeedsFullBuild.Error())
	}
	ret := ffmpegcli.Run(args)
	os.Exit(int(ret)) // 正常流程已在 C 层 exit；此为 main return 路径（负值=ffmpeg 真实失败码照传）
}
