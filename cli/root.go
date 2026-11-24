// Package cli 装配 zpack 全部子命令。命令面按功能拆在子包里（每个子包对应 specs/ 下的一份 spec）：
// archive（压缩包）/ burn（刻录）/ crypto（加密与校验）/ image（图片）/ media（视频 + 音频 + ffmpeg 代理）/
// office（文档转换）/ pdf（PDF）/ tools（base64 / json / skill）；本包只做装配，不含命令实现。
package cli

import (
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/cli/archive"
	"github.com/guohub8080/zpack/cli/burn"
	"github.com/guohub8080/zpack/cli/crypto"
	"github.com/guohub8080/zpack/cli/image"
	"github.com/guohub8080/zpack/cli/media"
	"github.com/guohub8080/zpack/cli/office"
	"github.com/guohub8080/zpack/cli/pdf"
	"github.com/guohub8080/zpack/cli/tools"
	"github.com/guohub8080/zpack/core"
	"github.com/guohub8080/zpack/internal/cliutil"
	"github.com/guohub8080/zpack/internal/cmdutil"
	"github.com/guohub8080/zpack/internal/stdout"
)

// cliVersion 由构建注入的版本号（Execute 时保存，供命令读取）
var cliVersion = "dev"

// init 信号处理：第一次 Ctrl+C 发出取消请求，宽限期到了还没停就强制收尾，第二次 Ctrl+C 立即退出
// （2026-11-24 用户定案：长操作都要响应第一次取消——此前压缩包以外的命令按一次没反应，要按两次，
// 而且第二次直接 exit 130，不清理、stdout 没有结果）。序列的规则在 cliutil.CancelSequence，可单测
func init() {
	sigChan := make(chan os.Signal, 2)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go cliutil.CancelSequence(cliutil.CANCEL_GRACE_PERIOD, sigChan, requestCancel, forceCancel, immediateExit)
}

// requestCancel 第一次 Ctrl+C / SIGTERM：发出取消请求。有可取消循环的引擎（压缩包一族）检查到后
// 清掉半成品、输出「操作已被用户取消」再退出
func requestCancel() {
	core.RequestCancel()
	stdout.LoggerInfo("已请求取消：能中途停下的操作会清掉半成品后退出；再按一次 Ctrl+C 则立即退出，不清理")
}

// forceCancel 宽限期到了操作还在跑：引擎没有可取消的循环（刻录、转码……），没法让它自己收尾。
// 尽力清掉本次新建的输出文件，按「取消」给出结果。正在被引擎占用的文件删不掉，如实报出来
func forceCancel() {
	message := "操作已被用户取消（操作无法中途收尾，已强制结束）"
	if leftover := cliutil.RemoveCreatedOutputs(); len(leftover) > 0 {
		message += "；这些文件正被占用、没能清掉，请手动处理：" + strings.Join(leftover, "、")
	}
	stdout.ErrorPrint(message)
}

// immediateExit 第二次 Ctrl+C：立即退出，不等清理（130 是被信号终止的惯例退出码）
func immediateExit() {
	os.Exit(130)
}

// Execute 是 CLI 唯一入口；version 由构建时的 -ldflags -X 注入
func Execute(version string) {
	cliVersion = version
	rootCommand := &cobra.Command{
		Use:     cmdutil.AppName,
		Short:   "zpack——单文件命令行工具箱：压缩/解压、刻录、图片、视频、音频、PDF、文档转换、加密与校验（stdout 恒为一条 JSON）",
		Version: version,
		PersistentPreRun: func(command *cobra.Command, args []string) {
			// 统一开始日志：所有命令入口处恒一行（装配层约束，命令代码不各自发）。
			// 只记命令名、绝不记参数——参数可能含密码/密钥，落终端或日志即泄密
			stdout.LoggerInfo(cmdutil.AppName + " " + command.Name() + " 开始执行")
		},
	}
	rootCommand.AddCommand(
		// 压缩包
		archive.NewCompressCmd(),
		archive.NewExtractCmd(),
		archive.NewConvertCmd(),
		archive.NewListCmd(),
		archive.NewTestCmd(),
		archive.NewInfoCmd(),
		archive.NewBenchCmd(),
		// 刻录
		burn.NewBurnCmd(),
		// 加密与校验
		crypto.NewCryptoCmd(),
		crypto.NewHashCmd(),
		// 图片 / 文档转换 / PDF
		image.NewImageCmd(),
		office.NewOfficeCmd(),
		pdf.NewPdfCmd(),
		// 视频 / 音频 / ffmpeg 透明代理
		media.NewMediaCmd(),
		media.NewAudioCmd(),
		media.NewFFmpegCmd(),
		// 零依赖消费层
		tools.NewBase64Cmd(),
		tools.NewJSONCmd(),
		tools.NewSkillCmd(),
	)
	if executeErr := rootCommand.Execute(); executeErr != nil {
		os.Exit(1)
	}
}
