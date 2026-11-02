// Package cli 装配 zpack 全部子命令（扁平形态：compress/extract，无格式子命令——
// 写格式由 --output 扩展名决定，读格式靠魔数识别）。7z/rar 读取与 GUI 壳按 AGENTS.md 路线图追加。
package cli

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

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
		Short:   "zpack——压缩/解压专项 CLI（zip 并行架构 + tar/tar.gz；7z/rar 解压规划中）",
		Version: version,
		PersistentPreRun: func(command *cobra.Command, args []string) {
			// 统一开始日志：所有命令入口处恒一行（装配层约束，命令代码不各自发）。
			// 只记命令名、绝不记参数——参数可能含密码/密钥，落终端或日志即泄密
			stdout.LoggerInfo(cmdutil.AppName + " " + command.Name() + " 开始执行")
		},
	}
	rootCommand.AddCommand(
		NewCompressCmd(),
		NewExtractCmd(),
		NewBase64Cmd(),
		NewCryptoCmd(),
		NewHashCmd(),
		NewListCmd(),
		NewTestCmd(),
		NewInfoCmd(),
		NewBenchCmd(),
		NewConvertCmd(),
		NewSkillCmd(),
		NewMediaCmd(),
		NewImageCmd(),
		NewAudioCmd(),
		NewFFmpegCmd(),
		NewPdfCmd(),
		NewOfficeCmd(),
		NewJSONCmd(),
	)
	if executeErr := rootCommand.Execute(); executeErr != nil {
		os.Exit(1)
	}
}
