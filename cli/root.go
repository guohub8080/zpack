// Package cli 装配 zpack 全部子命令。命令面按功能拆在子包里（每个子包对应 specs/ 下的一份 spec）：
// archive（压缩包）/ burn（刻录）/ crypto（加密与校验）/ image（图片）/ media（视频 + 音频 + ffmpeg 代理）/
// office（文档转换）/ pdf（PDF）/ tools（base64 / json / skill）；本包只做装配，不含命令实现。
package cli

import (
	"os"
	"os/signal"
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
	"github.com/guohub8080/zpack/internal/cmdutil"
	"github.com/guohub8080/zpack/internal/stdout"
)

// cliVersion 由构建注入的版本号（Execute 时保存，供命令读取）
var cliVersion = "dev"

// init 信号处理：第一次 Ctrl+C 优雅取消（清理半成品），第二次强制退出
func init() {
	sigChan := make(chan os.Signal, 2)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		core.RequestCancel()
		// 第二次信号强制退出（不等清理）
		<-sigChan
		os.Exit(130)
	}()
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
