// extract 命令：多格式解压入口——格式靠魔数识别不靠扩展名；智能解压、缺省跳过与 --force、密码与密码表。
package archive

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	engine "github.com/guohub8080/zpack/core"
	"github.com/guohub8080/zpack/internal/cliutil"
	"github.com/guohub8080/zpack/internal/cmdutil"
	"github.com/guohub8080/zpack/internal/stdout"
)

// NewExtractCmd extract 命令：多格式解压入口，格式靠魔数识别不靠扩展名
func NewExtractCmd() *cobra.Command {
	return &cobra.Command{
		Use: "extract <归档 .zip|.7z|.rar|.tar*|.gz 等> [--output 目录] [--jobs N] [--force] [--password 值] [--temp-dir 目录]",
		Long: `解压任意可读归档（26 种：zip/7z/rar/wim/cab/iso/tar 系/单流压缩/.001 分卷…），格式靠魔数识别不靠扩展名。

示例:
  zpack extract 归档.zip                    # 智能解压：单根直落 CWD，散包自动包一层目录
  zpack extract 归档.7z --output 目录/       # 指定目标目录
  zpack extract 加密.zip --password 值       # WinZip AES / ZipCrypto
  zpack extract 包.zip.001                  # 分卷首卷自动拼接

缺省跳过已存在（skipped 计数保护已改动文件），--force 覆盖；GBK 文件名自动修复；路径穿越/符号链接逃逸防护内置。
stdout 恒为一条 JSON：{"is_success":true,"output":"...","file_count":N,"size_bytes":N,"skipped":N}。`,
		Short:              "解压 zip/7z/rar/tar 系与单流压缩（智能解压、GBK 文件名修复、路径穿越防护、.001 分卷）",
		DisableFlagParsing: true,
		Run:                func(command *cobra.Command, args []string) { cliutil.HelpAwareRun(command, args, runExtract) },
	}
}

// resolveExtractJobsFlag 解压侧 --jobs：缺省 max(1, 核数/2)——业界主流惯例
// CArkParaExtMan::CalcThreadCount 逆向公式 min(设置 || 核数/2, 文件数, 32)
// （文件数封顶与 32 硬顶在 core 各管线内执行）
func resolveExtractJobsFlag(args []string) int {
	jobsText, hasJobs := cmdutil.FlagValue(args, "--jobs")
	if !hasJobs {
		return max(1, runtime.NumCPU()/2)
	}
	jobs, jobsOk := cmdutil.ParseIntStrict(jobsText)
	if !jobsOk || jobs < 1 || jobs > engine.MaxJobs {
		stdout.ErrorPrint("--jobs 需要 1-64 的整数，收到: " + jobsText)
	}
	return jobs
}

// parseExtractArgs 解析 extract 的位置参数与旗标：归档路径 / --output 目录 / --password 值
func parseExtractArgs(args []string) (archivePath string, outputDirPath string, password string, passwordTablePath string) {
	for argIndex := 0; argIndex < len(args); argIndex++ {
		switch {
		case args[argIndex] == "--password-table":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--password-table 缺少值（密码映射文件路径）")
			}
			passwordTablePath = args[argIndex+1]
			argIndex++
		case args[argIndex] == "--comment-file":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--comment-file 缺少值（注释文件路径）")
			}
			commentFilePath = args[argIndex+1]
			argIndex++
		case args[argIndex] == "--comment":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--comment 缺少值")
			}
			compressComment = args[argIndex+1]
			argIndex++
		case args[argIndex] == "--quiet":
			// --quiet：静默由 cliutil.EngineLogf(args) 统一判定，这里只占位（不被当作输入路径）
		case args[argIndex] == "--password":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--password 缺少值")
			}
			password = args[argIndex+1]
			argIndex++
		case args[argIndex] == "--output":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--output 缺少值")
			}
			outputDirPath = args[argIndex+1]
			argIndex++
		case args[argIndex] == "--jobs":
			// 数值旗标：占位跳过其值，值合法性由 resolveJobsFlag 严格校验
			if argIndex+1 < len(args) {
				argIndex++
			}
		case args[argIndex] == "--temp-dir":
			// 临时目录旗标：值由 applyTempDirFlag 统一注入，此处占位跳过防被当归档路径
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--temp-dir 缺少值（临时目录路径）")
			}
			argIndex++
		case strings.HasPrefix(args[argIndex], "-"):
			// 未知旗标静默忽略（宽容语义；--force 无值旗标在此吞掉，语义由 forceOverwrite 读取）
		default:
			if archivePath == "" {
				archivePath = args[argIndex]
			}
		}
	}
	return archivePath, outputDirPath, password, passwordTablePath
}

func runExtract(args []string) {
	applyTempDirFlag(args) // --temp-dir：cab 大文件夹溢写落点（缺省系统临时目录）
	archivePath, outputDirPath, password, passwordTablePath := parseExtractArgs(args)
	if archivePath == "" {
		stdout.ErrorPrint("请指定归档：extract <归档.zip|.tar|.tar.gz> [--output 目录] [--jobs N] [--force]")
	}
	forceOverwrite := cmdutil.HasFlag(args, "--force")
	jobs := resolveExtractJobsFlag(args)

	absoluteArchive, absErr := filepath.Abs(archivePath)
	if absErr != nil {
		absoluteArchive = archivePath
	}
	if _, statErr := os.Stat(absoluteArchive); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteArchive)
	}

	extractOptions := engine.ExtractOptions{
		Archive:   absoluteArchive,
		OutputDir: outputDirPath,
		Force:     forceOverwrite,
		Jobs:      jobs,
		Password:  password,
	}
	if passwordTablePath != "" {
		resolver, _, tableErr := parsePasswordTable(passwordTablePath)
		if tableErr != nil {
			stdout.ErrorPrint(tableErr.Error())
		}
		extractOptions.PasswordResolver = resolver
	}
	result, extractErr := engine.Extract(extractOptions, cliutil.EngineLogf(args))
	if extractErr != nil {
		if errors.Is(extractErr, engine.ErrCancelled) {
			stdout.ErrorPrint("操作已被用户取消（已解出的文件保留）")
		}
		stdout.ErrorPrint(extractErr.Error())
	}
	stdout.JSONPrint(extractResultJSON{
		Output: result.Output, FileCount: result.FileCount, SizeBytes: result.SizeBytes, Skipped: result.Skipped,
	})
}

// ── 输出契约 struct（信封 data 载荷；键名/键序=声明序，与契约文档同步）──

// extractResultJSON extract 成功载荷
type extractResultJSON struct {
	Output    string `json:"output"`
	FileCount int    `json:"file_count"`
	SizeBytes int64  `json:"size_bytes"`
	Skipped   int    `json:"skipped"`
}
